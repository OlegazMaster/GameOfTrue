// «Танки»: режимы «Захват флага» и «Контроль точек», ящики с неба (в том числе золотой), мины и припасы.
package main

import (
	"math"
	"math/rand"
	"time"
)

type TFlag struct {
	home    V3
	pos     V3
	atHome  bool
	carrier int // id игрока, который везёт флаг
	dropAt  time.Time
}

type TCP struct {
	pos   V3
	prog  float64 // −100 — красные, +100 — синие
	owner int
}

// типы ящиков: 0 ремкомплект, 1 броня, 2 урон, 3 ускорение, 4 кристалл, 5 золотой
type TBox struct {
	id, kind int
	pos      V3
	landY    float64
	landed   bool
	until    time.Time
}

type TMine struct {
	id, owner, team int
	pos             V3
	armAt           time.Time
}

const (
	supDur      = 60 * time.Second // броня, урон, ускорение
	repairCD    = 30 * time.Second
	mineCD      = 30 * time.Second
	flagReturn  = 30 * time.Second
	cpRadius    = 7.0
	goldCrystal = 1000
	boxCrystal  = 10
)

func (r *TRoom) resetObjectives() {
	r.boxes, r.mines = nil, nil
	r.nextBox, r.goldAt = time.Time{}, time.Time{}
	r.flags = [3]*TFlag{}
	r.cps = nil
	if r.mode == "ctf" {
		for t := 1; t <= 2; t++ {
			h := r.m.flags[t]
			r.flags[t] = &TFlag{home: h, pos: h, atHome: true}
		}
	}
	if r.mode == "cp" {
		for _, c := range r.m.cps {
			r.cps = append(r.cps, &TCP{pos: c})
		}
	}
}

func near(a, b V3, rad, dy float64) bool {
	dx, dz := a[0]-b[0], a[2]-b[2]
	return dx*dx+dz*dz < rad*rad && math.Abs(a[1]-b[1]) < dy
}

/* ---------- флаги ---------- */

// dropFlag: игрок с флагом погиб или вышел — флаг остаётся на месте (при падении с карты возвращается).
func (r *TRoom) dropFlag(p *TPlayer, home bool) {
	for t := 1; t <= 2; t++ {
		f := r.flags[t]
		if f == nil || f.carrier != p.id {
			continue
		}
		f.carrier = 0
		if home || p.pos[1] < -5 {
			f.pos, f.atHome = f.home, true
			r.all(map[string]any{"t": "ev", "k": "flag", "a": "ret", "tm": t, "n": ""})
			continue
		}
		f.pos, f.dropAt = p.pos, time.Now()
		r.all(map[string]any{"t": "ev", "k": "flag", "a": "drop", "tm": t, "n": p.name})
	}
}

func (r *TRoom) stepFlags(now time.Time) {
	for t := 1; t <= 2; t++ {
		f := r.flags[t]
		if f.carrier != 0 {
			if c := r.players[f.carrier]; c != nil && c.alive {
				f.pos = c.pos
			}
		} else if !f.atHome && now.Sub(f.dropAt) > flagReturn {
			f.pos, f.atHome = f.home, true
			r.all(map[string]any{"t": "ev", "k": "flag", "a": "ret", "tm": t, "n": ""})
		}
	}
	for _, p := range r.players {
		if !p.alive || p.team == 0 {
			continue
		}
		own, ef := r.flags[p.team], r.flags[3-p.team]
		if ef.carrier == 0 && near(p.pos, ef.pos, 2.6+p.H().HW, 3) {
			ef.carrier, ef.atHome = p.id, false
			p.prot = false
			r.all(map[string]any{"t": "ev", "k": "flag", "a": "take", "tm": 3 - p.team, "n": p.name, "id": p.id})
		}
		if own.carrier == 0 && !own.atHome && near(p.pos, own.pos, 2.6+p.H().HW, 3) {
			own.pos, own.atHome = own.home, true
			r.all(map[string]any{"t": "ev", "k": "flag", "a": "ret", "tm": p.team, "n": p.name})
			r.give(p, 10, 0)
		}
		if ef.carrier == p.id && own.atHome && near(p.pos, own.home, 2.6+p.H().HW, 3) {
			ef.carrier, ef.pos, ef.atHome = 0, ef.home, true
			r.teamK[p.team]++
			r.fund += 20
			r.all(map[string]any{"t": "ev", "k": "flag", "a": "cap", "tm": 3 - p.team, "n": p.name, "id": p.id})
			r.give(p, 30, 0)
			r.all(r.scores())
			if lim, _ := r.limits(); r.teamK[p.team] >= lim {
				r.finish()
				return
			}
		}
	}
}

/* ---------- контрольные точки ---------- */

