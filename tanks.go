// Танки: онлайн-бои на танках.
// Сервер считает попадания, урон, снаряды, флаги, точки, ящики, мины, счёт и награды; движение танка считает клиент.
package main

import (
	_ "embed"
	"encoding/json"
	"math"
	"math/rand"
	"net/http"
	"sort"
	"sync"
	"time"
)

//go:embed web/tanks.html
var tanksHTML []byte

// толчок от попадания (условные единицы)
var turImpulse = []float64{4, 0, 1, 9, 0, 6, 7, 0, 2, 8, 1.2}

/* ---------- карты и лучи ---------- */

type TSpawn struct {
	P    V3
	Yaw  float64
	Team int // 0 — только для дезматча, 1 — красные, 2 — синие
}

type TMap struct {
	theme  Theme
	boxes  []MBox
	spawns []TSpawn
	flags  [3]V3 // база флага красных [1] и синих [2]
	cps    []V3  // контрольные точки
	drops  []V3  // места падения ящиков
}

func rp(m string, r int, x0, y0, z0, x1, y1, z1 float64) MBox {
	b := bx(m, x0, y0, z0, x1, y1, z1)
	b.R = r
	return b
}

// rampPlane: верх пандуса — плоскость n·p = c, тело ниже неё.
func rampPlane(m *MBox) (V3, float64) {
	b := m.B
	ax := 0
	if m.R >= 3 {
		ax = 2
	}
	k := (b[4] - b[1]) / (b[ax+3] - b[ax])
	var n V3
	n[1] = 1
	if m.R%2 == 1 {
		n[ax] = -k
		return n, b[1] - k*b[ax]
	}
	n[ax] = k
	return n, b[1] + k*b[ax+3]
}

// rayMBN: луч против коробки или пандуса; возвращает расстояние и нормаль грани входа.
func rayMBN(m *MBox, o, d V3, maxT float64) (float64, V3, bool) {
	b := m.B
	tmin, tmax := 0.0, maxT
	var n V3
	for i := 0; i < 3; i++ {
		if math.Abs(d[i]) < 1e-9 {
			if o[i] < b[i] || o[i] > b[i+3] {
				return 0, n, false
			}
			continue
		}
		t1, t2 := (b[i]-o[i])/d[i], (b[i+3]-o[i])/d[i]
		if t1 > t2 {
			t1, t2 = t2, t1
		}
		if t1 > tmin {
			tmin = t1
			n = V3{}
			n[i] = -math.Copysign(1, d[i])
		}
		tmax = math.Min(tmax, t2)
		if tmin > tmax {
			return 0, n, false
		}
	}
	if m.R != 0 {
		pn, c := rampPlane(m)
		nd, no := pn.dot(d), pn.dot(o)
		if math.Abs(nd) < 1e-9 {
			if no > c {
				return 0, n, false
			}
		} else if tq := (c - no) / nd; nd > 0 {
			tmax = math.Min(tmax, tq)
		} else if tq > tmin {
			tmin = tq
			n = pn.norm()
		}
		if tmin > tmax {
			return 0, n, false
		}
	}
	return tmin, n, true
}

func (m *TMap) rayN(o, d V3, maxT float64) (float64, V3, bool) {
	best, hit := maxT, false
	var bn V3
	for i := range m.boxes {
		if t, n, ok := rayMBN(&m.boxes[i], o, d, best); ok && t < best {
			best, bn, hit = t, n, true
		}
	}
	return best, bn, hit
}

func (m *TMap) ray(o, d V3, maxT float64) (float64, bool) {
	t, _, ok := m.rayN(o, d, maxT)
	return t, ok
}

/* ---------- игроки и комнаты ---------- */

type tHist struct {
	t time.Time
	p V3
	y float64
}

type TPlayer struct {
	id               int
	uid, name        string
	ws               *wsConn
	out              chan []byte
	pos              V3
	yaw, tyaw        float64
	hull, tur, paint int
	hm, um           int // модификации корпуса и пушки
	team, rank       int
	hp               float64
	alive, prot      bool
	respawnAt        time.Time
	spawnedAt        time.Time
	spawnP           V3
	kills, deaths    int
	score            int
	hist             [40]tHist
	hi               int
	last             time.Time
	energy           float64
	enAt             time.Time
	burnUntil        time.Time
	burnAt           time.Time
	burnBy           int
	burnDps          float64
	frz              float64
	frzAt            time.Time
	scopeAt          time.Time
	beamT            int
	fireUntil        time.Time
	healAcc          float64
	spinAt           time.Time // Вулкан: начало раскрутки
	heat             float64   // Вулкан: секунды непрерывной стрельбы
	heatAt           time.Time
	mag              int // Молот: выстрелов в обойме
	magAt            time.Time
	armUntil         time.Time // двойная броня
	ddUntil          time.Time // двойной урон
	nitroUntil       time.Time // ускорение
	supCD            [5]time.Time
}

type TProj struct {
	id, owner, tur, m int
	pos, vel          V3
	born              time.Time
	bounces           int
}

type TRoom struct {
	key, mode string
	m         *TMap
	mu        sync.Mutex
	players   map[int]*TPlayer
	projs     []*TProj
	nextID    int
	mapJSON   json.RawMessage
	endAt     time.Time
	over      bool
	restartAt time.Time
	teamK     [3]int
	teamPts   [3]float64
	fund      int
	// режимы и бонусы
	flags   [3]*TFlag
	cps     []*TCP
	boxes   []*TBox
	mines   []*TMine
	nextBox time.Time
	goldAt  time.Time
	goldChk time.Time
}

