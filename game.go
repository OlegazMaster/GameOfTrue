// Арена: онлайн-шутер от первого лица (дезматч). Свой WebSocket, без внешних библиотек.
package main

import (
	"bufio"
	"crypto/sha1"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/fnv"
	"io"
	"math"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

//go:embed web/game.html
var gameHTML []byte

/* ---------- векторы ---------- */

type V3 [3]float64

func (a V3) add(b V3) V3      { return V3{a[0] + b[0], a[1] + b[1], a[2] + b[2]} }
func (a V3) sub(b V3) V3      { return V3{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }
func (a V3) mul(s float64) V3 { return V3{a[0] * s, a[1] * s, a[2] * s} }
func (a V3) dot(b V3) float64 { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }
func (a V3) length() float64  { return math.Sqrt(a.dot(a)) }
func (a V3) cross(b V3) V3 {
	return V3{a[1]*b[2] - a[2]*b[1], a[2]*b[0] - a[0]*b[2], a[0]*b[1] - a[1]*b[0]}
}
func (a V3) norm() V3 {
	l := a.length()
	if l < 1e-9 {
		return V3{0, 1, 0}
	}
	return a.mul(1 / l)
}
func r2(x float64) float64 { return math.Round(x*100) / 100 }
func (a V3) r() V3         { return V3{r2(a[0]), r2(a[1]), r2(a[2])} }
func v3(s []float64) (V3, bool) {
	if len(s) != 3 {
		return V3{}, false
	}
	for _, x := range s {
		if math.IsNaN(x) || math.IsInf(x, 0) || math.Abs(x) > 1000 {
			return V3{}, false
		}
	}
	return V3{s[0], s[1], s[2]}, true
}

/* ---------- карта ---------- */

type MBox struct {
	B [6]float64 `json:"b"`
	M string     `json:"m"`
	P *V3        `json:"p,omitempty"` // толчок джамп-пада
	R int        `json:"r,omitempty"` // пандус: 1 подъём к +X, 2 к −X, 3 к +Z, 4 к −Z (только в «Танках»)
}
type MLight struct {
	P V3      `json:"p"`
	C V3      `json:"c"`
	R float64 `json:"r"`
}
type MItem struct {
	K  string `json:"k"`
	P  V3     `json:"p"`
	On bool   `json:"on"`
	at time.Time
}

func bx(m string, x0, y0, z0, x1, y1, z1 float64) MBox {
	return MBox{B: [6]float64{x0, y0, z0, x1, y1, z1}, M: m}
}

func buildMap() ([]MBox, []MLight, []MItem, []V3) {
	b := []MBox{
		// пол с ямой лавы в центре
		bx("floor", -32, -4, -32, 32, 0, -6), bx("floor", -32, -4, 6, 32, 0, 32),
		bx("floor", -32, -4, -6, -6, 0, 6), bx("floor", 6, -4, -6, 32, 0, 6),
		bx("lava", -6, -4, -6, 6, -2.5, 6),
		bx("metal", -6, -0.4, -1.5, 6, 0, 1.5),
		bx("trim", -6, 0, -1.6, 6, 0.6, -1.4), bx("trim", -6, 0, 1.4, 6, 0.6, 1.6),
		// внешние стены
		bx("wall", -33, -4, -33, 33, 12, -32), bx("wall", -33, -4, 32, 33, 12, 33),
		bx("wall", -33, -4, -32, -32, 12, 32), bx("wall", 32, -4, -32, 33, 12, 32),
		bx("light", -20, 6, 31.7, 20, 6.4, 32), bx("light", -20, 6, -32, 20, 6.4, -31.7),
		bx("trim", -32, 11.6, -32, 32, 12, -31.6), bx("trim", -32, 11.6, 31.6, 32, 12, 32),
		// северная база
		bx("stone", -14, 0, 22, 14, 3, 32), bx("trim", -14, 2.7, 21.8, 14, 3.02, 22.02),
		bx("wall", -12, 3, 26, -10, 7, 28), bx("wall", 10, 3, 26, 12, 7, 28),
		bx("metal", -14, 7, 26, 14, 7.6, 32),
		bx("crate", -2, 3, 29, 2, 4.6, 31),
		// южная база
		bx("stone", -14, 0, -32, 14, 3, -22), bx("trim", -14, 2.7, -22.02, 14, 3.02, -21.8),
		bx("crate", -7, 3, -29, -4.5, 5, -26.5), bx("crate", 4, 3, -30.5, 6, 4.5, -28.5),
		bx("pad", -1.5, 0, -19, 1.5, 0.2, -16),
		bx("wall", -14, 3, -32, -12, 8, -30), bx("wall", 12, 3, -32, 14, 8, -30),
		// западное здание с коридором
		bx("wall", -21, 0, -16, -20, 4, -3), bx("wall", -21, 0, 3, -20, 4, 16),
		bx("wall", -32, 0, -17, -28, 4, -16), bx("wall", -25, 0, -17, -20, 4, -16),
		bx("wall", -32, 0, 16, -28, 4, 17), bx("wall", -25, 0, 16, -20, 4, 17),
		bx("metal", -32, 4, -17, -20, 4.5, 17),
		bx("light", -27, 3.8, -8, -25, 4, -6), bx("light", -27, 3.8, 6, -25, 4, 8),
		bx("crate", -31.5, 0, -2, -29.5, 1.5, 0), bx("crate", -23, 0, 9, -21.2, 1.2, 11),
		bx("crate", -19, 0, 10, -17, 1.5, 12), bx("crate", -19, 1.5, 10.5, -17.4, 3.3, 12),
		// восточная башня
		bx("stone", 24, 0, -4, 32, 7, 4), bx("trim", 23.8, 6.7, -4, 24.02, 7.02, 4),
		bx("light", 23.8, 4.5, -1, 24, 5.5, 1),
		bx("crate", 26, 7, 2.6, 27.4, 8.2, 4), bx("crate", 29, 7, -4, 31, 8.4, -2.4),
		// центр: ящики и колонны
		bx("crate", -14, 0, -12, -11, 2, -9), bx("crate", -13.2, 2, -11.4, -11.6, 3.2, -9.8),
		bx("crate", 10, 0, 9, 13, 2, 12), bx("crate", 10.8, 2, 9.6, 12.4, 3.2, 11.2),
		bx("crate", -12, 0, 10, -9, 1.5, 13), bx("crate", 9, 0, -13, 12, 1.5, -10),
		bx("crate", 16, 0, 16, 18, 1.4, 18), bx("crate", -18, 0, -18, -16, 1.4, -16),
		bx("metal", -8, 0, -8, -7, 6, -7), bx("metal", 7, 0, -8, 8, 6, -7),
		bx("metal", -8, 0, 7, -7, 6, 8), bx("metal", 7, 0, 7, 8, 6, 8),
		bx("light", -8.2, 6, -8.2, -6.8, 6.4, -6.8), bx("light", 6.8, 6, -8.2, 8.2, 6.4, -6.8),
		bx("light", -8.2, 6, 6.8, -6.8, 6.4, 8.2), bx("light", 6.8, 6, 6.8, 8.2, 6.4, 8.2),
	}
	// лестница к северной базе
	for i := 1; i <= 6; i++ {
		f := float64(i)
		b = append(b, bx("stone", -3, 0, 22-f, 3, 3-0.5*(f-1), 23-f))
	}
	// боковые лестницы южной базы
	for i := 1; i <= 6; i++ {
		f := float64(i)
		h := 3 - 0.5*(f-1)
		b = append(b, bx("stone", -14-f, 0, -28, -13-f, h, -24), bx("stone", 13+f, 0, -28, 14+f, h, -24))
	}
	padS := bx("pad", -1.5, 0, -19, 1.5, 0.2, -16)
	for i := range b {
		if b[i].M == "pad" && b[i].B == padS.B {
			b[i].P = &V3{0, 13.5, -6.5}
		}
	}
	tower := bx("pad", 18, 0, -1.5, 21, 0.2, 1.5)
	tower.P = &V3{6, 19, 0}
	b = append(b, tower)

	lights := []MLight{
		{V3{0, 5.5, 29}, V3{1.0, 0.65, 0.35}, 26}, {V3{0, 5.5, -29}, V3{1.0, 0.65, 0.35}, 26},
		{V3{-26, 3.4, -7}, V3{0.45, 0.65, 1.0}, 10}, {V3{-26, 3.4, 7}, V3{0.45, 0.65, 1.0}, 10},
		{V3{-7.5, 6.8, -7.5}, V3{1, 0.85, 0.5}, 11}, {V3{7.5, 6.8, -7.5}, V3{1, 0.85, 0.5}, 11},
		{V3{-7.5, 6.8, 7.5}, V3{1, 0.85, 0.5}, 11}, {V3{7.5, 6.8, 7.5}, V3{1, 0.85, 0.5}, 11},
		{V3{0, -1.2, 0}, V3{1.0, 0.35, 0.08}, 14}, {V3{22, 5, 0}, V3{0.4, 0.6, 1.0}, 13},
		{V3{0, 1, -17.5}, V3{0.3, 1.0, 0.4}, 6}, {V3{19.5, 1, 0}, V3{0.3, 1.0, 0.4}, 6},
	}
	items := []MItem{
		{K: "w2", P: V3{-26, 0, 0}}, {K: "w3", P: V3{0, 0, 12}}, {K: "w4", P: V3{0, 0, -12}},
		{K: "w5", P: V3{0, 3, -27}}, {K: "w6", P: V3{28, 7, 0}}, {K: "w7", P: V3{0, 3, 25}},
		{K: "hp", P: V3{-30, 0, 10}}, {K: "hp", P: V3{-30, 0, -10}}, {K: "hp", P: V3{16, 0, 12}},
		{K: "hp", P: V3{16, 0, -12}}, {K: "hp", P: V3{12, 3, 24}}, {K: "hp", P: V3{-10, 3, -24}},
		{K: "ar", P: V3{-26, 4.5, 0}}, {K: "ar", P: V3{0, 0, 0}},
		{K: "w8", P: V3{-15, 0, 5}}, {K: "w9", P: V3{-26, 4.5, 10}},
	}
	for i := range items {
		items[i].On = true
	}
	spawns := []V3{{-20, 0, -24}, {20, 0, -24}, {-20, 0, 24}, {20, 0, 24}, {-26, 0, -12}, {14, 0, 0},
		{-14, 0, 0}, {8, 3, 30}, {-8, 3, -30}, {28, 0, 12}, {28, 0, -12}, {-26, 0, 12}}
	return b, lights, items, spawns
}

/* ---------- оружие ---------- */

type Weapon struct {
	Name    string  `json:"n"`
	Kind    string  `json:"k"` // hit | rail | proj
	Ammo    string  `json:"a"`
	Rate    int     `json:"r"` // мс между выстрелами
	Dmg     int     `json:"-"`
	Pellets int     `json:"-"`
	Spread  float64 `json:"-"`
	Start   int     `json:"-"`
	Pick    int     `json:"-"`
	Max     int     `json:"-"`
	Speed   float64 `json:"-"`
	Grav    float64 `json:"-"`
	Radius  float64 `json:"-"`
	Life    float64 `json:"-"` // снаряд гаснет через столько секунд без взрыва
}

var weapons = []Weapon{
	{Name: "Бластер", Kind: "hit", Rate: 380, Dmg: 15, Pellets: 1},
	{Name: "Дробовик", Kind: "hit", Ammo: "shells", Rate: 900, Dmg: 7, Pellets: 10, Spread: 0.075, Pick: 10, Max: 50},
	{Name: "Автомат", Kind: "hit", Ammo: "bullets", Rate: 95, Dmg: 8, Pellets: 1, Spread: 0.022, Start: 50, Pick: 50, Max: 200},
	{Name: "Гранатомёт", Kind: "proj", Ammo: "grenades", Rate: 800, Dmg: 100, Pick: 6, Max: 30, Speed: 19, Grav: 18, Radius: 4},
	{Name: "Ракетница", Kind: "proj", Ammo: "rockets", Rate: 850, Dmg: 100, Pick: 6, Max: 30, Speed: 26, Radius: 3.6},
	{Name: "Гипербластер", Kind: "proj", Ammo: "cells", Rate: 100, Dmg: 14, Pick: 50, Max: 200, Speed: 42},
	{Name: "Рельсотрон", Kind: "rail", Ammo: "slugs", Rate: 1500, Dmg: 100, Pellets: 1, Pick: 6, Max: 30},
	{Name: "Огнемёт", Kind: "proj", Ammo: "fuel", Rate: 70, Dmg: 6, Pick: 60, Max: 200, Speed: 19, Grav: -3, Spread: 0.05, Life: 0.5},
	{Name: "BFG", Kind: "proj", Ammo: "bfg", Rate: 2200, Dmg: 160, Pick: 2, Max: 6, Speed: 13, Radius: 7.5},
}

const nW = 9

/* ---------- игроки и снаряды ---------- */

type histPt struct {
	t time.Time
	p V3
}

type GPlayer struct {
	id           int
	uid, name    string
	color        int
	ws           *wsConn
	out          chan []byte
	pos          V3
	yaw, pitch   float64
	w            int
	hp, armor    int
	alive        bool
	respawnAt    time.Time
	spawnedAt    time.Time
	has          [nW]bool
	ammo         map[string]int
	last         [nW]time.Time
	frags, death int
	hist         [40]histPt
	hi           int
	lavaAt       time.Time
	lastBy       int
	lastByAt     time.Time
}

type Proj struct {
	id, owner, w int
	pos, vel     V3
	born, fuse   time.Time
}

type Game struct {
	key     string
	theme   Theme
	mu      sync.Mutex
	boxes   []MBox
	lights  []MLight
	items   []MItem
	spawns  []V3
	players map[int]*GPlayer
	projs   []*Proj
	nextID  int
	mapJSON json.RawMessage
}

var games = map[string]*Game{"arena": newGame("arena", arenaMap()), "town": newGame("town", townMap())}

func newGame(key string, md MapDef) *Game {
	g := &Game{key: key, theme: md.theme, players: map[int]*GPlayer{}}
	g.boxes, g.lights, g.items, g.spawns = md.boxes, md.lights, md.items, md.spawns
	if g.lights == nil {
		g.lights = []MLight{}
	}
	g.mapJSON, _ = json.Marshal(map[string]any{"boxes": g.boxes, "lights": g.lights, "spawns": g.spawns, "theme": g.theme, "key": key})
	go g.loop()
	return g
}

func (p *GPlayer) send(v any) {
	b, _ := json.Marshal(v)
	select {
	case p.out <- b:
	default:
	}
}

func (g *Game) all(v any) {
	b, _ := json.Marshal(v)
	for _, p := range g.players {
		select {
		case p.out <- b:
		default:
		}
	}
}

func (p *GPlayer) meMsg() map[string]any {
	has := []int{}
	for i, h := range p.has {
		if h {
			has = append(has, i)
		}
	}
	return map[string]any{"t": "me", "hp": p.hp, "ar": p.armor, "am": p.ammo, "has": has, "alive": p.alive, "f": p.frags, "d": p.death}
}

func (g *Game) scores() map[string]any {
	l := []map[string]any{}
	for _, p := range g.players {
		l = append(l, map[string]any{"id": p.id, "n": p.name, "f": p.frags, "d": p.death, "c": p.color})
	}
	return map[string]any{"t": "sc", "l": l}
}

func (p *GPlayer) histAt(t time.Time) V3 {
	best := p.pos
	var newer *histPt
	for i := 0; i < len(p.hist); i++ {
		h := &p.hist[(p.hi-1-i+len(p.hist)*2)%len(p.hist)]
		if h.t.IsZero() {
			break
		}
		if !h.t.After(t) {
			if newer == nil {
				return h.p
			}
			span := newer.t.Sub(h.t).Seconds()
			if span <= 0 {
				return h.p
			}
			f := t.Sub(h.t).Seconds() / span
			return h.p.add(newer.p.sub(h.p).mul(f))
		}
		newer = h
		best = h.p
	}
	return best
}

func pbox(p V3) [6]float64 {
	return [6]float64{p[0] - 0.45, p[1], p[2] - 0.45, p[0] + 0.45, p[1] + 1.85, p[2] + 0.45}
}

// rayBox: пересечение луча o + d*t (0..maxT) с коробкой. Возвращает t и ось грани.
func rayBox(o, d V3, b [6]float64, maxT float64) (float64, int, bool) {
	tmin, tmax, ax := 0.0, maxT, -1
	for i := 0; i < 3; i++ {
		if math.Abs(d[i]) < 1e-9 {
			if o[i] < b[i] || o[i] > b[i+3] {
				return 0, 0, false
			}
			continue
		}
		t1, t2 := (b[i]-o[i])/d[i], (b[i+3]-o[i])/d[i]
		if t1 > t2 {
			t1, t2 = t2, t1
		}
		if t1 > tmin {
			tmin, ax = t1, i
		}
		if t2 < tmax {
			tmax = t2
		}
		if tmin > tmax {
			return 0, 0, false
		}
	}
	return tmin, ax, true
}

func (g *Game) mapRay(o, d V3, maxT float64) (float64, int, bool) {
	best, bax, hit := maxT, -1, false
	for i := range g.boxes {
		if t, ax, ok := rayBox(o, d, g.boxes[i].B, best); ok && t < best {
			best, bax, hit = t, ax, true
		}
	}
	return best, bax, hit
}

func spreadDir(d V3, s float64) V3 {
	if s == 0 {
		return d
	}
	up := V3{0, 1, 0}
	if math.Abs(d[1]) > 0.95 {
		up = V3{1, 0, 0}
	}
	r := d.cross(up).norm()
	u := r.cross(d).norm()
	return d.add(r.mul((rand.Float64()*2 - 1) * s)).add(u.mul((rand.Float64()*2 - 1) * s)).norm()
}

/* ---------- урон ---------- */

func (g *Game) damage(t, by *GPlayer, dmg int, wname string, from V3) {
	if !t.alive || dmg <= 0 {
		return
	}
	if time.Since(t.spawnedAt) < 1200*time.Millisecond && by != t {
		return // защита после возрождения
	}
	save := t.armor
	if s := dmg * 2 / 3; s < save {
		save = s
	}
	t.armor -= save
	t.hp -= dmg - save
	t.send(map[string]any{"t": "ev", "k": "hurt", "d": dmg, "f": from.r()})
	if by != nil && by != t {
		by.send(map[string]any{"t": "ev", "k": "hm", "d": dmg})
		t.lastBy, t.lastByAt = by.id, time.Now()
	}
	if t.hp <= 0 {
		t.hp, t.alive = 0, false
		t.death++
		t.respawnAt = time.Now().Add(3 * time.Second)
		killer := ""
		if by != nil && by != t {
			by.frags++
			killer = by.name
			by.send(by.meMsg())
		} else {
			t.frags--
		}
		g.all(map[string]any{"t": "ev", "k": "kill", "a": killer, "v": t.name, "w": wname, "vid": t.id})
		g.all(g.scores())
	}
	t.send(t.meMsg())
}

func (g *Game) explode(pr *Proj, at V3) {
	wp := weapons[pr.w]
	owner := g.players[pr.owner]
	g.all(map[string]any{"t": "ev", "k": "boom", "p": at.r(), "r": wp.Radius, "w": pr.w})
	for _, p := range g.players {
		if !p.alive {
			continue
		}
		b := pbox(p.pos)
		var near V3
		for i := 0; i < 3; i++ {
			near[i] = math.Max(b[i], math.Min(at[i], b[i+3]))
		}
		dist := near.sub(at).length()
		if dist >= wp.Radius {
			continue
		}
		f := 1 - dist/wp.Radius
		dmg := int(float64(wp.Dmg) * (0.25 + 0.75*f))
		kb := 13.0
		if p == owner {
			dmg /= 2
			kb = 11
		}
		dir := p.pos.add(V3{0, 0.9, 0}).sub(at).norm()
		p.send(map[string]any{"t": "ev", "k": "kb", "v": dir.mul(kb * f).r()})
		g.damage(p, owner, dmg, wp.Name, at)
	}
}

func (g *Game) fire(p *GPlayer, w int, o, d V3, lag float64) {
	if w < 0 || w >= len(weapons) || !p.has[w] || !p.alive {
		return
	}
	wp := weapons[w]
	now := time.Now()
	if now.Sub(p.last[w]) < time.Duration(float64(wp.Rate)*0.8)*time.Millisecond {
		return
	}
	if wp.Ammo != "" {
		if p.ammo[wp.Ammo] <= 0 {
			return
		}
		p.ammo[wp.Ammo]--
	}
	p.last[w] = now
	eye := p.pos.add(V3{0, 1.6, 0})
	if o.sub(eye).length() > 3 {
		o = eye
	}
	d = d.norm()
	if lag < 0 {
		lag = 0
	}
	if lag > 350 {
		lag = 350
	}
	past := now.Add(-time.Duration(lag) * time.Millisecond)
	switch wp.Kind {
	case "hit", "rail":
		dmgBy := map[*GPlayer]int{}
		ends := [][3]float64{}
		for i := 0; i < wp.Pellets; i++ {
			dir := spreadDir(d, wp.Spread)
			wt, _, _ := g.mapRay(o, dir, 200)
			if wp.Kind == "rail" {
				for _, t := range g.players {
					if t == p || !t.alive {
						continue
					}
					if _, _, ok := rayBox(o, dir, pbox(t.histAt(past)), wt); ok {
						dmgBy[t] += wp.Dmg
					}
				}
			} else {
				var best *GPlayer
				bt := wt
				for _, t := range g.players {
					if t == p || !t.alive {
						continue
					}
					if tt, _, ok := rayBox(o, dir, pbox(t.histAt(past)), bt); ok && tt < bt {
						bt, best = tt, t
					}
				}
				if best != nil {
					dmgBy[best] += wp.Dmg
					wt = bt
				}
			}
			e := o.add(dir.mul(wt)).r()
			ends = append(ends, [3]float64(e))
		}
		g.all(map[string]any{"t": "ev", "k": "shot", "id": p.id, "w": w, "o": o.r(), "e": ends})
		for t, dmg := range dmgBy {
			g.damage(t, p, dmg, wp.Name, o)
		}
	case "proj":
		g.nextID++
		vel := d.mul(wp.Speed)
		if w == 3 {
			vel = vel.add(V3{0, 3, 0})
		}
		if wp.Spread > 0 {
			vel = spreadDir(d, wp.Spread).mul(wp.Speed)
		}
		pr := &Proj{id: g.nextID, owner: p.id, w: w, pos: o.add(d.mul(0.5)), vel: vel, born: now, fuse: now.Add(2200 * time.Millisecond)}
		g.projs = append(g.projs, pr)
		g.all(map[string]any{"t": "ev", "k": "shot", "id": p.id, "w": w, "o": o.r(), "e": [][3]float64{}})
	}
	p.send(p.meMsg())
}

func (g *Game) respawn(p *GPlayer) {
	best, bd := g.spawns[0], -1.0
	for _, s := range g.spawns {
		md := 1e9
		for _, o := range g.players {
			if o != p && o.alive {
				md = math.Min(md, o.pos.sub(s).length())
			}
		}
		md += rand.Float64() * 4
		if md > bd {
			best, bd = s, md
		}
	}
	p.pos = best
	p.hp, p.armor, p.alive = 100, 0, true
	p.has = [nW]bool{true, false, true}
	p.ammo = map[string]int{"bullets": weapons[2].Start}
	p.spawnedAt = time.Now()
	p.hist = [40]histPt{}
	yaw := math.Atan2(best[0], best[2]) // лицом к центру
	p.send(map[string]any{"t": "ev", "k": "spawn", "p": best, "yaw": r2(yaw)})
	p.send(p.meMsg())
}

func (g *Game) pickup(p *GPlayer, i int) {
	it := &g.items[i]
	msg := ""
	resp := 20 * time.Second
	switch {
	case strings.HasPrefix(it.K, "w"):
		w := int(it.K[1] - '1')
		wp := weapons[w]
		if p.has[w] && p.ammo[wp.Ammo] >= wp.Max {
			return
		}
		got := !p.has[w]
		p.has[w] = true
		p.ammo[wp.Ammo] = min(wp.Max, p.ammo[wp.Ammo]+wp.Pick)
		msg = wp.Name
		resp = 15 * time.Second
		p.send(map[string]any{"t": "ev", "k": "pick", "s": msg, "w": w, "new": got})
	case it.K == "hp":
		if p.hp >= 100 {
			return
		}
		p.hp = min(100, p.hp+25)
		p.send(map[string]any{"t": "ev", "k": "pick", "s": "+25 здоровья"})
	case it.K == "ar":
		if p.armor >= 100 {
			return
		}
		p.armor = min(100, p.armor+50)
		resp = 25 * time.Second
		p.send(map[string]any{"t": "ev", "k": "pick", "s": "+50 брони"})
	}
	it.On, it.at = false, time.Now().Add(resp)
	g.all(map[string]any{"t": "ev", "k": "item", "i": i, "on": false})
	p.send(p.meMsg())
}

/* ---------- игровой цикл ---------- */

func (g *Game) loop() {
	tk := time.NewTicker(time.Second / 30)
	for range tk.C {
		g.mu.Lock()
		g.step(time.Now())
		g.mu.Unlock()
	}
}

func (g *Game) step(now time.Time) {
	const dt = 1.0 / 30
	{
		if len(g.players) == 0 {
			g.projs = nil
			return
		}
		// снаряды
		keep := g.projs[:0]
		for _, pr := range g.projs {
			wp := weapons[pr.w]
			if wp.Grav > 0 {
				pr.vel[1] -= wp.Grav * dt
			}
			step := pr.vel.mul(dt)
			L := step.length()
			dir := step.norm()
			wt, ax, wok := g.mapRay(pr.pos, dir, L)
			var hitP *GPlayer
			pt := L
			for _, t := range g.players {
				if !t.alive || (t.id == pr.owner && (now.Sub(pr.born) < 300*time.Millisecond || wp.Radius == 0)) {
					continue
				}
				if tt, _, ok := rayBox(pr.pos, dir, pbox(t.pos), pt); ok && tt < pt {
					pt, hitP = tt, t
				}
			}
			dead := false
			switch {
			case hitP != nil && (!wok || pt <= wt):
				at := pr.pos.add(dir.mul(pt))
				if wp.Radius == 0 {
					if pr.w == 5 {
						g.all(map[string]any{"t": "ev", "k": "spark", "p": at.r()})
					}
					g.damage(hitP, g.players[pr.owner], wp.Dmg, wp.Name, pr.pos)
				} else {
					g.explode(pr, at)
				}
				dead = true
			case wok:
				at := pr.pos.add(dir.mul(math.Max(0, wt-0.05)))
				if pr.w == 3 && ax >= 0 {
					pr.pos = at
					pr.vel[ax] = -pr.vel[ax] * 0.45
					for i := 0; i < 3; i++ {
						if i != ax {
							pr.vel[i] *= 0.7
						}
					}
				} else if wp.Radius == 0 {
					if pr.w == 5 {
						g.all(map[string]any{"t": "ev", "k": "spark", "p": at.r()})
					}
					dead = true
				} else {
					g.explode(pr, at)
					dead = true
				}
			default:
				pr.pos = pr.pos.add(step)
			}
			if !dead && pr.w == 3 && now.After(pr.fuse) {
				g.explode(pr, pr.pos)
				dead = true
			}
			if !dead && wp.Life > 0 && now.Sub(pr.born).Seconds() > wp.Life {
				dead = true
			}
			if !dead && now.Sub(pr.born) > 6*time.Second {
				dead = true
			}
			if !dead {
				keep = append(keep, pr)
			}
		}
		g.projs = keep
		// предметы
		for i := range g.items {
			it := &g.items[i]
			if !it.On && now.After(it.at) {
				it.On = true
				g.all(map[string]any{"t": "ev", "k": "item", "i": i, "on": true})
			}
		}
		// игроки
		pl := [][]float64{}
		for _, p := range g.players {
			if !p.alive {
				if !p.respawnAt.IsZero() && now.After(p.respawnAt) {
					g.respawn(p)
				}
			} else {
				for i := range g.items {
					it := &g.items[i]
					if it.On && math.Hypot(p.pos[0]-it.P[0], p.pos[2]-it.P[2]) < 1.1 && math.Abs(p.pos[1]-it.P[1]) < 1.4 {
						g.pickup(p, i)
					}
				}
				if p.pos[1] < g.theme.lavaY && now.After(p.lavaAt) {
					p.lavaAt = now.Add(400 * time.Millisecond)
					var by *GPlayer
					if now.Sub(p.lastByAt) < 4*time.Second {
						by = g.players[p.lastBy]
					}
					name := "Лава"
					if by != nil {
						name = "Лава (столкнул)"
					}
					p.spawnedAt = time.Time{}
					g.damage(p, by, 25, name, p.pos)
				}
			}
			alive := 0.0
			if p.alive {
				alive = 1
			}
			pl = append(pl, []float64{float64(p.id), r2(p.pos[0]), r2(p.pos[1]), r2(p.pos[2]), r2(p.yaw), r2(p.pitch), float64(p.w), alive, float64(p.color)})
		}
		pr := [][]float64{}
		for _, x := range g.projs {
			pr = append(pr, []float64{float64(x.id), float64(x.w), r2(x.pos[0]), r2(x.pos[1]), r2(x.pos[2])})
		}
		g.all(map[string]any{"t": "s", "pl": pl, "pr": pr})
	}
}

/* ---------- WebSocket (RFC 6455, минимально) ---------- */

type wsConn struct {
	c  net.Conn
	br *bufio.Reader
	wm sync.Mutex
}

func wsUpgrade(w http.ResponseWriter, r *http.Request) (*wsConn, error) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") || r.Header.Get("Sec-WebSocket-Key") == "" {
		return nil, errors.New("not websocket")
	}
	h := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("no hijack")
	}
	c, brw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}
	brw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(h[:]) + "\r\n\r\n")
	if err := brw.Flush(); err != nil {
		c.Close()
		return nil, err
	}
	return &wsConn{c: c, br: brw.Reader}, nil
}

