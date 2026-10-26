// ОлеговщинаСМС — простой мессенджер со сквозным шифрованием. Один сервер, без внешних зависимостей.
package main

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

//go:embed web/index.html
var indexHTML []byte

const maxCT = 3 << 20 // 3 МБ на одно зашифрованное сообщение

/* ---------- модель ---------- */

type User struct {
	ID      string            `json:"id"`
	Auth    string            `json:"auth"` // SHA-256 от секрета входа (сам секрет сервер не хранит)
	Pub     string            `json:"pub"`  // открытый ключ X25519
	EncPriv string            `json:"encPriv"`
	Name    string            `json:"name"`
	Nick    string            `json:"nick,omitempty"`
	Created int64             `json:"created"`
	Read    map[string]int64  `json:"read"` // собеседник -> прочитано до (мс)
	Blocked map[string]bool   `json:"blocked,omitempty"`
	Banned  bool              `json:"banned,omitempty"`
	Extra   map[string]string `json:"-"`
}

type Msg struct {
	ID   string `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
	TS   int64  `json:"ts"` // миллисекунды
	CT   string `json:"ct"` // шифротекст: base64url(iv ‖ AES-GCM)
}

type Store struct {
	mu       sync.RWMutex
	dir      string
	users    map[string]*User
	byAuth   map[string]string
	byNick   map[string]string // ник в нижнем регистре -> id
	sessions map[string]string // токен -> id
	msgs     map[string]*Msg
	conv     map[string][]*Msg             // "a|b" -> сообщения по времени
	reports  map[string]map[string]*Report // "post:id" -> кто пожаловался -> жалоба
	logf     *os.File
	// лента
	posts  []*Post // по времени
	postBy map[string]*Post
	cmts   map[string][]*Cmt // пост -> комментарии
	cmtBy  map[string]*Cmt
	flog   *os.File
}

func convKey(a, b string) string {
	if a < b {
		return a + "|" + b
	}
	return b + "|" + a
}

func randHex(n int) string { b := make([]byte, n); rand.Read(b); return hex.EncodeToString(b) }

func (s *Store) load() error {
	s.users, s.byAuth, s.sessions, s.msgs, s.conv = map[string]*User{}, map[string]string{}, map[string]string{}, map[string]*Msg{}, map[string][]*Msg{}
	if b, err := os.ReadFile(filepath.Join(s.dir, "users.json")); err == nil {
		if err := json.Unmarshal(b, &s.users); err != nil {
			return fmt.Errorf("users.json повреждён: %w", err)
		}
	}
	s.byNick = map[string]string{}
	for id, u := range s.users {
		s.byAuth[u.Auth] = id
		if u.Nick != "" {
			s.byNick[strings.ToLower(u.Nick)] = id
		}
		if u.Read == nil {
			u.Read = map[string]int64{}
		}
	}
	if b, err := os.ReadFile(filepath.Join(s.dir, "sessions.json")); err == nil {
		json.Unmarshal(b, &s.sessions)
	}
	if f, err := os.Open(filepath.Join(s.dir, "messages.jsonl")); err == nil {
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), maxCT+4096)
		for sc.Scan() {
			var rec struct {
				Msg
				Del string `json:"del"`
			}
			if json.Unmarshal(sc.Bytes(), &rec) != nil {
				continue
			}
			if rec.Del != "" {
				s.removeMsg(rec.Del)
				continue
			}
			m := rec.Msg
			s.addMsg(&m)
		}
		f.Close()
	}
	for _, list := range s.conv {
		sort.Slice(list, func(i, j int) bool { return list[i].TS < list[j].TS })
	}
	var err error
	s.logf, err = os.OpenFile(filepath.Join(s.dir, "messages.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	return s.loadFeed()
}

func (s *Store) addMsg(m *Msg) {
	s.msgs[m.ID] = m
	k := convKey(m.From, m.To)
	s.conv[k] = append(s.conv[k], m)
}

func (s *Store) removeMsg(id string) {
	m, ok := s.msgs[id]
	if !ok {
		return
	}
	delete(s.msgs, id)
	k := convKey(m.From, m.To)
	list := s.conv[k]
	for i, x := range list {
		if x.ID == id {
			s.conv[k] = append(list[:i:i], list[i+1:]...)
			break
		}
	}
}

func writeJSONFile(path string, v any) {
	b, _ := json.MarshalIndent(v, "", " ")
	tmp := path + ".tmp"
	if os.WriteFile(tmp, b, 0600) == nil {
		os.Rename(tmp, path)
	}
}

var saveMu sync.Mutex

func (s *Store) saveUsers() { // вызывать под s.mu
	b, _ := json.Marshal(s.users)
	sb, _ := json.Marshal(s.sessions)
	go func() {
		saveMu.Lock()
		defer saveMu.Unlock()
		for _, x := range []struct {
			p string
			b []byte
		}{{"users.json", b}, {"sessions.json", sb}} {
			tmp := filepath.Join(s.dir, x.p+".tmp")
			if os.WriteFile(tmp, x.b, 0600) == nil {
				os.Rename(tmp, filepath.Join(s.dir, x.p))
			}
		}
	}()
}

func (s *Store) appendLog(v any) {
	b, _ := json.Marshal(v)
	s.logf.Write(append(b, '\n'))
}

/* ---------- события в реальном времени (SSE) ---------- */

type Hub struct {
	mu   sync.Mutex
	subs map[string]map[chan []byte]bool
}

func (h *Hub) add(id string) chan []byte {
	c := make(chan []byte, 64)
	h.mu.Lock()
	if h.subs[id] == nil {
		h.subs[id] = map[chan []byte]bool{}
	}
	h.subs[id][c] = true
	h.mu.Unlock()
	return c
}
func (h *Hub) remove(id string, c chan []byte) {
	h.mu.Lock()
	delete(h.subs[id], c)
	if len(h.subs[id]) == 0 {
		delete(h.subs, id)
	}
	h.mu.Unlock()
}
func (h *Hub) send(id, typ string, data any) {
	b, _ := json.Marshal(data)
	frame := []byte("event: " + typ + "\ndata: " + string(b) + "\n\n")
	h.mu.Lock()
	for c := range h.subs[id] {
		select {
		case c <- frame:
		default:
		}
	}
	h.mu.Unlock()
}
func (h *Hub) online(id string) bool { h.mu.Lock(); defer h.mu.Unlock(); return len(h.subs[id]) > 0 }

/* ---------- ограничение частоты ---------- */

type Limiter struct {
	mu sync.Mutex
	m  map[string][]int64
}

func (l *Limiter) allow(key string, n int, per time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now().UnixNano()
	cut := now - int64(per)
	list := l.m[key]
	i := 0
	for i < len(list) && list[i] < cut {
		i++
	}
	list = list[i:]
	if len(list) >= n {
		l.m[key] = list
		return false
	}
	l.m[key] = append(list, now)
	return true
}

/* ---------- HTTP ---------- */

var (
	st      = &Store{}
	hub     = &Hub{subs: map[string]map[chan []byte]bool{}}
	lim     = &Limiter{m: map[string][]int64{}}
	invite  string
	srvName string
	reB64   = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	reID    = regexp.MustCompile(`^[0-9]{9}$`)
)

type apiErr struct {
	code int
	msg  string
}

func fail(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
func ok(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}
func readBody(r *http.Request, v any, max int64) error {
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, max)).Decode(v)
}
func clientIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}
func hashAuth(a string) string {
	s := sha256.Sum256([]byte("shepot/auth/" + a))
	return hex.EncodeToString(s[:])
}

func cleanName(n string) string {
	n = strings.TrimSpace(n)
	n = strings.Map(func(r rune) rune {
		if r < 32 || r == 0x7f {
			return -1
		}
		return r
	}, n)
	if utf8.RuneCountInString(n) > 40 {
		n = string([]rune(n)[:40])
	}
	return n
}

func authed(r *http.Request) (*User, string) {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if tok == "" {
		tok = r.URL.Query().Get("token")
	}
	if tok == "" {
		return nil, ""
	}
	st.mu.RLock()
	defer st.mu.RUnlock()
	id, okk := st.sessions[hashAuth(tok)]
	if !okk {
		return nil, ""
	}
	return st.users[id], tok
}

func withUser(h func(http.ResponseWriter, *http.Request, *User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, _ := authed(r)
		if u == nil {
			fail(w, 401, "Нужно войти заново")
			return
		}
		if u.Banned {
			fail(w, 423, "Ваш аккаунт заблокирован администратором")
			return
		}
		h(w, r, u)
	}
}

func newSession(id string) string { // под s.mu
	tok := randHex(32)
	st.sessions[hashAuth(tok)] = id
	return tok
}

func publicUser(u *User) map[string]any {
	return map[string]any{"id": u.ID, "name": u.Name, "nick": u.Nick, "pub": u.Pub, "online": hub.online(u.ID)}
}

func hRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		fail(w, 405, "POST")
		return
	}
	if !lim.allow("reg:"+clientIP(r), 5, time.Hour) {
		fail(w, 429, "Слишком много регистраций с этого адреса. Попробуйте позже")
		return
	}
	var in struct{ Auth, Pub, EncPriv, Name, Invite string }
	if readBody(r, &in, 8192) != nil || len(in.Auth) != 43 || !reB64.MatchString(in.Auth) || len(in.Pub) != 43 || !reB64.MatchString(in.Pub) || len(in.EncPriv) < 40 || len(in.EncPriv) > 200 || !reB64.MatchString(in.EncPriv) {
		fail(w, 400, "Неверные данные регистрации")
		return
	}
	if invite != "" && in.Invite != invite {
		fail(w, 403, "Неверный код приглашения")
		return
	}
	name := cleanName(in.Name)
	if name == "" {
		fail(w, 400, "Укажите имя")
		return
	}
	if bw := badWord(name); bw != "" {
		fail(w, 400, "Имя содержит запрещённое слово")
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	ah := hashAuth(in.Auth)
	if _, exists := st.byAuth[ah]; exists {
		fail(w, 409, "Такой ключ уже зарегистрирован")
		return
	}
	var id string
	for {
		n, _ := rand.Int(rand.Reader, big.NewInt(900000000))
		id = strconv.FormatInt(n.Int64()+100000000, 10)
		if _, e := st.users[id]; !e {
			break
		}
	}
	u := &User{ID: id, Auth: ah, Pub: in.Pub, EncPriv: in.EncPriv, Name: name, Created: time.Now().Unix(), Read: map[string]int64{}}
	st.users[id] = u
	st.byAuth[ah] = id
	tok := newSession(id)
	st.saveUsers()
	log.Printf("Новый пользователь %s (%s)", id, name)
	ok(w, map[string]any{"id": id, "token": tok, "name": name})
}

func hLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		fail(w, 405, "POST")
		return
	}
	if !lim.allow("login:"+clientIP(r), 20, 10*time.Minute) {
		fail(w, 429, "Слишком много попыток. Подождите 10 минут")
		return
	}
	var in struct{ Auth string }
	if readBody(r, &in, 1024) != nil || len(in.Auth) != 43 {
		fail(w, 400, "Неверный ключ")
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	id, found := st.byAuth[hashAuth(in.Auth)]
	if !found {
		fail(w, 404, "Аккаунт с таким ключом не найден")
		return
	}
	u := st.users[id]
	if u.Banned {
		fail(w, 423, "Аккаунт заблокирован администратором")
		return
	}
	tok := newSession(id)
	st.saveUsers()
	ok(w, map[string]any{"id": u.ID, "token": tok, "name": u.Name, "pub": u.Pub, "encPriv": u.EncPriv})
}

func hLogout(w http.ResponseWriter, r *http.Request) {
	_, tok := authed(r)
	if tok != "" {
		st.mu.Lock()
		delete(st.sessions, hashAuth(tok))
		st.saveUsers()
		st.mu.Unlock()
	}
	ok(w, map[string]bool{"ok": true})
}

func hMe(w http.ResponseWriter, r *http.Request, u *User) {
	if r.Method == "POST" {
		var in struct{ Name string }
		if readBody(r, &in, 1024) != nil || cleanName(in.Name) == "" {
			fail(w, 400, "Укажите имя")
			return
		}
		if badWord(in.Name) != "" {
			fail(w, 400, "Имя содержит запрещённое слово")
			return
		}
		st.mu.Lock()
		u.Name = cleanName(in.Name)
		st.saveUsers()
		peers := map[string]bool{}
		for k := range st.conv {
			if a, b, f := strings.Cut(k, "|"); f && (a == u.ID || b == u.ID) {
				peers[a], peers[b] = true, true
			}
		}
		st.mu.Unlock()
		for p := range peers {
			hub.send(p, "user", publicUser(u))
		}
	}
	st.mu.RLock()
	defer st.mu.RUnlock()
	ok(w, map[string]any{"id": u.ID, "name": u.Name, "nick": u.Nick, "pub": u.Pub, "encPriv": u.EncPriv, "blocked": keys(u.Blocked), "admin": admins[u.ID]})
}

func keys(m map[string]bool) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}

func hUser(w http.ResponseWriter, r *http.Request, me *User) {
	id := strings.ReplaceAll(strings.TrimSpace(r.URL.Query().Get("id")), " ", "")
	if !lim.allow("find:"+me.ID, 30, time.Minute) {
		fail(w, 429, "Слишком много запросов")
		return
	}
	st.mu.RLock()
	var u *User
	if reID.MatchString(id) {
		u = st.users[id]
	} else if nid, f := st.byNick[strings.ToLower(normNick(id))]; f {
		u = st.users[nid]
	}
	st.mu.RUnlock()
	if u == nil {
		fail(w, 404, "Пользователь не найден")
		return
	}
	ok(w, publicUser(u))
}

func hChats(w http.ResponseWriter, r *http.Request, me *User) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	out := []map[string]any{}
	for k, list := range st.conv {
		a, b, _ := strings.Cut(k, "|")
		if a != me.ID && b != me.ID {
			continue
		}
		peer := a
		if a == me.ID {
			peer = b
		}
		p := st.users[peer]
		if p == nil || len(list) == 0 {
			continue
		}
		last := list[len(list)-1]
		unread := 0
		for i := len(list) - 1; i >= 0 && list[i].TS > me.Read[peer]; i-- {
			if list[i].From != me.ID {
				unread++
			}
		}
		out = append(out, map[string]any{"peer": publicUser(p), "last": last, "unread": unread, "peerRead": p.Read[me.ID], "myRead": me.Read[peer]})
	}
	ok(w, out)
}

func hMessages(w http.ResponseWriter, r *http.Request, me *User) {
	q := r.URL.Query()
	peer := q.Get("peer")
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	if before == 0 {
		before = 1 << 62
	}
	st.mu.RLock()
	defer st.mu.RUnlock()
	list := st.conv[convKey(me.ID, peer)]
	end := sort.Search(len(list), func(i int) bool { return list[i].TS >= before })
	start := end - 100
	if start < 0 {
		start = 0
	}
	p := st.users[peer]
	pr := int64(0)
	if p != nil {
		pr = p.Read[me.ID]
	}
	ok(w, map[string]any{"msgs": list[start:end], "more": start > 0, "peerRead": pr})
}

func hSend(w http.ResponseWriter, r *http.Request, me *User) {
	if !lim.allow("send:"+me.ID, 40, 10*time.Second) {
		fail(w, 429, "Вы отправляете слишком часто")
		return
	}
	var in struct{ To, CT string }
	if err := readBody(r, &in, maxCT+1024); err != nil {
		fail(w, 413, "Сообщение слишком большое")
		return
	}
	if len(in.CT) < 30 || len(in.CT) > maxCT || !reB64.MatchString(in.CT) {
		fail(w, 400, "Неверное сообщение")
		return
	}
	st.mu.Lock()
	to := st.users[in.To]
	if to == nil {
		st.mu.Unlock()
		fail(w, 404, "Получатель не найден")
		return
	}
	if to.Blocked[me.ID] {
		st.mu.Unlock()
		fail(w, 403, "Пользователь ограничил входящие сообщения")
		return
	}
	ts := time.Now().UnixMilli()
	if list := st.conv[convKey(me.ID, in.To)]; len(list) > 0 && list[len(list)-1].TS >= ts {
		ts = list[len(list)-1].TS + 1
	}
	m := &Msg{ID: randHex(12), From: me.ID, To: in.To, TS: ts, CT: in.CT}
	st.addMsg(m)
	st.appendLog(m)
	me.Read[in.To] = ts
	st.mu.Unlock()
	hub.send(in.To, "msg", m)
	if in.To != me.ID {
		hub.send(me.ID, "msg", m)
	}
	ok(w, m)
}

func hRead(w http.ResponseWriter, r *http.Request, me *User) {
	var in struct {
		Peer string
		Upto int64
	}
	if readBody(r, &in, 1024) != nil {
		fail(w, 400, "Неверные данные")
		return
	}
	st.mu.Lock()
	if in.Upto > me.Read[in.Peer] && st.users[in.Peer] != nil {
		me.Read[in.Peer] = in.Upto
		st.saveUsers()
		st.mu.Unlock()
		hub.send(in.Peer, "read", map[string]any{"peer": me.ID, "upto": in.Upto})
		hub.send(me.ID, "myread", map[string]any{"peer": in.Peer, "upto": in.Upto})
	} else {
		st.mu.Unlock()
	}
	ok(w, map[string]bool{"ok": true})
}

func hDelete(w http.ResponseWriter, r *http.Request, me *User) {
	var in struct{ ID string }
	if readBody(r, &in, 1024) != nil {
		fail(w, 400, "Неверные данные")
		return
	}
	st.mu.Lock()
	m := st.msgs[in.ID]
	if m == nil || (m.From != me.ID) {
		st.mu.Unlock()
		fail(w, 404, "Сообщение не найдено")
		return
	}
	st.removeMsg(in.ID)
	st.appendLog(map[string]string{"del": in.ID})
	st.mu.Unlock()
	ev := map[string]string{"id": m.ID, "from": m.From, "to": m.To}
	hub.send(m.To, "del", ev)
	if m.To != me.ID {
		hub.send(me.ID, "del", ev)
	}
	ok(w, map[string]bool{"ok": true})
}

func hTyping(w http.ResponseWriter, r *http.Request, me *User) {
	var in struct{ To string }
	if readBody(r, &in, 256) == nil && lim.allow("typ:"+me.ID, 20, 10*time.Second) {
		hub.send(in.To, "typing", map[string]string{"from": me.ID})
	}
	ok(w, map[string]bool{"ok": true})
}

func hBlock(w http.ResponseWriter, r *http.Request, me *User) {
	var in struct {
		ID    string
		Block bool
	}
	if readBody(r, &in, 256) != nil {
		fail(w, 400, "Неверные данные")
		return
	}
	st.mu.Lock()
	if me.Blocked == nil {
		me.Blocked = map[string]bool{}
	}
	if in.Block {
		me.Blocked[in.ID] = true
	} else {
		delete(me.Blocked, in.ID)
	}
	st.saveUsers()
	st.mu.Unlock()
	ok(w, map[string]bool{"ok": true})
}

func hEvents(w http.ResponseWriter, r *http.Request, me *User) {
	fl, can := w.(http.Flusher)
	if !can {
		fail(w, 500, "Поток не поддерживается")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	c := hub.add(me.ID)
	defer hub.remove(me.ID, c)
	fmt.Fprint(w, "retry: 3000\nevent: hello\ndata: {}\n\n")
	fl.Flush()
	tick := time.NewTicker(25 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case f := <-c:
			if _, err := w.Write(f); err != nil {
				return
			}
			fl.Flush()
		case <-tick.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
	}
}

const csp = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self'; manifest-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

func hIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", csp)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-cache")
	w.Write(indexHTML)
}

func hInfo(w http.ResponseWriter, r *http.Request) {
	ok(w, map[string]any{"name": srvName, "invite": invite != ""})
}

const iconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 96 96"><rect width="96" height="96" rx="24" fill="#3b4cca"/><path d="M26 30h44a6 6 0 0 1 6 6v20a6 6 0 0 1-6 6H44l-12 10v-10h-6a6 6 0 0 1-6-6V36a6 6 0 0 1 6-6z" fill="#fff"/><circle cx="38" cy="46" r="3.5" fill="#3b4cca"/><circle cx="48" cy="46" r="3.5" fill="#3b4cca"/><circle cx="58" cy="46" r="3.5" fill="#3b4cca"/></svg>`

/* ---------- сертификат ---------- */

func localIPs() []net.IP {
	ips := []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, k := a.(*net.IPNet); k && !n.IP.IsLoopback() && n.IP.To4() != nil {
			ips = append(ips, n.IP)
		}
	}
	return ips
}

