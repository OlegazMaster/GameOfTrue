// Модерация: стоп-слова, жалобы, премодерация картинок, блокировка и удаление аккаунтов.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

type Report struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"` // post | cmt | user
	Target string `json:"target"`
	By     string `json:"by"`
	Reason string `json:"reason"`
	TS     int64  `json:"ts"`
}

const hideAfter = 3 // столько жалоб от разных людей — и пост скрывается до решения модератора

var reasons = map[string]string{
	"porn":    "Порнография",
	"nazi":    "Нацизм, экстремизм",
	"violent": "Насилие, жестокость",
	"spam":    "Спам, мошенничество",
	"other":   "Другое",
}

func hiddenBy(m map[string]*Report) bool { return len(m) >= hideAfter }

func canSeePost(p *Post, me *User) bool { // под s.mu (чтение)
	if admins[me.ID] || p.Author == me.ID {
		return true
	}
	if p.Pending || hiddenBy(st.reports["post:"+p.ID]) {
		return false
	}
	u := st.users[p.Author]
	return u != nil && !u.Banned
}

func canSeeCmt(c *Cmt, me *User) bool { // под s.mu (чтение)
	if admins[me.ID] || c.Author == me.ID {
		return true
	}
	if hiddenBy(st.reports["cmt:"+c.ID]) {
		return false
	}
	u := st.users[c.Author]
	return u != nil && !u.Banned
}

/* ---------- настройки ---------- */

type Settings struct {
	Premod bool `json:"premod"` // посты с картинками публикуются только после проверки
}

var (
	setMu    sync.RWMutex
	settings = Settings{Premod: true}
)

func getSettings() Settings { setMu.RLock(); defer setMu.RUnlock(); return settings }

/* ---------- стоп-слова ---------- */

const defaultWords = `# Запрещённые слова и фразы. Одна строка — одно слово или фраза.
# Слово срабатывает, если с него начинается слово в тексте:
# «порн» ловит «порно», «порнуха», но не «топорно».
# Строки, которые начинаются с #, — комментарии. Мат здесь не запрещён.
# Порнография
порн
porn
xvideos
xhamster
onlyfans
онлифанс
hentai
хентай
# Нацизм
зиг хайль
зигхайль
sieg heil
хайль гитлер
heil hitler
слава гитлер
1488
14 88
white power
卐
卍
ᛋᛋ
`

type word struct{ orig, norm string }

var (
	wordsMu  sync.RWMutex
	words    []word
	wordsRaw string
)

// fold приводит похожие латинские и кириллические буквы к одному виду, чтобы «pоrnо» (смесь алфавитов) не обходило фильтр.
var fold = map[rune]rune{'а': 'a', 'е': 'e', 'ё': 'e', 'о': 'o', 'р': 'p', 'с': 'c', 'х': 'x', 'у': 'y', 'к': 'k', 'м': 'm', 'т': 't', 'в': 'b', 'н': 'h', 'і': 'i', '0': 'o'}

// tokens режет текст на слова. Буквы, разделённые пробелами («п о р н о»), склеиваются в одно слово.
func tokens(s string) []string {
	s = strings.ToLower(s)
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, string(cur))
			cur = nil
		}
	}
	for _, r := range s {
		if f, ok := fold[r]; ok {
			r = f
		}
		switch {
		case r == 0x200b || r == 0x200c || r == 0x200d || r == 0xfeff || r == 0xad:
			continue
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			cur = append(cur, r)
		case unicode.IsSpace(r) || unicode.IsPunct(r):
			flush()
		default: // символы вроде свастики — отдельным словом
			flush()
			out = append(out, string(r))
		}
	}
	flush()
	// склеить подряд идущие одиночные буквы
	var res []string
	for i := 0; i < len(out); {
		if utf8.RuneCountInString(out[i]) == 1 && unicode.IsLetter([]rune(out[i])[0]) {
			j := i
			var b strings.Builder
			for j < len(out) && utf8.RuneCountInString(out[j]) == 1 && unicode.IsLetter([]rune(out[j])[0]) {
				b.WriteString(out[j])
				j++
			}
			if j-i >= 3 {
				res = append(res, b.String())
			} else {
				res = append(res, out[i:j]...)
			}
			i = j
		} else {
			res = append(res, out[i])
			i++
		}
	}
	return res
}

