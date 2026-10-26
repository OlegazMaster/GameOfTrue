// Лента: публичные посты, картинки, лайки, комментарии, хештеги.
// Посты не шифруются сквозным шифрованием: их видят все пользователи сервера.
package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type Post struct {
	ID      string   `json:"id"`
	Author  string   `json:"author"`
	TS      int64    `json:"ts"`
	Text    string   `json:"text"`
	Imgs    []string `json:"imgs,omitempty"`
	Tags    []string `json:"tags,omitempty"`
	Pending bool     `json:"pending,omitempty"` // ждёт проверки модератором (пост с картинками)
	likes   map[string]bool
	nc      int
}

type Cmt struct {
	ID     string `json:"id"`
	Post   string `json:"post"`
	Author string `json:"author"`
	TS     int64  `json:"ts"`
	Text   string `json:"text"`
}

type feedRec struct {
	Post    *Post   `json:"post,omitempty"`
	Cmt     *Cmt    `json:"cmt,omitempty"`
	Like    string  `json:"like,omitempty"`
	By      string  `json:"by,omitempty"`
	On      bool    `json:"on,omitempty"`
	DelPost string  `json:"delPost,omitempty"`
	DelCmt  string  `json:"delCmt,omitempty"`
	Report  *Report `json:"report,omitempty"`
	Dismiss string  `json:"dismiss,omitempty"` // "post:id" — снять жалобы
	Approve string  `json:"approve,omitempty"` // одобрить пост
}

const (
	maxPostRunes = 3000
	maxCmtRunes  = 1000
	maxImgBytes  = 600 << 10
	maxImgs      = 4
	pageSize     = 20
)

var (
	reTag  = regexp.MustCompile(`#([\p{L}\p{N}_]{1,50})`)
	reImg  = regexp.MustCompile(`^/img/([0-9a-f]{24})\.jpg$`)
	admins = map[string]bool{}
)

func (s *Store) loadFeed() error {
	s.postBy, s.cmts, s.cmtBy, s.reports = map[string]*Post{}, map[string][]*Cmt{}, map[string]*Cmt{}, map[string]map[string]*Report{}
	path := filepath.Join(s.dir, "posts.jsonl")
	if f, err := os.Open(path); err == nil {
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			var r feedRec
			if json.Unmarshal(sc.Bytes(), &r) == nil {
				s.applyFeed(r)
			}
		}
		f.Close()
	}
	sort.SliceStable(s.posts, func(i, j int) bool { return s.posts[i].TS < s.posts[j].TS })
	var err error
	s.flog, err = os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	return err
}

func (s *Store) applyFeed(r feedRec) { // под s.mu
	switch {
	case r.Post != nil:
		p := r.Post
		if p.ID == "" || s.postBy[p.ID] != nil {
			return
		}
		p.likes = map[string]bool{}
		s.posts = append(s.posts, p)
		s.postBy[p.ID] = p
	case r.Cmt != nil:
		c := r.Cmt
		if p := s.postBy[c.Post]; p != nil && s.cmtBy[c.ID] == nil {
			s.cmts[p.ID] = append(s.cmts[p.ID], c)
			s.cmtBy[c.ID] = c
			p.nc++
		}
	case r.Like != "":
		if p := s.postBy[r.Like]; p != nil {
			if r.On {
				p.likes[r.By] = true
			} else {
				delete(p.likes, r.By)
			}
		}
	case r.Report != nil:
		k := r.Report.Kind + ":" + r.Report.Target
		if s.reports[k] == nil {
			s.reports[k] = map[string]*Report{}
		}
		s.reports[k][r.Report.By] = r.Report
	case r.Dismiss != "":
		delete(s.reports, r.Dismiss)
	case r.Approve != "":
		if p := s.postBy[r.Approve]; p != nil {
			p.Pending = false
		}
	case r.DelPost != "":
		p := s.postBy[r.DelPost]
		if p == nil {
			return
		}
		delete(s.reports, "post:"+p.ID)
		delete(s.postBy, p.ID)
		for i, x := range s.posts {
			if x == p {
				s.posts = append(s.posts[:i:i], s.posts[i+1:]...)
				break
			}
		}
		for _, c := range s.cmts[p.ID] {
			delete(s.cmtBy, c.ID)
		}
		delete(s.cmts, p.ID)
	case r.DelCmt != "":
		c := s.cmtBy[r.DelCmt]
		if c == nil {
			return
		}
		delete(s.cmtBy, c.ID)
		delete(s.reports, "cmt:"+c.ID)
		list := s.cmts[c.Post]
		for i, x := range list {
			if x == c {
				s.cmts[c.Post] = append(list[:i:i], list[i+1:]...)
				break
			}
		}
		if p := s.postBy[c.Post]; p != nil {
			p.nc--
		}
	}
}