var troomOrder []string
var trooms = map[string]*TRoom{}
var tmodes = []string{"dm", "tdm", "ctf", "cp"}

func init() {
	maps := tankMaps()
	for _, mk := range tmapOrder {
		m := maps[mk]
		m.zones()
		js, _ := json.Marshal(map[string]any{"boxes": m.boxes, "theme": m.theme, "flags": m.flags, "cps": m.cps})
		for _, mode := range tmodes {
			k := mk + "-" + mode
			r := &TRoom{key: k, mode: mode, m: m, players: map[int]*TPlayer{}, mapJSON: js}
			r.resetObjectives()
			troomOrder = append(troomOrder, k)
			trooms[k] = r
			go r.loop()
		}
	}
}

func (r *TRoom) teams() bool { return r.mode != "dm" }

func (r *TRoom) limits() (int, time.Duration) {
	switch r.mode {
	case "tdm":
		return 60, 12 * time.Minute
	case "ctf":
		return 7, 15 * time.Minute
	case "cp":
		return 100, 15 * time.Minute
	}
	return 20, 10 * time.Minute
}

func (p *TPlayer) send(v any) {
	b, _ := json.Marshal(v)
	select {
	case p.out <- b:
	default:
	}
}

func (r *TRoom) all(v any) {
	b, _ := json.Marshal(v)
	for _, p := range r.players {
		select {
		case p.out <- b:
		default:
		}
	}
}

func (p *TPlayer) H() *Hull   { return &hulls[p.hull] }
func (p *TPlayer) T() *Turret { return &turrets[p.tur] }

func (p *TPlayer) maxHP() float64 { return p.H().HP[p.hm] }
func (p *TPlayer) mass() float64  { return p.H().Mass[p.hm] / 1700 }
func (p *TPlayer) rate() time.Duration {
	return time.Duration(p.T().Rate[p.um]) * time.Millisecond
}

func secsLeft(t, now time.Time) float64 {
	if t.After(now) {
		return r2(t.Sub(now).Seconds())
	}
	return 0
}

func (p *TPlayer) meMsg() map[string]any {
	now := time.Now()
	cd := make([]float64, 5)
	for i := range cd {
		cd[i] = secsLeft(p.supCD[i], now)
	}
	return map[string]any{"t": "me", "hp": r2(p.hp), "mhp": r2(p.maxHP()), "alive": p.alive, "k": p.kills, "d": p.deaths, "s": p.score,
		"h": p.hull, "u": p.tur, "c": p.paint, "hm": p.hm, "um": p.um, "tm": p.team,
		"fx": []float64{secsLeft(p.armUntil, now), secsLeft(p.ddUntil, now), secsLeft(p.nitroUntil, now)}, "cd": cd, "mag": p.mag,
		"magw": secsLeft(p.magAt, now)}
}

func (p *TPlayer) sendProf() {
	tp.Lock()
	m := profMsg(profOf(p.uid))
	tp.Unlock()
	p.send(m)
}

func (r *TRoom) scores() map[string]any {
	l := []map[string]any{}
	for _, p := range r.players {
		l = append(l, map[string]any{"id": p.id, "n": p.name, "k": p.kills, "d": p.deaths, "s": p.score, "tm": p.team, "r": p.rank})
	}
	left := 0.0
	if !r.endAt.IsZero() {
		left = math.Max(0, time.Until(r.endAt).Seconds())
	}
	lim, _ := r.limits()
	return map[string]any{"t": "sc", "l": l, "tk": r.teamK, "left": math.Round(left), "lim": lim, "over": r.over, "fund": r.fund}
}

func (p *TPlayer) histAt(t time.Time) (V3, float64) {
	var newer *tHist
	bp, by := p.pos, p.yaw
	for i := 0; i < len(p.hist); i++ {
		h := &p.hist[(p.hi-1-i+len(p.hist)*2)%len(p.hist)]
		if h.t.IsZero() {
			break
		}
		if !h.t.After(t) {
			if newer == nil {
				return h.p, h.y
			}
			span := newer.t.Sub(h.t).Seconds()
			if span <= 0 {
				return h.p, h.y
			}
			f := t.Sub(h.t).Seconds() / span
			dy := math.Remainder(newer.y-h.y, 2*math.Pi)
			return h.p.add(newer.p.sub(h.p).mul(f)), h.y + dy*f
		}
		newer = h
		bp, by = h.p, h.y
	}
	return bp, by
}

// tankHit: луч против повёрнутой коробки танка.
func tankHit(pos V3, yaw float64, h *Hull, o, d V3, maxT float64) (float64, bool) {
	c, s := math.Cos(yaw), math.Sin(yaw)
	loc := func(v V3) V3 { return V3{v[0]*c - v[2]*s, v[1], v[0]*s + v[2]*c} }
	lo, ld := loc(o.sub(pos)), loc(d)
	t, _, ok := rayBox(lo, ld, [6]float64{-h.HW, 0, -h.HL, h.HW, h.HH + .5, h.HL}, maxT)
	return t, ok
}

func (r *TRoom) enemy(a, b *TPlayer) bool {
	return a != b && (!r.teams() || a.team != b.team)
}

func (p *TPlayer) center() V3 { return p.pos.add(V3{0, p.H().HH * .6, 0}) }

/* ---------- урон и награды ---------- */

