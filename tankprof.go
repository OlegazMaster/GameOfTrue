// «Танки»: профили игроков — опыт, звания, кристаллы, техника M0–M3, краски, припасы; выдача администратором.
package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

var rankNames = []string{"Новобранец", "Рядовой", "Ефрейтор", "Капрал", "Мастер-капрал", "Сержант", "Штаб-сержант", "Мастер-сержант",
	"Первый сержант", "Сержант-майор", "Уорэнт-офицер 1", "Уорэнт-офицер 2", "Уорэнт-офицер 3", "Уорэнт-офицер 4", "Уорэнт-офицер 5",
	"Младший лейтенант", "Лейтенант", "Старший лейтенант", "Капитан", "Майор", "Подполковник", "Полковник", "Бригадир",
	"Генерал-майор", "Генерал-лейтенант", "Генерал", "Маршал", "Фельдмаршал", "Командор", "Генералиссимус"}

// Пороги опыта — как в «Танках Онлайн».
var rankXP = []int{0, 100, 500, 1500, 3700, 7100, 12300, 20000, 29000, 41000, 57000, 76000, 98000, 125000, 156000, 192000,
	233000, 280000, 332000, 390000, 455000, 527000, 606000, 692000, 787000, 889000, 1000000, 1122000, 1255000, 1400000}

func rankOf(xp int) int {
	r := 0
	for i, v := range rankXP {
		if xp >= v {
			r = i
		}
	}
	return r
}

// краски: цена и звание
var paintShop = [][2]int{{0, 0}, {100, 0}, {150, 1}, {150, 1}, {250, 2}, {300, 3}, {300, 3}, {400, 4}, {400, 4}, {500, 5},
	{800, 6}, {800, 6}, {1200, 8}, {1500, 10}, {2500, 12}, {4000, 15}}

// припасы: аптечка, двойная броня, двойной урон, ускорение, мина (цены — по вики ProTanki)
var supNames = []string{"Ремкомплект", "Двойная броня", "Двойной урон", "Ускорение", "Мина"}
var supPrice = []int{150, 50, 50, 50, 50}

const startCrystals = 300
const profVer = 3

type TProf struct {
	V       int         `json:"v"`
	XP      int         `json:"xp"`
	Cr      int         `json:"cr"`
	H       map[int]int `json:"h"`
	U       map[int]int `json:"u"`
	C       []int       `json:"c"`
	Eq      [3]int      `json:"eq"`
	Sup     [5]int      `json:"sup"`
	Kills   int         `json:"k"`
	Deaths  int         `json:"d"`
	Battles int         `json:"b"`
}

var tp = struct {
	sync.Mutex
	m     map[string]*TProf
	dirty bool
	path  string
}{m: map[string]*TProf{}}

func loadTanks(dir string) {
	tp.path = filepath.Join(dir, "tanks.json")
	if b, err := os.ReadFile(tp.path); err == nil {
		json.Unmarshal(b, &tp.m)
	}
	go func() {
		for range time.Tick(10 * time.Second) {
			saveTanks()
		}
	}()
}

func saveTanks() {
	tp.Lock()
	if !tp.dirty || tp.path == "" {
		tp.Unlock()
		return
	}
	b, _ := json.Marshal(tp.m)
	tp.dirty = false
	tp.Unlock()
	tmp := tp.path + ".tmp"
	if os.WriteFile(tmp, b, 0600) == nil {
		os.Rename(tmp, tp.path)
	}
}