func ensureCert(dir string, extra []string) (string, string, error) {
	cp, kp := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if _, err := os.Stat(cp); err == nil {
		return cp, kp, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	host, _ := os.Hostname()
	tpl := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "OlegovshchinaSMS " + host},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(10, 0, 0),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: []string{"localhost", host}, IPAddresses: localIPs(),
	}
	for _, e := range extra {
		if ip := net.ParseIP(e); ip != nil {
			tpl.IPAddresses = append(tpl.IPAddresses, ip)
		} else if e != "" {
			tpl.DNSNames = append(tpl.DNSNames, e)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return "", "", err
	}
	kb, _ := x509.MarshalECPrivateKey(key)
	os.WriteFile(cp, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600)
	os.WriteFile(kp, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0600)
	log.Println("Создан сертификат для HTTPS:", cp)
	return cp, kp, nil
}

/* ---------- запуск ---------- */

// fatal показывает ошибку и ждёт Enter, чтобы окно не закрылось сразу (при запуске двойным щелчком)
func fatal(v ...any) {
	fmt.Println()
	fmt.Println("ОШИБКА:", fmt.Sprint(v...))
	fmt.Println("Нажмите Enter, чтобы закрыть окно")
	bufio.NewReader(os.Stdin).ReadString('\n')
	os.Exit(1)
}