func (r *TRoom) damage(t, by *TPlayer, dmg float64, wname string, from V3, crit bool) {
	if !t.alive || dmg <= 0 || r.over {
		return
	}
	if by != nil && by != t && (t.prot || !r.enemy(by, t)) {
		return
	}
	now := time.Now()
	if by != nil && now.Before(by.ddUntil) {
		dmg *= 2
	}
	if now.Before(t.armUntil) {
		dmg /= 2
	}
	t.hp -= dmg
	t.send(map[string]any{"t": "ev", "k": "hurt", "d": r2(dmg), "f": from.r()})
	if by != nil && by != t {
		by.send(map[string]any{"t": "ev", "k": "hm", "d": r2(dmg), "id": t.id, "c": crit})
	}
	if t.hp <= 0 {
		r.kill(t, by, wname)
	}
	t.send(t.meMsg())
}

func (r *TRoom) rankUp(p *TPlayer, nr int) {
	if nr < 0 {
		return
	}
	p.rank = nr
	r.all(map[string]any{"t": "ev", "k": "rank", "id": p.id, "n": p.name, "r": nr})
}

// give: очки боя (они же опыт) и кристаллы игроку.
func (r *TRoom) give(p *TPlayer, score, cr int) {
	p.score += score
	r.rankUp(p, award(p.uid, score, cr))
	p.send(p.meMsg())
	p.sendProf()
}

func (r *TRoom) kill(t, by *TPlayer, wname string) {
	t.hp, t.alive = 0, false
	t.deaths++
	t.respawnAt = time.Now().Add(3500 * time.Millisecond)
	t.burnUntil, t.frz, t.scopeAt, t.spinAt, t.heat = time.Time{}, 0, time.Time{}, time.Time{}, 0
	t.armUntil, t.ddUntil, t.nitroUntil = time.Time{}, time.Time{}, time.Time{}
	r.dropFlag(t, wname == "Падение")
	r.clearMines(t.id)
	tp.Lock()
	profOf(t.uid).Deaths++
	tp.dirty = true
	tp.Unlock()
	killer := ""
	if by != nil && by != t {
		by.kills++
		if r.mode == "dm" || r.mode == "tdm" {
			r.teamK[by.team]++
		}
		r.fund += 8
		killer = by.name
		tp.Lock()
		profOf(by.uid).Kills++
		tp.Unlock()
		r.give(by, 10, 3)
	}
	r.all(map[string]any{"t": "ev", "k": "kill", "a": killer, "v": t.name, "w": wname, "vid": t.id, "aid": idOf(by), "p": t.pos.r()})
	r.all(r.scores())
	lim, _ := r.limits()
	if by != nil && by != t && !r.over {
		if (r.mode == "tdm" && r.teamK[by.team] >= lim) || (r.mode == "dm" && by.kills >= lim) {
			r.finish()
		}
	}
}

func idOf(p *TPlayer) int {
	if p == nil {
		return 0
	}
	return p.id
}

func (r *TRoom) heal(t, by *TPlayer, amt float64) {
	mh := t.maxHP()
	if !t.alive || t.hp >= mh {
		return
	}
	amt = math.Min(amt, mh-t.hp)
	t.hp += amt
	if by != nil && by != t {
		by.healAcc += amt
		for by.healAcc >= 10 {
			by.healAcc -= 10
			r.fund++
			r.give(by, 1, 0)
		}
		by.send(map[string]any{"t": "ev", "k": "hl", "d": r2(amt), "id": t.id})
	}
	t.send(t.meMsg())
}

// finish: конец битвы — фонд кристаллов делится по очкам (в командах победителям вес ×1.5).
func (r *TRoom) finish() {
	r.over = true
	r.restartAt = time.Now().Add(12 * time.Second)
	win := 0
	if r.teamK[1] > r.teamK[2] {
		win = 1
	} else if r.teamK[2] > r.teamK[1] {
		win = 2
	}
	l := []*TPlayer{}
	tot := 0.0
	w := map[*TPlayer]float64{}
	for _, p := range r.players {
		l = append(l, p)
		k := float64(p.score)
		if r.teams() && win != 0 && p.team == win {
			k *= 1.5
		}
		w[p] = k
		tot += k
	}
	sort.Slice(l, func(i, j int) bool { return l[i].score > l[j].score })
	res := []map[string]any{}
	for _, p := range l {
		cr := 0
		if tot > 0 {
			cr = int(math.Round(float64(r.fund) * w[p] / tot))
		}
		tp.Lock()
		pr := profOf(p.uid)
		pr.Cr += cr
		pr.Battles++
		tp.dirty = true
		tp.Unlock()
		p.sendProf()
		res = append(res, map[string]any{"n": p.name, "k": p.kills, "d": p.deaths, "s": p.score, "tm": p.team, "id": p.id, "cr": cr, "r": p.rank})
	}
	r.all(map[string]any{"t": "end", "l": res, "win": win, "tk": r.teamK, "in": 12, "fund": r.fund})
	r.fund = 0
	r.mines = nil
}

func (r *TRoom) restart() {
	r.over = false
	r.teamK = [3]int{}
	r.teamPts = [3]float64{}
	r.projs = nil
	r.resetObjectives()
	_, dur := r.limits()
	r.endAt = time.Now().Add(dur)
	for _, p := range r.players {
		p.kills, p.deaths, p.score, p.healAcc = 0, 0, 0, 0
		r.respawn(p)
	}
	r.all(map[string]any{"t": "ev", "k": "start"})
	r.all(r.scores())
}

/* ---------- стрельба ---------- */