func setWords(raw string) {
	var list []word
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if t := tokens(line); len(t) > 0 {
			list = append(list, word{line, strings.Join(t, " ")})
		}
	}
	wordsMu.Lock()
	words, wordsRaw = list, raw
	wordsMu.Unlock()
}

// badWord возвращает найденное запрещённое слово или "".
func badWord(s string) string {
	t := " " + strings.Join(tokens(s), " ")
	wordsMu.RLock()
	defer wordsMu.RUnlock()
	for _, w := range words {
		if strings.Contains(t, " "+w.norm) {
			return w.orig
		}
	}
	return ""
}

func loadMod(dir string) {
	wp := filepath.Join(dir, "banned-words.txt")
	b, err := os.ReadFile(wp)
	if err != nil {
		os.WriteFile(wp, []byte(defaultWords), 0600)
		b = []byte(defaultWords)
	}
	setWords(string(b))
	if b, err := os.ReadFile(filepath.Join(dir, "settings.json")); err == nil {
		json.Unmarshal(b, &settings)
	}
}

/* ---------- жалобы ---------- */

func notifyAdmins() {
	for id := range admins {
		hub.send(id, "modq", map[string]bool{"ok": true})
	}
}

// POST /api/report {kind, id, reason}
func hReport(w http.ResponseWriter, r *http.Request, me *User) {
	var in struct{ Kind, ID, Reason string }
	if readBody(r, &in, 1024) != nil || reasons[in.Reason] == "" {
		fail(w, 400, "Неверные данные")
		return
	}
	if !lim.allow("report:"+me.ID, 30, time.Hour) {
		fail(w, 429, "Слишком много жалоб. Попробуйте позже")
		return
	}
	st.mu.Lock()
	exists := false
	switch in.Kind {
	case "post":
		exists = st.postBy[in.ID] != nil
	case "cmt":
		exists = st.cmtBy[in.ID] != nil
	case "user":
		exists = st.users[in.ID] != nil
	}
	if !exists || in.ID == me.ID {
		st.mu.Unlock()
		fail(w, 404, "Не найдено")
		return
	}
	rec := feedRec{Report: &Report{ID: randHex(8), Kind: in.Kind, Target: in.ID, By: me.ID, Reason: in.Reason, TS: time.Now().UnixMilli()}}
	st.applyFeed(rec)
	st.feedLog(rec)
	st.mu.Unlock()
	notifyAdmins()
	ok(w, map[string]bool{"ok": true})
}

/* ---------- админка ---------- */

func adminOnly(h func(http.ResponseWriter, *http.Request, *User)) http.HandlerFunc {
	return withUser(func(w http.ResponseWriter, r *http.Request, u *User) {
		if !admins[u.ID] {
			fail(w, 403, "Только для администратора")
			return
		}
		h(w, r, u)
	})
}

func adminUserView(u *User) map[string]any { // под s.mu
	n := 0
	for _, p := range st.posts {
		if p.Author == u.ID {
			n++
		}
	}
	return map[string]any{"id": u.ID, "name": u.Name, "nick": u.Nick, "created": u.Created, "banned": u.Banned, "admin": admins[u.ID], "posts": n, "online": hub.online(u.ID)}
}

// GET /api/admin/queue — посты на проверке и жалобы
func hAdminQueue(w http.ResponseWriter, r *http.Request, me *User) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	pending := []map[string]any{}
	for i := len(st.posts) - 1; i >= 0; i-- {
		if st.posts[i].Pending {
			pending = append(pending, postView(st.posts[i], me.ID))
		}
	}
	type item struct {
		Kind    string         `json:"kind"`
		Target  string         `json:"target"`
		Count   int            `json:"count"`
		Reasons []string       `json:"reasons"`
		TS      int64          `json:"ts"`
		Hidden  bool           `json:"hidden"`
		Post    map[string]any `json:"post,omitempty"`
		Cmt     map[string]any `json:"cmt,omitempty"`
		User    map[string]any `json:"user,omitempty"`
	}
	items := []item{}
	for k, m := range st.reports {
		if len(m) == 0 {
			continue
		}
		kind, target, _ := strings.Cut(k, ":")
		it := item{Kind: kind, Target: target, Count: len(m), Hidden: hiddenBy(m)}
		seen := map[string]bool{}
		for _, rep := range m {
			if rep.TS > it.TS {
				it.TS = rep.TS
			}
			if !seen[rep.Reason] {
				seen[rep.Reason] = true
				it.Reasons = append(it.Reasons, reasons[rep.Reason])
			}
		}
		switch kind {
		case "post":
			if p := st.postBy[target]; p != nil {
				it.Post = postView(p, me.ID)
			}
		case "cmt":
			if c := st.cmtBy[target]; c != nil {
				it.Cmt = cmtView(c)
			}
		case "user":
			if u := st.users[target]; u != nil {
				it.User = adminUserView(u)
			}
		}
		if it.Post == nil && it.Cmt == nil && it.User == nil {
			continue
		}
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].TS > items[j].TS })
	ok(w, map[string]any{"pending": pending, "reports": items})
}