func (ws *wsConn) write(op byte, data []byte) error {
	ws.wm.Lock()
	defer ws.wm.Unlock()
	hdr := []byte{0x80 | op}
	n := len(data)
	switch {
	case n < 126:
		hdr = append(hdr, byte(n))
	case n < 65536:
		hdr = append(hdr, 126, byte(n>>8), byte(n))
	default:
		hdr = append(hdr, 127)
		hdr = binary.BigEndian.AppendUint64(hdr, uint64(n))
	}
	ws.c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := ws.c.Write(append(hdr, data...)); err != nil {
		return err
	}
	return nil
}

func (ws *wsConn) read() ([]byte, error) {
	var msg []byte
	for {
		ws.c.SetReadDeadline(time.Now().Add(45 * time.Second))
		var h [2]byte
		if _, err := io.ReadFull(ws.br, h[:]); err != nil {
			return nil, err
		}
		fin, op, masked := h[0]&0x80 != 0, h[0]&0x0f, h[1]&0x80 != 0
		n := uint64(h[1] & 0x7f)
		if n == 126 {
			var e [2]byte
			if _, err := io.ReadFull(ws.br, e[:]); err != nil {
				return nil, err
			}
			n = uint64(binary.BigEndian.Uint16(e[:]))
		} else if n == 127 {
			var e [8]byte
			if _, err := io.ReadFull(ws.br, e[:]); err != nil {
				return nil, err
			}
			n = binary.BigEndian.Uint64(e[:])
		}
		if n > 1<<16 || !masked {
			return nil, errors.New("bad frame")
		}
		var mk [4]byte
		io.ReadFull(ws.br, mk[:])
		p := make([]byte, n)
		if _, err := io.ReadFull(ws.br, p); err != nil {
			return nil, err
		}
		for i := range p {
			p[i] ^= mk[i%4]
		}
		switch op {
		case 8:
			ws.write(8, nil)
			return nil, io.EOF
		case 9:
			ws.write(10, p)
			continue
		case 10:
			continue
		}
		msg = append(msg, p...)
		if fin {
			return msg, nil
		}
	}
}