// useEnergy: расход энергии (ёмкость 1000). Восполняется, пока пушка не стреляет (у Рикошета — всегда).
func (r *TRoom) useEnergy(p *TPlayer, cost float64, now time.Time) bool {
	tu := p.T()
	gap := now.Sub(p.enAt).Seconds()
	if tu.Kind == "proj" {
		p.energy = math.Min(1000, p.energy+tu.Regen*gap)
	} else if gap > .3 {
		p.energy = math.Min(1000, p.energy+tu.Regen*(gap-.3))
	}
	p.enAt = now
	if p.energy < cost*.5 {
		return false
	}
	p.energy = math.Max(0, p.energy-cost)
	return true
}

// cone: все враги в конусе перед танком (огнемёт, фриз).
func (r *TRoom) cone(p *TPlayer, rng, cone float64, o, d V3, past time.Time, fn func(t *TPlayer, L float64)) {
	for _, t := range r.players {
		if !t.alive || !r.enemy(p, t) {
			continue
		}
		tq, _ := t.histAt(past)
		c := tq.add(V3{0, t.H().HH * .6, 0})
		v := c.sub(o)
		L := v.length()
		if L > rng+t.H().HL || L < .01 {
			continue
		}
		hv := V3{v[0], 0, v[2]}
		ang := math.Acos(math.Max(-1, math.Min(1, hv.norm().dot(V3{d[0], 0, d[2]}.norm()))))
		if ang > cone+math.Atan(t.H().HW/math.Max(L, 1)) {
			continue
		}
		if math.Abs(v[1]) > L*.8+2 {
			continue
		}
		if _, blocked := r.m.ray(o, v.norm(), L-1); blocked {
			continue
		}
		fn(t, L)
	}
}