func (r *TRoom) stepCP(now time.Time) {
	const dt = 1.0 / 20
	owned := [3]int{}
	for i, c := range r.cps {
		n := [3]int{}
		for _, p := range r.players {
			if p.alive && near(p.pos, c.pos, cpRadius, 4) {
				n[p.team]++
			}
		}
		d := n[2] - n[1]
		if d != 0 {
			// чем больше танков на точке, тем быстрее захват: один танк — 10 секунд от нейтральной
			c.prog = math.Max(-100, math.Min(100, c.prog+float64(d)*10*dt))
			prev := c.owner
			switch {
			case c.prog >= 100:
				c.owner = 2
			case c.prog <= -100:
				c.owner = 1
			case (c.owner == 2 && c.prog <= 0) || (c.owner == 1 && c.prog >= 0):
				c.owner = 0
			}
			if c.owner != prev {
				r.all(map[string]any{"t": "ev", "k": "cp", "i": i, "tm": c.owner, "was": prev})
				w := 3 - prev
				if c.owner != 0 {
					w = c.owner
				}
				for _, p := range r.players {
					if p.alive && p.team == w && near(p.pos, c.pos, cpRadius, 4) {
						r.give(p, map[bool]int{true: 10, false: 5}[c.owner != 0], 0)
					}
				}
			}
		}
		owned[c.owner]++
	}
	// очки получает команда, которая держит больше точек: +1 за каждую лишнюю точку раз в 4 секунды
	if diff := owned[2] - owned[1]; diff != 0 {
		w := 2
		if diff < 0 {
			w, diff = 1, -diff
		}
		before := r.teamK[w]
		r.teamPts[w] += float64(diff) * dt / 4
		r.teamK[w] = int(r.teamPts[w])
		if r.teamK[w] != before {
			r.all(r.scores())
			if lim, _ := r.limits(); r.teamK[w] >= lim {
				r.finish()
			}
		}
	}
}

/* ---------- ящики ---------- */

func (r *TRoom) freeDrop() (V3, bool) {
	z := r.m.drops
	if len(z) == 0 {
		return V3{}, false
	}
	for try := 0; try < 12; try++ {
		c := z[rand.Intn(len(z))]
		busy := false
		for _, b := range r.boxes {
			if near(b.pos, c, 3, 50) {
				busy = true
				break
			}
		}
		if !busy {
			return c, true
		}
	}
	return V3{}, false
}

func (r *TRoom) dropBox(kind int, now time.Time) {
	c, ok := r.freeDrop()
	if !ok {
		return
	}
	r.nextID++
	b := &TBox{id: r.nextID, kind: kind, pos: c.add(V3{0, 22, 0}), landY: c[1], until: now.Add(90 * time.Second)}
	if kind == 5 {
		b.until = now.Add(30 * time.Minute)
		r.all(map[string]any{"t": "ev", "k": "gold", "a": "drop", "p": c.r()})
	}
	r.boxes = append(r.boxes, b)
}

func (r *TRoom) stepBoxes(now time.Time) {
	n := len(r.players)
	if r.nextBox.IsZero() {
		r.nextBox = now.Add(8 * time.Second)
	}
	regular := 0
	for _, b := range r.boxes {
		if b.kind != 5 {
			regular++
		}
	}
	if now.After(r.nextBox) {
		r.nextBox = now.Add(time.Duration(14+rand.Intn(14)) * time.Second)
		if regular < 3+2*n {
			k := rand.Intn(4)
			if rand.Float64() < .2 {
				k = 4
			}
			r.dropBox(k, now)
		}
	}
	// золотой ящик: раз в минуту небольшой шанс, если в бою хотя бы двое; сначала — предупреждение
	if r.goldChk.IsZero() || now.Sub(r.goldChk) > time.Minute {
		r.goldChk = now
		if n >= 2 && r.goldAt.IsZero() && rand.Float64() < .07 {
			r.goldAt = now.Add(10 * time.Second)
			r.all(map[string]any{"t": "ev", "k": "gold", "a": "soon"})
		}
	}
	if !r.goldAt.IsZero() && now.After(r.goldAt) {
		r.goldAt = time.Time{}
		r.dropBox(5, now)
	}
	keep := r.boxes[:0]
	for _, b := range r.boxes {
		if !b.landed {
			sp := 5.0
			if b.kind == 5 {
				sp = 3.5
			}
			b.pos[1] -= sp / 20
			if b.pos[1] <= b.landY {
				b.pos[1], b.landed = b.landY, true
			}
		}
		if now.After(b.until) {
			continue
		}
		taken := false
		if b.landed {
			for _, p := range r.players {
				if p.alive && near(p.pos, b.pos, 1.4+p.H().HW+p.H().HL*.4, 2.5) {
					r.pickBox(p, b, now)
					taken = true
					break
				}
			}
		}
		if !taken {
			keep = append(keep, b)
		}
	}
	r.boxes = keep
}