/* ---------- HTTP ---------- */

func colorOf(uid string) int {
	h := fnv.New32a()
	h.Write([]byte(uid))
	return int(h.Sum32() % 8)
}

func hGameWS(w http.ResponseWriter, r *http.Request) {
	u, _ := authed(r)
	if u == nil || u.Banned {
		http.Error(w, "unauthorized", 401)
		return
	}
	ws, err := wsUpgrade(w, r)
	if err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	g := games[r.URL.Query().Get("map")]
	if g == nil {
		g = games["arena"]
	}
	g.mu.Lock()
	for _, o := range g.players { // второе окно того же человека вытесняет первое
		if o.uid == u.ID {
			o.ws.c.Close()
		}
	}
	g.nextID++
	p := &GPlayer{id: g.nextID, uid: u.ID, name: u.Name, color: colorOf(u.ID), ws: ws, out: make(chan []byte, 256), ammo: map[string]int{}}
	g.players[p.id] = p
	items := make([]bool, len(g.items))
	ik := make([]map[string]any, len(g.items))
	for i, it := range g.items {
		items[i] = it.On
		ik[i] = map[string]any{"k": it.K, "p": it.P, "on": it.On}
	}
	p.send(map[string]any{"t": "hi", "id": p.id, "map": g.mapJSON, "items": ik, "wp": weapons, "c": p.color, "name": p.name})
	g.respawn(p)
	g.all(g.scores())
	g.all(map[string]any{"t": "ev", "k": "join", "n": p.name})
	g.mu.Unlock()

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
			T    string
			P, O []float64
			D    []float64
			Y, X float64
			W    int
			Lag  float64
			C    float64
		}
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		g.mu.Lock()
		switch m.T {
		case "st":
			if pos, ok := v3(m.P); ok && p.alive {
				p.pos = pos
				p.yaw, p.pitch = m.Y, math.Max(-1.6, math.Min(1.6, m.X))
				if m.W >= 0 && m.W < len(weapons) && p.has[m.W] {
					p.w = m.W
				}
				p.hist[p.hi%len(p.hist)] = histPt{time.Now(), p.pos}
				p.hi = (p.hi + 1) % len(p.hist)
			}
		case "fire":
			o, ok1 := v3(m.O)
			d, ok2 := v3(m.D)
			if ok1 && ok2 {
				g.fire(p, m.W, o, d, m.Lag)
			}
		case "pi":
			p.send(map[string]any{"t": "po", "c": m.C})
		}
		g.mu.Unlock()
	}
	close(done)
	ws.c.Close()
	g.mu.Lock()
	if g.players[p.id] == p {
		delete(g.players, p.id)
		g.all(g.scores())
		g.all(map[string]any{"t": "ev", "k": "leave", "n": p.name})
	}
	g.mu.Unlock()
}

func hGameInfo(w http.ResponseWriter, r *http.Request, me *User) {
	out := []map[string]any{}
	for _, k := range mapOrder {
		g := games[k]
		g.mu.Lock()
		names := []string{}
		for _, p := range g.players {
			names = append(names, p.name)
		}
		g.mu.Unlock()
		out = append(out, map[string]any{"id": k, "name": g.theme.Name, "players": names})
	}
	ok(w, map[string]any{"maps": out})
}

const gameCSP = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; connect-src 'self' wss: ws:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

func hGamePage(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", gameCSP)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-cache")
	w.Write(gameHTML)
}