func (r *TRoom) fire(p *TPlayer, o, d V3, lag float64) {
	if !p.alive || r.over {
		return
	}
	tu := p.T()
	m := p.um
	h := p.H()
	now := time.Now()
	gapOK := now.Sub(p.last) >= time.Duration(float64(p.rate())*0.8)
	if tu.Kind == "vulcan" {
		gapOK = now.Sub(p.last) >= 80*time.Millisecond && !p.spinAt.IsZero() && now.Sub(p.spinAt) >= time.Duration(tu.Spin[m]*.8)*time.Millisecond
	}
	if !gapOK {
		return
	}
	if tu.Mag > 0 && p.mag <= 0 { // Молот: обойма из трёх выстрелов
		if now.Before(p.magAt.Add(-300 * time.Millisecond)) {
			return
		}
		p.mag = tu.Mag
	}
	switch tu.Kind {
	case "flame", "freeze":
		if !r.useEnergy(p, tu.Cost[m]*.1, now) {
			return
		}
	case "proj":
		if tu.Cost[m] > 0 && !r.useEnergy(p, tu.Cost[m], now) {
			return
		}
	}
	p.last = now
	p.prot = false
	if tu.Mag > 0 {
		p.mag--
		if p.mag <= 0 {
			p.magAt = now.Add(time.Duration(tu.MagR[m]) * time.Millisecond)
		}
		p.send(p.meMsg())
	}
	piv := p.pos.add(V3{0, h.HH + .3, 0})
	if o.sub(piv).length() > 4.5 {
		o = piv
	}
	d = d.norm()
	// направление — по башне; по вертикали — автонаведение (до ±43°)
	dy := math.Atan2(-d[0], -d[2])
	if math.Abs(math.Remainder(dy-p.tyaw, 2*math.Pi)) > .25 {
		dy = p.tyaw
	}
	pitch := math.Max(-.75, math.Min(.75, math.Asin(math.Max(-1, math.Min(1, d[1])))))
	d = V3{-math.Sin(dy) * math.Cos(pitch), math.Sin(pitch), -math.Cos(dy) * math.Cos(pitch)}
	lag = math.Max(0, math.Min(350, lag))
	past := now.Add(-time.Duration(lag) * time.Millisecond)

	switch tu.Kind {
	case "shot", "rail", "shaft", "vulcan":
		type hitT struct {
			t  *TPlayer
			at float64
		}
		scope := -1.0
		if tu.Kind == "shaft" && !p.scopeAt.IsZero() && now.Sub(p.scopeAt) > 400*time.Millisecond {
			scope = math.Max(0, math.Min(1, (float64(now.Sub(p.scopeAt).Milliseconds())-500)/tu.Charge[m]))
		}
		p.scopeAt = time.Time{}
		pellets := max(1, tu.Pel)
		dmgBy := map[*TPlayer]float64{}
		crits := map[*TPlayer]bool{}
		ends := [][3]float64{}
		hitIDs := []int{}
		var impact V3
		var direct *TPlayer
		rng := tu.reach(m)
		for pi := 0; pi < pellets; pi++ {
			dir := d
			if tu.Spread[0] > 0 {
				dir = spreadHV(d, tu.Spread[0], tu.Spread[1])
			}
			wt, _ := r.m.ray(o, dir, rng)
			hits := []hitT{}
			for _, t := range r.players {
				if t == p || !t.alive {
					continue
				}
				tq, ty := t.histAt(past)
				if at, ok := tankHit(tq, ty, t.H(), o, dir, wt); ok {
					hits = append(hits, hitT{t, at})
				}
			}
			sort.Slice(hits, func(i, j int) bool { return hits[i].at < hits[j].at })
			end := wt
			if tu.Kind != "rail" && len(hits) > 0 {
				hits = hits[:1]
				end = hits[0].at
			}
			for i, ht := range hits {
				lo, hi := tu.Dmg[m][0], tu.Dmg[m][1]
				dmg := lo + rand.Float64()*(hi-lo)
				switch {
				case tu.Kind == "vulcan":
					dmg = lo * .1 * tu.falloff(m, ht.at)
				case tu.Kind == "rail" && i > 0:
					dmg *= tu.Pierce[m]
				case tu.Kind == "shaft" && scope >= 0:
					s0, s1 := tu.Snipe[m][0], tu.Snipe[m][1]
					dmg = s0 + (s1-s0)*scope
				default:
					dmg *= tu.falloff(m, ht.at)
				}
				if tu.Crit[m] > 0 && rand.Float64() < tu.Crit[m] {
					dmg = tu.CritD[m] // крит у Смоки не ослабевает с расстоянием
					crits[ht.t] = true
				}
				dmgBy[ht.t] += dmg
				direct = ht.t
			}
			e := o.add(dir.mul(end))
			impact = e
			ends = append(ends, [3]float64(e.r()))
		}
		for t, dmg := range dmgBy {
			if r.enemy(p, t) && !t.prot {
				imp := turImpulse[p.tur]
				if tu.Kind == "shaft" && scope >= 0 {
					imp *= .4 + .6*scope
				}
				mk := t.mass()
				t.send(map[string]any{"t": "ev", "k": "kb", "v": V3{d[0] * imp / mk, 0, d[2] * imp / mk}.r()})
			}
			hitIDs = append(hitIDs, t.id)
			r.damage(t, p, dmg, tu.Name, o, crits[t])
		}
		if tu.Kind == "vulcan" {
			p.fireUntil = now.Add(220 * time.Millisecond)
			if now.Sub(p.heatAt) < 400*time.Millisecond {
				p.heat += now.Sub(p.heatAt).Seconds()
			}
			p.heatAt = now
			if p.heat > tu.Heat[m] { // перегрев: Вулкан поджигает сам себя
				p.burnUntil, p.burnBy, p.burnDps = now.Add(time.Second), p.id, 12
			}
			r.all(map[string]any{"t": "ev", "k": "vul", "id": p.id, "e": ends[0], "h": len(hitIDs) > 0})
			return
		}
		ev := map[string]any{"t": "ev", "k": "shot", "id": p.id, "w": p.tur, "o": o.r(), "e": ends, "h": hitIDs}
		if tu.Splash > 0 { // Тандер: взрыв задевает всех рядом, в том числе стрелка
			ev["sp"] = tu.Splash
			base := (tu.Dmg[m][0] + tu.Dmg[m][1]) / 2 * tu.falloff(m, impact.sub(o).length())
			for _, t := range r.players {
				if t == direct || !t.alive || (t != p && !r.enemy(p, t)) {
					continue
				}
				v := t.center().sub(impact)
				dist := math.Max(0, v.length()-t.H().HW)
				if dist >= tu.Splash {
					continue
				}
				if dist > 1 {
					if _, blocked := r.m.ray(impact.sub(v.norm().mul(.3)), v.norm(), v.length()-.5); blocked {
						continue
					}
				}
				k := 1 - (1-tu.SplW)*dist/tu.Splash
				r.damage(t, p, base*k, tu.Name, impact, false)
			}
		}
		if tu.Kind == "shaft" {
			ev["sc"] = r2(math.Max(0, scope))
		}
		r.all(ev)
	case "proj":
		r.nextID++
		pr := &TProj{id: r.nextID, owner: p.id, tur: p.tur, m: m, pos: o, vel: d.mul(tu.Speed[m]), born: now, bounces: tu.Bounce}
		// догоняем задержку сети: снаряд сразу проходит путь, который прошёл бы за время пинга
		r.moveProj(pr, math.Min(lag, 150)/1000, now)
		if pr.id != 0 {
			r.projs = append(r.projs, pr)
		}
		r.all(map[string]any{"t": "ev", "k": "shot", "id": p.id, "w": p.tur, "o": o.r(), "e": [][3]float64{}})
	case "flame":
		p.fireUntil = now.Add(220 * time.Millisecond)
		r.cone(p, tu.RMin[m], tu.Cone, o, d, past, func(t *TPlayer, L float64) {
			r.damage(t, p, tu.Dmg[m][0]*.1*tu.falloff(m, L), tu.Name, o, false)
			if t.alive && !t.prot {
				t.burnUntil, t.burnBy, t.burnDps, t.frz = now.Add(3*time.Second), p.id, tu.Burn[m], 0
			}
		})
	case "freeze":
		p.fireUntil = now.Add(220 * time.Millisecond)
		r.cone(p, tu.RMin[m], tu.Cone, o, d, past, func(t *TPlayer, L float64) {
			r.damage(t, p, tu.Dmg[m][0]*.1*tu.falloff(m, L), tu.Name, o, false)
			if t.alive && !t.prot {
				t.frz, t.frzAt, t.burnUntil = math.Min(1, t.frz+.06), now, time.Time{}
			}
		})
	case "beam":
		p.fireUntil = now.Add(220 * time.Millisecond)
		rng := tu.RMin[m]
		valid := func(t *TPlayer, cone float64) (float64, bool) {
			if t == nil || t == p || !t.alive {
				return 0, false
			}
			tq, _ := t.histAt(past)
			v := tq.add(V3{0, t.H().HH * .6, 0}).sub(o)
			L := v.length()
			if L > rng+t.H().HL {
				return 0, false
			}
			hv := V3{v[0], 0, v[2]}.norm()
			ang := math.Acos(math.Max(-1, math.Min(1, hv.dot(V3{d[0], 0, d[2]}.norm()))))
			if ang > cone+math.Atan(t.H().HW/math.Max(L, 1)) {
				return 0, false
			}
			if _, blocked := r.m.ray(o, v.norm(), L-1); blocked {
				return 0, false
			}
			return ang, true
		}
		var tgt *TPlayer
		if cur := r.players[p.beamT]; cur != nil {
			if _, ok := valid(cur, tu.Cone*1.8); ok {
				tgt = cur
			}
		}
		if tgt == nil {
			best := 1e9
			for _, t := range r.players {
				if a, ok := valid(t, tu.Cone); ok {
					if r.enemy(p, t) {
						a -= 1 // враг важнее союзника
					}
					if a < best {
						best, tgt = a, t
					}
				}
			}
		}
		p.beamT = idOf(tgt)
		cost := 83.3 // без цели и при лечении
		if tgt != nil && r.enemy(p, tgt) {
			cost = tu.Cost[m]
		}
		if !r.useEnergy(p, cost*.1, now) {
			p.beamT = 0
			return
		}
		if tgt != nil {
			if r.enemy(p, tgt) {
				if !tgt.prot {
					r.damage(tgt, p, tu.Dmg[m][0]*.1, tu.Name, o, false)
					r.heal(p, nil, tu.Dmg[m][0]*.1*.3)
				}
			} else {
				r.heal(tgt, p, tu.Heal[m]*.1)
			}
		}
	}
}