// POST /api/admin/approve {id}
func hAdminApprove(w http.ResponseWriter, r *http.Request, me *User) {
	var in struct{ ID string }
	readBody(r, &in, 512)
	st.mu.Lock()
	p := st.postBy[in.ID]
	if p == nil {
		st.mu.Unlock()
		fail(w, 404, "Пост не найден")
		return
	}
	rec := feedRec{Approve: p.ID}
	st.applyFeed(rec)
	st.feedLog(rec)
	st.mu.Unlock()
	hub.all("post", map[string]string{"id": p.ID, "author": p.Author})
	hub.send(p.Author, "approved", map[string]string{"id": p.ID})
	ok(w, map[string]bool{"ok": true})
}

// POST /api/admin/dismiss {kind, id} — жалобы необоснованны, оставить
func hAdminDismiss(w http.ResponseWriter, r *http.Request, me *User) {
	var in struct{ Kind, ID string }
	readBody(r, &in, 512)
	st.mu.Lock()
	rec := feedRec{Dismiss: in.Kind + ":" + in.ID}
	st.applyFeed(rec)
	st.feedLog(rec)
	st.mu.Unlock()
	ok(w, map[string]bool{"ok": true})
}

// GET /api/admin/users?q=
func hAdminUsers(w http.ResponseWriter, r *http.Request, me *User) {
	q := strings.ToLower(strings.TrimPrefix(strings.ReplaceAll(strings.TrimSpace(r.URL.Query().Get("q")), " ", ""), "@"))
	st.mu.RLock()
	defer st.mu.RUnlock()
	list := []*User{}
	for _, u := range st.users {
		if q == "" || strings.Contains(u.ID, q) || strings.Contains(strings.ToLower(strings.ReplaceAll(u.Name, " ", "")), q) || strings.Contains(strings.ToLower(u.Nick), q) {
			list = append(list, u)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Created > list[j].Created })
	out := []map[string]any{}
	for i, u := range list {
		if i == 100 {
			break
		}
		out = append(out, adminUserView(u))
	}
	ok(w, map[string]any{"users": out, "total": len(st.users)})
}

type purged struct {
	posts []*Post
	cmts  []*Cmt
}

// purgeContent удаляет все посты, комментарии и лайки пользователя. Под s.mu.
func purgeContent(id string) purged {
	var res purged
	for _, p := range append([]*Post(nil), st.posts...) {
		if p.Author == id {
			res.posts = append(res.posts, p)
			rec := feedRec{DelPost: p.ID}
			st.applyFeed(rec)
			st.feedLog(rec)
		} else if p.likes[id] {
			rec := feedRec{Like: p.ID, By: id, On: false}
			st.applyFeed(rec)
			st.feedLog(rec)
		}
	}
	for _, c := range st.cmtBy {
		if c.Author == id {
			res.cmts = append(res.cmts, c)
		}
	}
	for _, c := range res.cmts {
		rec := feedRec{DelCmt: c.ID}
		st.applyFeed(rec)
		st.feedLog(rec)
	}
	return res
}