// profOf: профиль игрока (создаётся при первом входе). Вызывать под tp.Lock.
func profOf(uid string) *TProf {
	p := tp.m[uid]
	if p == nil {
		// как в оригинале: Хантер M0 и Смоки M0
		p = &TProf{V: profVer, Cr: startCrystals, H: map[int]int{2: 0}, U: map[int]int{0: 0}, C: []int{0}, Eq: [3]int{2, 0, 0}}
		tp.m[uid] = p
		tp.dirty = true
	}
	if p.H == nil {
		p.H = map[int]int{}
	}
	if p.U == nil {
		p.U = map[int]int{}
	}
	if p.V < profVer {
		// переход с этапа 2: опыт был вчетверо меньше, цены — в разы ниже
		p.XP *= 4
		p.Cr *= 10
		p.V = profVer
		tp.dirty = true
	}
	if _, ok := p.H[2]; !ok {
		p.H[2] = 0
	}
	if _, ok := p.U[0]; !ok {
		p.U[0] = 0
	}
	if len(p.C) == 0 {
		p.C = []int{0}
	}
	return p
}

func hasPaint(p *TProf, i int) bool {
	for _, c := range p.C {
		if c == i {
			return true
		}
	}
	return false
}

func profMsg(p *TProf) map[string]any {
	r := rankOf(p.XP)
	next := -1
	if r+1 < len(rankXP) {
		next = rankXP[r+1]
	}
	return map[string]any{"t": "prof", "xp": p.XP, "cr": p.Cr, "rank": r, "cur": rankXP[r], "next": next, "h": p.H, "u": p.U, "c": p.C, "eq": p.Eq,
		"sup": p.Sup, "k": p.Kills, "d": p.Deaths, "b": p.Battles}
}

func catalog() map[string]any {
	return map[string]any{"ranks": rankNames, "rxp": rankXP, "ps": paintShop, "sn": supNames, "sp": supPrice}
}

// rankBonus: кристаллы за новое звание.
func rankBonus(r int) int { return 100 + 40*r }

// award: опыт и кристаллы; при новом звании — бонус. Возвращает новое звание или -1.
func award(uid string, xp, cr int) int {
	tp.Lock()
	defer tp.Unlock()
	p := profOf(uid)
	r0 := rankOf(p.XP)
	p.XP += xp
	p.Cr += cr
	tp.dirty = true
	if r := rankOf(p.XP); r > r0 {
		p.Cr += rankBonus(r)
		return r
	}
	return -1
}

// buy: покупка или улучшение. Возвращает текст ошибки или "", и текст об успехе.
func buy(uid, kind string, i, n int) (string, string) {
	tp.Lock()
	defer tp.Unlock()
	p := profOf(uid)
	rank := rankOf(p.XP)
	pay := func(price, needRank int) string {
		if rank < needRank {
			return "Нужно звание «" + rankNames[needRank] + "»"
		}
		if p.Cr < price {
			return "Не хватает кристаллов"
		}
		p.Cr -= price
		tp.dirty = true
		return ""
	}
	switch kind {
	case "h", "u":
		own := p.H
		var rk, pr [4]int
		name := ""
		if kind == "h" {
			if i < 0 || i >= len(hulls) {
				return "Нет такого предмета", ""
			}
			rk, pr, name = hulls[i].Rank, hulls[i].Price, hulls[i].Name
		} else {
			if i < 0 || i >= len(turrets) {
				return "Нет такого предмета", ""
			}
			own = p.U
			rk, pr, name = turrets[i].Rank, turrets[i].Price, turrets[i].Name
		}
		m, has := own[i]
		if !has {
			if e := pay(pr[0], rk[0]); e != "" {
				return e, ""
			}
			own[i] = 0
			return "", "Куплено: " + name + " M0"
		}
		if m >= 3 {
			return "Уже максимальная модификация", ""
		}
		if e := pay(pr[m+1], rk[m+1]); e != "" {
			return e, ""
		}
		own[i] = m + 1
		return "", name + " улучшен до M" + strconv.Itoa(m+1)
	case "c":
		if i < 0 || i >= len(paintShop) || hasPaint(p, i) {
			return "Нельзя купить", ""
		}
		if e := pay(paintShop[i][0], paintShop[i][1]); e != "" {
			return e, ""
		}
		p.C = append(p.C, i)
		return "", "Краска куплена"
	case "s":
		if i < 0 || i >= len(supPrice) || (n != 1 && n != 10 && n != 100) {
			return "Нельзя купить", ""
		}
		if e := pay(supPrice[i]*n, 0); e != "" {
			return e, ""
		}
		p.Sup[i] += n
		return "", supNames[i] + ": +" + strconv.Itoa(n)
	}
	return "Нельзя купить", ""
}