// spreadHV: случайное отклонение направления по горизонтали и вертикали (Молот).
func spreadHV(d V3, sh, sv float64) V3 {
	yaw := math.Atan2(-d[0], -d[2]) + (rand.Float64()*2-1)*sh
	pit := math.Asin(math.Max(-1, math.Min(1, d[1]))) + (rand.Float64()*2-1)*sv
	return V3{-math.Sin(yaw) * math.Cos(pit), math.Sin(pit), -math.Cos(yaw) * math.Cos(pit)}
}

// moveProj: сдвиг снаряда на dt с отскоками и попаданиями. Уничтоженный снаряд получает id 0.
func (r *TRoom) moveProj(pr *TProj, dt float64, now time.Time) {
	tu := &turrets[pr.tur]
	left := pr.vel.length() * dt
	for left > 1e-6 && pr.id != 0 {
		dir := pr.vel.norm()
		seg := math.Min(left, 2)
		wt, wn, wok := r.m.rayN(pr.pos, dir, seg)
		var hit *TPlayer
		ht := seg
		for _, t := range r.players {
			if !t.alive || (t.id == pr.owner && pr.bounces == tu.Bounce) {
				continue
			}
			if at, ok := tankHit(t.pos, t.yaw, t.H(), pr.pos, dir, ht); ok && at < ht {
				ht, hit = at, t
			}
		}
		switch {
		case hit != nil && (!wok || ht <= wt):
			at := pr.pos.add(dir.mul(ht))
			owner := r.players[pr.owner]
			if owner != nil {
				if r.enemy(owner, hit) && !hit.prot {
					mk := hit.mass()
					hit.send(map[string]any{"t": "ev", "k": "kb", "v": V3{dir[0] * turImpulse[pr.tur] / mk, 0, dir[2] * turImpulse[pr.tur] / mk}.r()})
				}
				lo, hi := tu.Dmg[pr.m][0], tu.Dmg[pr.m][1]
				dmg := (lo + rand.Float64()*(hi-lo)) * tu.falloff(pr.m, now.Sub(pr.born).Seconds()*tu.Speed[pr.m])
				r.damage(hit, owner, dmg, tu.Name, pr.pos, false)
			}
			r.all(map[string]any{"t": "ev", "k": "pimp", "p": at.r(), "w": pr.tur, "hit": true})
			pr.id = 0
		case wok:
			at := pr.pos.add(dir.mul(math.Max(0, wt-.05)))
			if pr.bounces > 0 && wn.dot(wn) > .5 {
				pr.bounces--
				pr.pos = at
				pr.vel = pr.vel.sub(wn.mul(2 * pr.vel.dot(wn)))
				left -= wt
				r.all(map[string]any{"t": "ev", "k": "bnc", "p": at.r()})
				continue
			}
			r.all(map[string]any{"t": "ev", "k": "pimp", "p": at.r(), "w": pr.tur})
			pr.id = 0
		default:
			pr.pos = pr.pos.add(dir.mul(seg))
			left -= seg
		}
	}
	if pr.id != 0 && now.Sub(pr.born).Seconds()*tu.Speed[pr.m] > tu.reach(pr.m) {
		pr.id = 0
	}
}

/* ---------- возрождение и цикл ---------- */