func (r *TRoom) pickBox(p *TPlayer, b *TBox, now time.Time) {
	switch b.kind {
	case 0:
		r.heal(p, nil, p.maxHP())
	case 1:
		p.armUntil = now.Add(supDur)
	case 2:
		p.ddUntil = now.Add(supDur)
	case 3:
		p.nitroUntil = now.Add(supDur)
	case 4:
		award(p.uid, 0, boxCrystal)
		p.sendProf()
	case 5:
		award(p.uid, 0, goldCrystal)
		p.sendProf()
		r.all(map[string]any{"t": "ev", "k": "gold", "a": "got", "n": p.name})
	}
	r.all(map[string]any{"t": "ev", "k": "box", "id": p.id, "b": b.kind, "p": b.pos.r()})
	p.send(p.meMsg())
}

/* ---------- мины ---------- */

func (r *TRoom) clearMines(owner int) {
	keep := r.mines[:0]
	for _, m := range r.mines {
		if m.owner != owner {
			keep = append(keep, m)
		}
	}
	r.mines = keep
}

func (r *TRoom) stepMines(now time.Time) {
	keep := r.mines[:0]
	for _, mn := range r.mines {
		boom := false
		if now.After(mn.armAt) {
			owner := r.players[mn.owner]
			for _, p := range r.players {
				if !p.alive || p.id == mn.owner || (r.teams() && p.team == mn.team) || p.prot {
					continue
				}
				if near(p.pos, mn.pos, p.H().HW+1, 1.6) {
					r.all(map[string]any{"t": "ev", "k": "mine", "p": mn.pos.r()})
					r.damage(p, owner, 120+rand.Float64()*120, "Мина", mn.pos, false)
					boom = true
					break
				}
			}
		}
		if !boom {
			keep = append(keep, mn)
		}
	}
	r.mines = keep
}

/* ---------- припасы (клавиши 1–5) ---------- */

func (r *TRoom) useSupply(p *TPlayer, i int) {
	if !p.alive || r.over || i < 0 || i > 4 {
		return
	}
	now := time.Now()
	if now.Before(p.supCD[i]) {
		return
	}
	if i == 0 && p.hp >= p.maxHP() {
		return
	}
	tp.Lock()
	pf := profOf(p.uid)
	if pf.Sup[i] <= 0 {
		tp.Unlock()
		return
	}
	pf.Sup[i]--
	tp.dirty = true
	tp.Unlock()
	switch i {
	case 0:
		p.supCD[0] = now.Add(repairCD)
		r.heal(p, nil, p.maxHP())
	case 1:
		p.armUntil, p.supCD[1] = now.Add(supDur), now.Add(supDur)
	case 2:
		p.ddUntil, p.supCD[2] = now.Add(supDur), now.Add(supDur)
	case 3:
		p.nitroUntil, p.supCD[3] = now.Add(supDur), now.Add(supDur)
	case 4:
		p.supCD[4] = now.Add(mineCD)
		n := 0
		for _, m := range r.mines {
			if m.owner == p.id {
				n++
			}
		}
		if n >= 12 {
			r.clearOldestMine(p.id)
		}
		r.nextID++
		r.mines = append(r.mines, &TMine{id: r.nextID, owner: p.id, team: p.team, pos: p.pos, armAt: now.Add(time.Second)})
	}
	p.prot = false
	r.all(map[string]any{"t": "ev", "k": "sup", "id": p.id, "i": i})
	p.send(p.meMsg())
	p.sendProf()
}

func (r *TRoom) clearOldestMine(owner int) {
	for i, m := range r.mines {
		if m.owner == owner {
			r.mines = append(r.mines[:i], r.mines[i+1:]...)
			return
		}
	}
}

/* ---------- такт и снимок ---------- */

func (r *TRoom) stepModes(now time.Time) {
	switch r.mode {
	case "ctf":
		r.stepFlags(now)
	case "cp":
		r.stepCP(now)
	}
	if !r.over {
		r.stepBoxes(now)
		r.stepMines(now)
	}
}

func (r *TRoom) modeSnap(msg map[string]any) {
	if r.mode == "ctf" {
		fl := [][]float64{}
		for t := 1; t <= 2; t++ {
			f := r.flags[t]
			st := 0.0
			if f.carrier != 0 {
				st = 1
			} else if !f.atHome {
				st = 2
			}
			fl = append(fl, []float64{st, r2(f.pos[0]), r2(f.pos[1]), r2(f.pos[2]), float64(f.carrier)})
		}
		msg["fl"] = fl
	}
	if r.mode == "cp" {
		cp := [][]float64{}
		for _, c := range r.cps {
			cp = append(cp, []float64{math.Round(c.prog), float64(c.owner)})
		}
		msg["cp"] = cp
	}
	bx := [][]float64{}
	for _, b := range r.boxes {
		bx = append(bx, []float64{float64(b.id), float64(b.kind), r2(b.pos[0]), r2(b.pos[1]), r2(b.pos[2]), b2f(b.landed)})
	}
	msg["bx"] = bx
	mn := [][]float64{}
	now := time.Now()
	for _, m := range r.mines {
		mn = append(mn, []float64{float64(m.id), float64(m.owner), float64(m.team), r2(m.pos[0]), r2(m.pos[1]), r2(m.pos[2]), b2f(now.After(m.armAt))})
	}
	msg["mn"] = mn
}