func main() {
	port := flag.Int("port", 8443, "порт")
	dataDir := flag.String("data", "", "папка с данными (по умолчанию shepot-data рядом с программой)")
	certF := flag.String("cert", "", "свой сертификат (PEM)")
	keyF := flag.String("key", "", "ключ сертификата (PEM)")
	plain := flag.Bool("http", false, "без HTTPS (только если перед сервером стоит прокси с HTTPS)")
	host := flag.String("host", "", "доменное имя или внешний IP для самоподписанного сертификата")
	flag.StringVar(&invite, "invite", "", "код приглашения: без него зарегистрироваться нельзя")
	flag.StringVar(&srvName, "name", "ОлеговщинаСМС", "название сервера")
	adminF := flag.String("admin", "", "ID модераторов через запятую: могут удалять любые посты и комментарии")
	flag.Parse()
	for _, a := range strings.Split(*adminF, ",") {
		if a = strings.ReplaceAll(strings.TrimSpace(a), " ", ""); a != "" {
			admins[a] = true
		}
	}
	log.SetFlags(log.Ldate | log.Ltime)

	if *dataDir == "" {
		exe, _ := os.Executable()
		*dataDir = filepath.Join(filepath.Dir(exe), "shepot-data")
		if strings.Contains(exe, "go-build") {
			*dataDir = "shepot-data"
		}
	}
	if err := os.MkdirAll(*dataDir, 0700); err != nil {
		fatal("Не могу создать папку данных: ", err)
	}
	st.dir = *dataDir
	if err := st.load(); err != nil {
		fatal(err)
	}
	loadMod(*dataDir)
	loadTanks(*dataDir)

	mux := http.NewServeMux()
	mux.HandleFunc("/", hIndex)
	mux.HandleFunc("/api/info", hInfo)
	mux.HandleFunc("/api/register", hRegister)
	mux.HandleFunc("/api/login", hLogin)
	mux.HandleFunc("/api/logout", hLogout)
	mux.HandleFunc("/api/me", withUser(hMe))
	mux.HandleFunc("/api/user", withUser(hUser))
	mux.HandleFunc("/api/chats", withUser(hChats))
	mux.HandleFunc("/api/messages", withUser(hMessages))
	mux.HandleFunc("/api/send", withUser(hSend))
	mux.HandleFunc("/api/read", withUser(hRead))
	mux.HandleFunc("/api/delete", withUser(hDelete))
	mux.HandleFunc("/api/typing", withUser(hTyping))
	mux.HandleFunc("/api/block", withUser(hBlock))
	mux.HandleFunc("/api/events", withUser(hEvents))
	mux.HandleFunc("/api/feed", withUser(hFeed))
	mux.HandleFunc("/api/post", withUser(hPost))
	mux.HandleFunc("/api/like", withUser(hLike))
	mux.HandleFunc("/api/comments", withUser(hComments))
	mux.HandleFunc("/api/comment", withUser(hComment))
	mux.HandleFunc("/api/delpost", withUser(hDelPost))
	mux.HandleFunc("/api/delcmt", withUser(hDelCmt))
	mux.HandleFunc("/api/tags", withUser(hTags))
	mux.HandleFunc("/api/nick", withUser(hNick))
	mux.HandleFunc("/api/report", withUser(hReport))
	mux.HandleFunc("/game", hGamePage)
	mux.HandleFunc("/api/game/ws", hGameWS)
	mux.HandleFunc("/api/game/info", withUser(hGameInfo))
	mux.HandleFunc("/tanks", hTanksPage)
	mux.HandleFunc("/api/tanks/ws", hTanksWS)
	mux.HandleFunc("/api/tanks/info", withUser(hTanksInfo))
	mux.HandleFunc("/api/tanks/admin", adminOnly(hTanksAdmin))
	mux.HandleFunc("/api/admin/queue", adminOnly(hAdminQueue))
	mux.HandleFunc("/api/admin/approve", adminOnly(hAdminApprove))
	mux.HandleFunc("/api/admin/dismiss", adminOnly(hAdminDismiss))
	mux.HandleFunc("/api/admin/users", adminOnly(hAdminUsers))
	mux.HandleFunc("/api/admin/ban", adminOnly(hAdminBan))
	mux.HandleFunc("/api/admin/purge", adminOnly(hAdminPurge))
	mux.HandleFunc("/api/admin/deluser", adminOnly(hAdminDelUser))
	mux.HandleFunc("/api/admin/settings", adminOnly(hAdminSettings))
	mux.HandleFunc("/api/nickfree", withUser(hNickFree))
	mux.HandleFunc("/img/", hImg)
	mux.HandleFunc("/icon.svg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Write([]byte(iconSVG))
	})
	mux.HandleFunc("/manifest.webmanifest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/manifest+json")
		json.NewEncoder(w).Encode(map[string]any{"name": srvName, "short_name": srvName, "start_url": "/", "display": "standalone", "background_color": "#ffffff", "theme_color": "#3b4cca", "icons": []map[string]string{{"src": "/icon.svg", "sizes": "any", "type": "image/svg+xml"}}})
	})

	srv := &http.Server{Addr: fmt.Sprintf(":%d", *port), Handler: mux, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}
	srv.TLSNextProto = map[string]func(*http.Server, *tls.Conn, http.Handler){} // только HTTP/1.1: нужно для WebSocket
	scheme := "https"
	if *plain {
		scheme = "http"
	} else {
		cf, kf := *certF, *keyF
		if cf == "" || kf == "" {
			var err error
			cf, kf, err = ensureCert(*dataDir, []string{*host})
			if err != nil {
				fatal("Не удалось создать сертификат: ", err)
			}
		}
		cert, err := tls.LoadX509KeyPair(cf, kf)
		if err != nil {
			fatal("Не удалось загрузить сертификат: ", err)
		}
		srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	}

	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
		<-ch
		st.mu.Lock()
		writeJSONFile(filepath.Join(st.dir, "users.json"), st.users)
		writeJSONFile(filepath.Join(st.dir, "sessions.json"), st.sessions)
		st.logf.Sync()
		st.flog.Sync()
		st.mu.Unlock()
		saveTanks()
		log.Println("Сервер остановлен")
		os.Exit(0)
	}()

	fmt.Println("────────────────────────────────────────────")
	fmt.Printf(" %s запущен. Пользователей: %d\n", srvName, len(st.users))
	fmt.Println(" Откройте в браузере:")
	fmt.Printf("   %s://localhost:%d\n", scheme, *port)
	for _, ip := range localIPs()[2:] {
		fmt.Printf("   %s://%s:%d   (для устройств в вашей сети)\n", scheme, ip, *port)
	}
	if invite != "" {
		fmt.Println(" Регистрация только по коду приглашения:", invite)
	}
	fmt.Println(" Данные:", st.dir)
	fmt.Println(" Остановить: Ctrl+C")
	fmt.Println("────────────────────────────────────────────")

	var err error
	if *plain {
		err = srv.ListenAndServe()
	} else {
		err = srv.ListenAndServeTLS("", "")
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		fatal("Ошибка запуска: ", err, "\nВозможно, порт занят. Попробуйте другой: -port 9443")
	}
}

var _ = base64.RawURLEncoding