func (r *TRoom) respawn(p *TPlayer) {
	tp.Lock()
	pf := profOf(p.uid)
	eq := pf.Eq
	if _, ok := pf.H[eq[0]]; !ok || eq[0] >= len(hulls) {
		eq[0] = 2
	}
	if _, ok := pf.U[eq[1]]; !ok || eq[1] >= len(turrets) {
		eq[1] = 0
	}
	if !hasPaint(pf, eq[2]) {
		eq[2] = 0
	}
	p.hull, p.tur, p.paint, p.hm, p.um = eq[0], eq[1], eq[2], pf.H[eq[0]], pf.U[eq[1]]
	p.rank = rankOf(pf.XP)
	tp.Unlock()
	cands := []TSpawn{}
	for _, s := range r.m.spawns {
		if (r.teams() && s.Team == p.team) || r.mode == "dm" {
			cands = append(cands, s)
		}
	}
	if len(cands) == 0 {
		cands = r.m.spawns
	}
	best, bd := cands[0], -1.0
	for _, s := range cands {
		md := 1e9
		for _, o := range r.players {
			if o != p && o.alive {
				md = math.Min(md, o.pos.sub(s.P).length())
			}
		}
		md += rand.Float64() * 6
		if md > bd {
			best, bd = s, md
		}
	}
	p.pos, p.yaw, p.tyaw, p.spawnP = best.P, best.Yaw, best.Yaw, best.P
	p.hp, p.alive, p.prot = p.maxHP(), true, true
	p.spawnedAt = time.Now()
	p.energy, p.enAt = 1000, time.Now()
	p.burnUntil, p.frz, p.scopeAt, p.spinAt, p.heat = time.Time{}, 0, time.Time{}, time.Time{}, 0
	p.mag, p.magAt = p.T().Mag, time.Time{}
	p.beamT = 0
	p.hist = [40]tHist{}
	p.send(map[string]any{"t": "ev", "k": "spawn", "p": best.P, "yaw": r2(best.Yaw)})
	p.send(p.meMsg())
}