/* ---------- выдача администратором ---------- */

func adminTankView(uid string) map[string]any { // под tp.Lock
	p := profOf(uid)
	r := rankOf(p.XP)
	return map[string]any{"rank": r, "rankName": rankNames[r], "xp": p.XP, "cr": p.Cr, "ranks": rankNames, "sup": p.Sup}
}

// pushProf: обновить профиль у игрока, если он сейчас в бою.
func pushProf(uid string) {
	for _, k := range troomOrder {
		r := trooms[k]
		r.mu.Lock()
		for _, p := range r.players {
			if p.uid == uid {
				tp.Lock()
				nr := rankOf(profOf(uid).XP)
				tp.Unlock()
				if nr != p.rank {
					p.rank = nr
					r.all(map[string]any{"t": "ev", "k": "rank", "id": p.id, "n": p.name, "r": nr, "adm": true})
					r.all(r.scores())
				}
				p.sendProf()
			}
		}
		r.mu.Unlock()
	}
}

// GET /api/tanks/admin?id=… — профиль; POST {id, rank?, cr?, add?, all?} — изменить. Только для -admin.
func hTanksAdmin(w http.ResponseWriter, rq *http.Request, me *User) {
	var in struct {
		ID   string `json:"id"`
		Rank *int   `json:"rank"`
		Cr   *int   `json:"cr"`
		Add  *int   `json:"add"`
		All  bool   `json:"all"`
		Sup  *int   `json:"sup"`
	}
	if rq.Method == http.MethodPost {
		if json.NewDecoder(http.MaxBytesReader(w, rq.Body, 4096)).Decode(&in) != nil {
			fail(w, 400, "Неверный запрос")
			return
		}
	} else {
		in.ID = rq.URL.Query().Get("id")
	}
	st.mu.RLock()
	u := st.users[in.ID]
	st.mu.RUnlock()
	if u == nil {
		fail(w, 404, "Пользователь не найден")
		return
	}
	tp.Lock()
	p := profOf(u.ID)
	msg := ""
	if in.Rank != nil {
		r := *in.Rank
		if r < 0 || r >= len(rankXP) {
			tp.Unlock()
			fail(w, 400, "Нет такого звания")
			return
		}
		p.XP = rankXP[r]
		msg = "Звание: " + rankNames[r]
	}
	const maxCr = 2_000_000_000
	if in.Cr != nil {
		p.Cr = min(max(0, *in.Cr), maxCr)
		msg = "Кристаллов: " + strconv.Itoa(p.Cr)
	}
	if in.Add != nil {
		p.Cr = min(max(0, p.Cr+*in.Add), maxCr)
		msg = "Кристаллов: " + strconv.Itoa(p.Cr)
	}
	if in.Sup != nil {
		n := min(max(0, *in.Sup), 100000)
		for i := range p.Sup {
			p.Sup[i] = n
		}
		msg = "Припасов каждого вида: " + strconv.Itoa(n)
	}
	if in.All {
		for i := range hulls {
			p.H[i] = 3
		}
		for i := range turrets {
			p.U[i] = 3
		}
		p.C = p.C[:0]
		for i := 0; i < nPaints; i++ {
			p.C = append(p.C, i)
		}
		msg = "Выдана вся техника M3 и все краски"
	}
	if rq.Method == http.MethodPost {
		tp.dirty = true
	}
	v := adminTankView(u.ID)
	tp.Unlock()
	if rq.Method == http.MethodPost {
		go pushProf(u.ID)
		saveTanks()
	}
	v["msg"] = msg
	v["name"] = u.Name
	ok(w, v)
}