func announcePurge(pg purged) { // без блокировки
	for _, p := range pg.posts {
		for _, im := range p.Imgs {
			os.Remove(filepath.Join(st.dir, "img", im+".jpg"))
		}
		hub.all("pdel", map[string]string{"id": p.ID})
	}
	for _, c := range pg.cmts {
		st.mu.RLock()
		n := 0
		if p := st.postBy[c.Post]; p != nil {
			n = p.nc
		}
		st.mu.RUnlock()
		hub.all("cdel", map[string]any{"post": c.Post, "id": c.ID, "n": n})
	}
}

func dropSessions(id string) { // под s.mu
	for k, v := range st.sessions {
		if v == id {
			delete(st.sessions, k)
		}
	}
}

// POST /api/admin/ban {id, ban, purge}
func hAdminBan(w http.ResponseWriter, r *http.Request, me *User) {
	var in struct {
		ID         string
		Ban, Purge bool
	}
	readBody(r, &in, 512)
	st.mu.Lock()
	u := st.users[in.ID]
	if u == nil || admins[u.ID] {
		st.mu.Unlock()
		fail(w, 400, "Этого пользователя нельзя заблокировать")
		return
	}
	u.Banned = in.Ban
	if in.Ban {
		dropSessions(u.ID)
	}
	var pg purged
	if in.Purge {
		pg = purgeContent(u.ID)
	}
	st.saveUsers()
	st.mu.Unlock()
	announcePurge(pg)
	if in.Ban {
		hub.send(u.ID, "banned", map[string]bool{"ok": true})
		log.Printf("Админ %s заблокировал %s (%s)", me.ID, u.ID, u.Name)
	}
	ok(w, map[string]bool{"ok": true})
}

// POST /api/admin/purge {id} — удалить все посты и комментарии пользователя
func hAdminPurge(w http.ResponseWriter, r *http.Request, me *User) {
	var in struct{ ID string }
	readBody(r, &in, 512)
	st.mu.Lock()
	pg := purgeContent(in.ID)
	st.mu.Unlock()
	announcePurge(pg)
	ok(w, map[string]int{"posts": len(pg.posts), "cmts": len(pg.cmts)})
}

// POST /api/admin/deluser {id} — удалить аккаунт полностью
func hAdminDelUser(w http.ResponseWriter, r *http.Request, me *User) {
	var in struct{ ID string }
	readBody(r, &in, 512)
	st.mu.Lock()
	u := st.users[in.ID]
	if u == nil || admins[u.ID] {
		st.mu.Unlock()
		fail(w, 400, "Этого пользователя нельзя удалить")
		return
	}
	pg := purgeContent(u.ID)
	for _, m := range st.msgs {
		if m.From == u.ID || m.To == u.ID {
			st.removeMsg(m.ID)
			st.appendLog(map[string]string{"del": m.ID})
		}
	}
	rec := feedRec{Dismiss: "user:" + u.ID}
	st.applyFeed(rec)
	st.feedLog(rec)
	dropSessions(u.ID)
	delete(st.byAuth, u.Auth)
	if u.Nick != "" {
		delete(st.byNick, strings.ToLower(u.Nick))
	}
	delete(st.users, u.ID)
	st.saveUsers()
	st.mu.Unlock()
	announcePurge(pg)
	hub.send(u.ID, "banned", map[string]bool{"ok": true})
	log.Printf("Админ %s удалил аккаунт %s (%s)", me.ID, u.ID, u.Name)
	ok(w, map[string]bool{"ok": true})
}

// GET/POST /api/admin/settings {premod, words}
func hAdminSettings(w http.ResponseWriter, r *http.Request, me *User) {
	if r.Method == "POST" {
		var in struct {
			Premod bool
			Words  *string
		}
		if readBody(r, &in, 256<<10) != nil {
			fail(w, 400, "Неверные данные")
			return
		}
		setMu.Lock()
		settings.Premod = in.Premod
		b, _ := json.MarshalIndent(settings, "", " ")
		setMu.Unlock()
		os.WriteFile(filepath.Join(st.dir, "settings.json"), b, 0600)
		if in.Words != nil {
			raw := strings.ReplaceAll(*in.Words, "\r\n", "\n")
			setWords(raw)
			os.WriteFile(filepath.Join(st.dir, "banned-words.txt"), []byte(raw), 0600)
		}
	}
	wordsMu.RLock()
	raw := wordsRaw
	wordsMu.RUnlock()
	ok(w, map[string]any{"premod": getSettings().Premod, "words": raw})
}