func (r *TRoom) loop() {
	tk := time.NewTicker(time.Second / 20)
	for range tk.C {
		r.mu.Lock()
		r.step(time.Now())
		r.mu.Unlock()
	}
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func (r *TRoom) step(now time.Time) {
	if len(r.players) == 0 {
		if !r.endAt.IsZero() {
			r.endAt, r.over, r.teamK, r.teamPts, r.projs, r.fund = time.Time{}, false, [3]int{}, [3]float64{}, nil, 0
			r.resetObjectives()
		}
		return
	}
	if r.endAt.IsZero() {
		_, dur := r.limits()
		r.endAt = now.Add(dur)
	}
	if r.over {
		if now.After(r.restartAt) {
			r.restart()
		}
	} else if now.After(r.endAt) {
		r.finish()
	}
	// снаряды
	keep := r.projs[:0]
	for _, pr := range r.projs {
		r.moveProj(pr, 1.0/20, now)
		if pr.id != 0 {
			keep = append(keep, pr)
		}
	}
	r.projs = keep
	for _, p := range r.players {
		if !p.alive {
			if !p.respawnAt.IsZero() && now.After(p.respawnAt) && !r.over {
				r.respawn(p)
			}
			continue
		}
		if p.prot && now.Sub(p.spawnedAt) > 3*time.Second {
			p.prot = false
		}
		if now.Before(p.burnUntil) && now.After(p.burnAt) {
			p.burnAt = now.Add(250 * time.Millisecond)
			w := "Огнемёт"
			if p.burnBy == p.id {
				w = "Перегрев"
			}
			r.damage(p, r.players[p.burnBy], p.burnDps/4, w, p.pos, false)
		}
		if p.frz > 0 && now.Sub(p.frzAt) > 600*time.Millisecond {
			p.frz = math.Max(0, p.frz-.35/20)
		}
		if p.heat > 0 && now.Sub(p.heatAt) > 400*time.Millisecond {
			p.heat = math.Max(0, p.heat-1.0/20)
		}
		if p.alive && p.pos[1] < -20 {
			r.kill(p, nil, "Падение")
		}
	}
	if !r.over {
		r.stepModes(now)
	}
	pl := [][]float64{}
	for _, p := range r.players {
		st := 0.0
		if p.alive {
			st = 1
			if p.prot {
				st = 2
			}
		}
		fir := now.Before(p.fireUntil)
		bt := 0.0
		if fir {
			bt = float64(p.beamT)
		}
		fx := 0.0
		if now.Before(p.armUntil) {
			fx += 1
		}
		if now.Before(p.ddUntil) {
			fx += 2
		}
		if now.Before(p.nitroUntil) {
			fx += 4
		}
		if !p.spinAt.IsZero() {
			fx += 8
		}
		pl = append(pl, []float64{float64(p.id), r2(p.pos[0]), r2(p.pos[1]), r2(p.pos[2]), r2(p.yaw), r2(p.tyaw),
			float64(p.hull), float64(p.tur), float64(p.paint), st, float64(p.team),
			math.Round(100 * math.Max(0, p.hp) / p.maxHP()), b2f(fir), bt, b2f(now.Before(p.burnUntil)),
			math.Round(p.frz * 100), b2f(!p.scopeAt.IsZero()), fx, float64(p.hm), float64(p.um)})
	}
	prs := [][]float64{}
	for _, x := range r.projs {
		prs = append(prs, []float64{float64(x.id), float64(x.tur), r2(x.pos[0]), r2(x.pos[1]), r2(x.pos[2])})
	}
	msg := map[string]any{"t": "s", "pl": pl, "pr": prs}
	r.modeSnap(msg)
	r.all(msg)
}

/* ---------- HTTP ---------- */

func hTanksWS(w http.ResponseWriter, rq *http.Request) {
	u, _ := authed(rq)
	if u == nil || u.Banned {
		http.Error(w, "unauthorized", 401)
		return
	}
	ws, err := wsUpgrade(w, rq)
	if err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	r := trooms[rq.URL.Query().Get("room")]
	if r == nil {
		r = trooms[troomOrder[0]]
	}
	r.mu.Lock()
	for _, o := range r.players {
		if o.uid == u.ID {
			o.ws.c.Close()
		}
	}
	r.nextID++
	p := &TPlayer{id: r.nextID, uid: u.ID, name: u.Name, ws: ws, out: make(chan []byte, 256)}
	if r.teams() {
		n := [3]int{}
		for _, o := range r.players {
			if o.uid != u.ID {
				n[o.team]++
			}
		}
		p.team = 1
		if n[2] < n[1] || (n[2] == n[1] && r.teamK[2] < r.teamK[1]) {
			p.team = 2
		}
	}
	r.players[p.id] = p
	if r.endAt.IsZero() {
		_, dur := r.limits()
		r.endAt = time.Now().Add(dur)
	}
	tp.Lock()
	prof := profMsg(profOf(u.ID))
	tp.Unlock()
	p.send(map[string]any{"t": "hi", "id": p.id, "name": p.name, "map": r.mapJSON, "hulls": hulls, "turrets": turrets, "mode": r.mode, "room": r.key, "tm": p.team,
		"cat": catalog()})
	p.send(prof)
	if r.over {
		p.respawnAt = time.Now()
		p.send(p.meMsg())
	} else {
		r.respawn(p)
	}
	r.all(r.scores())
	r.all(map[string]any{"t": "ev", "k": "join", "n": p.name, "tm": p.team})
	r.mu.Unlock()

	done := make(chan struct{})
	go func() {
		for {
			select {
			case b := <-p.out:
				if ws.write(1, b) != nil {
					ws.c.Close()
					return
				}
			case <-done:
				return
			}
		}
	}()
	for {
		b, err := ws.read()
		if err != nil {
			break
		}
		var m struct {
			T       string
			P, O, D []float64
			Y, Ty   float64
			Lag, C  float64
			H, U    int
			Pt, I   int
			N       int
			K       string
			On      bool
		}
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		r.mu.Lock()
		switch m.T {
		case "st":
			if pos, ok := v3(m.P); ok && p.alive && !math.IsNaN(m.Y) && !math.IsNaN(m.Ty) {
				p.pos, p.yaw, p.tyaw = pos, m.Y, m.Ty
				p.hist[p.hi%len(p.hist)] = tHist{time.Now(), pos, m.Y}
				p.hi = (p.hi + 1) % len(p.hist)
				if p.prot && pos.sub(p.spawnP).length() > 1 {
					p.prot = false
				}
			}
		case "fire":
			o, ok1 := v3(m.O)
			d, ok2 := v3(m.D)
			if ok1 && ok2 {
				r.fire(p, o, d, m.Lag)
			}
		case "ch":
			if p.alive && p.T().Kind == "rail" {
				r.all(map[string]any{"t": "ev", "k": "ch", "id": p.id})
			}
		case "vs": // Вулкан: начал или закончил раскрутку
			if p.alive && p.T().Kind == "vulcan" {
				if !m.On {
					p.spinAt = time.Time{}
				} else if p.spinAt.IsZero() {
					p.spinAt = time.Now()
					p.prot = false
				}
			}
		case "sc":
			if p.alive && p.T().Kind == "shaft" && time.Since(p.last) > time.Duration(float64(p.rate())*.8) {
				p.scopeAt = time.Now()
				p.prot = false
			}
		case "scx":
			p.scopeAt = time.Time{}
		case "sup":
			r.useSupply(p, m.I)
		case "eq":
			tp.Lock()
			pf := profOf(p.uid)
			if _, ok := pf.H[m.H]; ok && m.H >= 0 && m.H < len(hulls) {
				pf.Eq[0] = m.H
			}
			if _, ok := pf.U[m.U]; ok && m.U >= 0 && m.U < len(turrets) {
				pf.Eq[1] = m.U
			}
			if hasPaint(pf, m.Pt) {
				pf.Eq[2] = m.Pt
			}
			tp.dirty = true
			pm := profMsg(pf)
			tp.Unlock()
			if p.alive && pf.Eq[0] == p.hull && pf.Eq[1] == p.tur {
				p.paint = pf.Eq[2] // краску меняем сразу
			}
			p.send(pm)
			p.send(p.meMsg())
		case "buy":
			e, okm := buy(p.uid, m.K, m.I, max(1, m.N))
			p.send(map[string]any{"t": "shop", "ok": e == "", "m": e + okm})
			p.sendProf()
		case "sd":
			if p.alive && !r.over {
				r.kill(p, nil, "Самоуничтожение")
				p.respawnAt = time.Now().Add(1500 * time.Millisecond)
			}
		case "pi":
			p.send(map[string]any{"t": "po", "c": m.C})
		}
		r.mu.Unlock()
	}
	close(done)
	ws.c.Close()
	r.mu.Lock()
	if r.players[p.id] == p {
		r.dropFlag(p, false)
		r.clearMines(p.id)
		delete(r.players, p.id)
		r.all(r.scores())
		r.all(map[string]any{"t": "ev", "k": "leave", "n": p.name})
	}
	r.mu.Unlock()
}

func hTanksInfo(w http.ResponseWriter, rq *http.Request, me *User) {
	out := []map[string]any{}
	for _, k := range troomOrder {
		r := trooms[k]
		r.mu.Lock()
		names := []string{}
		for _, p := range r.players {
			names = append(names, p.name)
		}
		r.mu.Unlock()
		out = append(out, map[string]any{"id": k, "map": r.m.theme.Name, "mode": r.mode, "players": names})
	}
	tp.Lock()
	pf := profOf(me.ID)
	rk, xp, cr := rankOf(pf.XP), pf.XP, pf.Cr
	tp.Unlock()
	ok(w, map[string]any{"rooms": out, "rank": rk, "rankName": rankNames[rk], "xp": xp, "cr": cr})
}

func hTanksPage(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", gameCSP)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-cache")
	w.Write(tanksHTML)
}