func (s *Store) feedLog(r feedRec) { // под s.mu
	b, _ := json.Marshal(r)
	s.flog.Write(append(b, '\n'))
}

/* ---------- вспомогательное ---------- */

func cleanText(s string, max int) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.Map(func(r rune) rune {
		if (r < 32 && r != '\n' && r != '\t') || r == 0x7f {
			return -1
		}
		return r
	}, s)
	for strings.Contains(s, "\n\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n\n", "\n\n\n")
	}
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > max {
		s = string([]rune(s)[:max])
	}
	return s
}

func extractTags(text string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, m := range reTag.FindAllStringSubmatch(text, -1) {
		t := strings.ToLower(m[1])
		if !seen[t] && len(out) < 10 {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

func userBrief(id string) map[string]any { // под s.mu (чтение)
	u := st.users[id]
	if u == nil {
		return map[string]any{"id": id, "name": "Удалённый аккаунт"}
	}
	return map[string]any{"id": u.ID, "name": u.Name, "nick": u.Nick}
}

func postView(p *Post, me string) map[string]any { // под s.mu (чтение)
	imgs := []string{}
	for _, id := range p.Imgs {
		imgs = append(imgs, "/img/"+id+".jpg")
	}
	return map[string]any{"id": p.ID, "author": userBrief(p.Author), "ts": p.TS, "text": p.Text, "imgs": imgs,
		"likes": len(p.likes), "liked": p.likes[me], "cmts": p.nc, "pending": p.Pending, "hidden": hiddenBy(st.reports["post:"+p.ID])}
}

func cmtView(c *Cmt) map[string]any {
	return map[string]any{"id": c.ID, "post": c.Post, "author": userBrief(c.Author), "ts": c.TS, "text": c.Text}
}

func (h *Hub) all(typ string, data any) {
	b, _ := json.Marshal(data)
	frame := []byte("event: " + typ + "\ndata: " + string(b) + "\n\n")
	h.mu.Lock()
	for _, set := range h.subs {
		for c := range set {
			select {
			case c <- frame:
			default:
			}
		}
	}
	h.mu.Unlock()
}

/* ---------- обработчики ---------- */

// GET /api/feed?before=&tag=&q=&author=
func hFeed(w http.ResponseWriter, r *http.Request, me *User) {
	q := r.URL.Query()
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	if before == 0 {
		before = 1 << 62
	}
	tag := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(q.Get("tag")), "#"))
	text := strings.ToLower(strings.TrimSpace(q.Get("q")))
	author := q.Get("author")
	st.mu.RLock()
	defer st.mu.RUnlock()
	out := []map[string]any{}
	more := false
	for i := len(st.posts) - 1; i >= 0; i-- {
		p := st.posts[i]
		if p.TS >= before || (author != "" && p.Author != author) || me.Blocked[p.Author] || !canSeePost(p, me) {
			continue
		}
		if tag != "" {
			found := false
			for _, t := range p.Tags {
				if t == tag {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		if text != "" && !strings.Contains(strings.ToLower(p.Text), text) {
			continue
		}
		if len(out) == pageSize {
			more = true
			break
		}
		out = append(out, postView(p, me.ID))
	}
	ok(w, map[string]any{"posts": out, "more": more})
}

// POST /api/post {text, imgs:[base64 jpeg]}
func hPost(w http.ResponseWriter, r *http.Request, me *User) {
	if r.Method != "POST" {
		fail(w, 405, "POST")
		return
	}
	var in struct {
		Text string
		Imgs []string
	}
	if err := readBody(r, &in, maxImgs*(maxImgBytes*4/3+1024)+16384); err != nil {
		fail(w, 413, "Пост слишком большой")
		return
	}
	text := cleanText(in.Text, maxPostRunes)
	if text == "" && len(in.Imgs) == 0 {
		fail(w, 400, "Пост пустой")
		return
	}
	if bw := badWord(text); bw != "" {
		fail(w, 400, "В тексте запрещённое слово: «"+bw+"». Правила запрещают порнографию, нацизм и экстремизм")
		return
	}
	if len(in.Imgs) > maxImgs {
		fail(w, 400, "Не больше 4 картинок в посте")
		return
	}
	if !lim.allow("post:"+me.ID, 10, 10*time.Minute) {
		fail(w, 429, "Слишком много постов. Подождите немного")
		return
	}
	var blobs [][]byte
	for _, s := range in.Imgs {
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil || len(b) > maxImgBytes || !bytes.HasPrefix(b, []byte{0xff, 0xd8, 0xff}) {
			fail(w, 400, "Картинка повреждена или слишком большая")
			return
		}
		blobs = append(blobs, b)
	}
	dir := filepath.Join(st.dir, "img")
	os.MkdirAll(dir, 0700)
	var ids []string
	for _, b := range blobs {
		id := randHex(12)
		if os.WriteFile(filepath.Join(dir, id+".jpg"), b, 0600) != nil {
			fail(w, 500, "Не удалось сохранить картинку")
			return
		}
		ids = append(ids, id)
	}
	st.mu.Lock()
	ts := time.Now().UnixMilli()
	if n := len(st.posts); n > 0 && st.posts[n-1].TS >= ts {
		ts = st.posts[n-1].TS + 1
	}
	p := &Post{ID: randHex(12), Author: me.ID, TS: ts, Text: text, Imgs: ids, Tags: extractTags(text)}
	p.Pending = len(ids) > 0 && getSettings().Premod && !admins[me.ID]
	st.applyFeed(feedRec{Post: p})
	st.feedLog(feedRec{Post: p})
	v := postView(p, me.ID)
	st.mu.Unlock()
	if p.Pending {
		notifyAdmins()
	} else {
		hub.all("post", map[string]string{"id": p.ID, "author": me.ID})
	}
	ok(w, v)
}

// POST /api/like {post, on}
func hLike(w http.ResponseWriter, r *http.Request, me *User) {
	var in struct {
		Post string
		On   bool
	}
	if readBody(r, &in, 512) != nil {
		fail(w, 400, "Неверные данные")
		return
	}
	if !lim.allow("like:"+me.ID, 120, time.Minute) {
		fail(w, 429, "Слишком часто")
		return
	}
	st.mu.Lock()
	p := st.postBy[in.Post]
	if p == nil {
		st.mu.Unlock()
		fail(w, 404, "Пост удалён")
		return
	}
	if p.likes[me.ID] != in.On {
		rec := feedRec{Like: p.ID, By: me.ID, On: in.On}
		st.applyFeed(rec)
		st.feedLog(rec)
	}
	n := len(p.likes)
	st.mu.Unlock()
	hub.all("pupd", map[string]any{"id": p.ID, "likes": n, "by": me.ID, "on": in.On})
	ok(w, map[string]any{"likes": n, "liked": in.On})
}

// GET /api/comments?post=
func hComments(w http.ResponseWriter, r *http.Request, me *User) {
	id := r.URL.Query().Get("post")
	st.mu.RLock()
	defer st.mu.RUnlock()
	p := st.postBy[id]
	if p == nil || !canSeePost(p, me) {
		fail(w, 404, "Пост удалён")
		return
	}
	out := []map[string]any{}
	for _, c := range st.cmts[id] {
		if !me.Blocked[c.Author] && canSeeCmt(c, me) {
			out = append(out, cmtView(c))
		}
	}
	ok(w, map[string]any{"post": postView(p, me.ID), "cmts": out})
}

// POST /api/comment {post, text}
func hComment(w http.ResponseWriter, r *http.Request, me *User) {
	var in struct{ Post, Text string }
	if readBody(r, &in, 16384) != nil {
		fail(w, 400, "Неверные данные")
		return
	}
	text := cleanText(in.Text, maxCmtRunes)
	if text == "" {
		fail(w, 400, "Комментарий пустой")
		return
	}
	if bw := badWord(text); bw != "" {
		fail(w, 400, "В комментарии запрещённое слово: «"+bw+"»")
		return
	}
	if !lim.allow("cmt:"+me.ID, 20, time.Minute) {
		fail(w, 429, "Слишком много комментариев. Подождите минуту")
		return
	}
	st.mu.Lock()
	p := st.postBy[in.Post]
	if p == nil || !canSeePost(p, me) {
		st.mu.Unlock()
		fail(w, 404, "Пост удалён")
		return
	}
	ts := time.Now().UnixMilli()
	if l := st.cmts[p.ID]; len(l) > 0 && l[len(l)-1].TS >= ts {
		ts = l[len(l)-1].TS + 1
	}
	c := &Cmt{ID: randHex(12), Post: p.ID, Author: me.ID, TS: ts, Text: text}
	st.applyFeed(feedRec{Cmt: c})
	st.feedLog(feedRec{Cmt: c})
	v, n := cmtView(c), p.nc
	st.mu.Unlock()
	hub.all("cmt", map[string]any{"post": p.ID, "cmt": v, "n": n})
	ok(w, v)
}

// POST /api/delpost {id}
func hDelPost(w http.ResponseWriter, r *http.Request, me *User) {
	var in struct{ ID string }
	if readBody(r, &in, 512) != nil {
		fail(w, 400, "Неверные данные")
		return
	}
	st.mu.Lock()
	p := st.postBy[in.ID]
	if p == nil || (p.Author != me.ID && !admins[me.ID]) {
		st.mu.Unlock()
		fail(w, 404, "Пост не найден")
		return
	}
	rec := feedRec{DelPost: p.ID}
	st.applyFeed(rec)
	st.feedLog(rec)
	st.mu.Unlock()
	for _, id := range p.Imgs {
		os.Remove(filepath.Join(st.dir, "img", id+".jpg"))
	}
	hub.all("pdel", map[string]string{"id": p.ID})
	ok(w, map[string]bool{"ok": true})
}

// POST /api/delcmt {id}
func hDelCmt(w http.ResponseWriter, r *http.Request, me *User) {
	var in struct{ ID string }
	if readBody(r, &in, 512) != nil {
		fail(w, 400, "Неверные данные")
		return
	}
	st.mu.Lock()
	c := st.cmtBy[in.ID]
	var p *Post
	if c != nil {
		p = st.postBy[c.Post]
	}
	if c == nil || p == nil || (c.Author != me.ID && p.Author != me.ID && !admins[me.ID]) {
		st.mu.Unlock()
		fail(w, 404, "Комментарий не найден")
		return
	}
	rec := feedRec{DelCmt: c.ID}
	st.applyFeed(rec)
	st.feedLog(rec)
	n := p.nc
	st.mu.Unlock()
	hub.all("cdel", map[string]any{"post": p.ID, "id": c.ID, "n": n})
	ok(w, map[string]bool{"ok": true})
}

// GET /api/tags — популярные хештеги за 30 дней
func hTags(w http.ResponseWriter, r *http.Request, me *User) {
	cut := time.Now().AddDate(0, 0, -30).UnixMilli()
	cnt := map[string]int{}
	st.mu.RLock()
	for i := len(st.posts) - 1; i >= 0 && st.posts[i].TS >= cut; i-- {
		if !canSeePost(st.posts[i], me) {
			continue
		}
		for _, t := range st.posts[i].Tags {
			cnt[t]++
		}
	}
	st.mu.RUnlock()
	type tc struct {
		Tag string `json:"tag"`
		N   int    `json:"n"`
	}
	out := []tc{}
	for t, n := range cnt {
		out = append(out, tc{t, n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].N != out[j].N {
			return out[i].N > out[j].N
		}
		return out[i].Tag < out[j].Tag
	})
	if len(out) > 15 {
		out = out[:15]
	}
	ok(w, out)
}

// GET /img/<id>.jpg
func hImg(w http.ResponseWriter, r *http.Request) {
	m := reImg.FindStringSubmatch(r.URL.Path)
	if m == nil {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "image/jpeg")
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'")
	http.ServeFile(w, r, filepath.Join(st.dir, "img", m[1]+".jpg"))
}
