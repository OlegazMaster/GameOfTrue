// Никнеймы: уникальные (без учёта регистра), по ним можно найти человека.
package main

import (
	"net/http"
	"regexp"
	"strings"
	"time"
)

var (
	reNick   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{3,31}$`)
	reserved = map[string]bool{"admin": true, "administrator": true, "support": true, "moderator": true, "system": true, "root": true, "help": true, "official": true}
)

func normNick(s string) string {
	return strings.TrimPrefix(strings.TrimSpace(s), "@")
}

// nickProblem возвращает текст ошибки или "" если ник можно занять пользователю me.
func nickProblem(nick, me string) string { // под s.mu (чтение)
	if !reNick.MatchString(nick) {
		return "Никнейм: 4–32 символа, латинские буквы, цифры и _, начинается с буквы"
	}
	low := strings.ToLower(nick)
	if reserved[low] || badWord(nick) != "" {
		return "Этот никнейм зарезервирован"
	}
	if id, taken := st.byNick[low]; taken && id != me {
		return "Никнейм уже занят"
	}
	return ""
}

// GET /api/nickfree?nick=
func hNickFree(w http.ResponseWriter, r *http.Request, me *User) {
	if !lim.allow("nickq:"+me.ID, 60, time.Minute) {
		fail(w, 429, "Слишком много запросов")
		return
	}
	nick := normNick(r.URL.Query().Get("nick"))
	st.mu.RLock()
	p := nickProblem(nick, me.ID)
	st.mu.RUnlock()
	ok(w, map[string]any{"free": p == "", "error": p})
}

// POST /api/nick {nick} — занять или сменить ник; пустая строка убирает ник
func hNick(w http.ResponseWriter, r *http.Request, me *User) {
	var in struct{ Nick string }
	if readBody(r, &in, 512) != nil {
		fail(w, 400, "Неверные данные")
		return
	}
	if !lim.allow("nick:"+me.ID, 10, time.Hour) {
		fail(w, 429, "Никнейм можно менять не чаще 10 раз в час")
		return
	}
	nick := normNick(in.Nick)
	st.mu.Lock()
	if nick != "" {
		if p := nickProblem(nick, me.ID); p != "" {
			st.mu.Unlock()
			fail(w, 409, p)
			return
		}
	}
	if me.Nick != "" {
		delete(st.byNick, strings.ToLower(me.Nick))
	}
	me.Nick = nick
	if nick != "" {
		st.byNick[strings.ToLower(nick)] = me.ID
	}
	st.saveUsers()
	pu := publicUser(me)
	st.mu.Unlock()
	hub.all("user", pu)
	ok(w, map[string]string{"nick": nick})
}
