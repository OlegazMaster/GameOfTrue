// Карты «Арены»: тёмная арена и дневной городок.
package main

type Theme struct {
	Name     string  `json:"name"`
	Sky      V3      `json:"sky"` // цвет горизонта и тумана
	Zen      V3      `json:"zen"` // цвет зенита
	Fog      float64 `json:"fog"`
	Amb      V3      `json:"amb"`
	Sun      V3      `json:"sun"` // направление лучей
	SunC     V3      `json:"sunc"`
	Bound    float64 `json:"bound"`
	FloorMin float64 `json:"floorMin"`
	lavaY    float64
}

type MapDef struct {
	theme  Theme
	boxes  []MBox
	lights []MLight
	items  []MItem
	spawns []V3
}

var mapOrder = []string{"arena", "town"}

func arenaMap() MapDef {
	b, l, it, sp := buildMap()
	return MapDef{Theme{Name: "Арена", Sky: V3{.16, .1, .07}, Zen: V3{.05, .03, .03}, Fog: .018, Amb: V3{.3, .28, .28},
		Sun: V3{-.35, -1, -.25}, SunC: V3{.5, .43, .36}, Bound: 32.4, FloorMin: -4, lavaY: -1.9}, b, l, it, sp}
}

/* ---------- конструктор построек ---------- */

type hole struct{ c, w, y0, y1 float64 }

// wall: стена вдоль оси ax (0 — вдоль X, 2 — вдоль Z) от a0 до a1, толщина по другой оси c0..c1, высота h, с проёмами.
func wall(m string, ax int, a0, a1, c0, c1, y0, h float64, holes ...hole) []MBox {
	mk := func(p0, p1, ya, yb float64) MBox {
		if ax == 0 {
			return bx(m, p0, ya, c0, p1, yb, c1)
		}
		return bx(m, c0, ya, p0, c1, yb, p1)
	}
	out := []MBox{}
	cur := a0
	for _, hl := range holes {
		l, r := hl.c-hl.w/2, hl.c+hl.w/2
		if l > cur {
			out = append(out, mk(cur, l, y0, y0+h))
		}
		if hl.y0 > 0 {
			out = append(out, mk(l, r, y0, y0+hl.y0))
		}
		if hl.y1 < h {
			out = append(out, mk(l, r, y0+hl.y1, y0+h))
		}
		cur = r
	}
	if a1 > cur {
		out = append(out, mk(cur, a1, y0, y0+h))
	}
	return out
}

// house: дом с дверью (door: "n","s","e","w") и окнами на противоположной стороне, плоская крыша.
func house(m string, x0, z0, w, d, h float64, door string) []MBox {
	const t = 0.5
	dh := hole{w: 2.2, y0: 0, y1: 2.8}
	wh := hole{w: 1.6, y0: 1.2, y1: 2.4}
	pick := func(side string, center float64) []hole {
		switch side {
		case door:
			x := dh
			x.c = center
			return []hole{x}
		case map[string]string{"n": "s", "s": "n", "e": "w", "w": "e"}[door]:
			a, b := wh, wh
			a.c, b.c = center-2.5, center+2.5
			return []hole{a, b}
		}
		return nil
	}
	cx, cz := x0+w/2, z0+d/2
	out := []MBox{}
	out = append(out, wall(m, 0, x0, x0+w, z0, z0+t, 0, h, pick("s", cx)...)...)
	out = append(out, wall(m, 0, x0, x0+w, z0+d-t, z0+d, 0, h, pick("n", cx)...)...)
	out = append(out, wall(m, 2, z0+t, z0+d-t, x0, x0+t, 0, h, pick("w", cz)...)...)
	out = append(out, wall(m, 2, z0+t, z0+d-t, x0+w-t, x0+w, 0, h, pick("e", cz)...)...)
	out = append(out, bx("wood", x0+t, -0.5, z0+t, x0+w-t, 0.06, z0+d-t))
	out = append(out, bx("roof", x0-0.3, h, z0-0.3, x0+w+0.3, h+0.4, z0+d+0.3))
	return out
}

// stairs: n ступеней высотой до top. Вершина у края at по оси ax, лестница уходит в сторону dir (+1 — к меньшим координатам).
func stairs(m string, ax int, at, dir, c0, c1, top float64, n int, depth float64) []MBox {
	out := []MBox{}
	sh := top / float64(n)
	for i := 1; i <= n; i++ {
		fi := float64(i)
		p0, p1 := at-dir*fi*depth, at-dir*(fi-1)*depth
		if p0 > p1 {
			p0, p1 = p1, p0
		}
		hgt := top - (fi-1)*sh
		if ax == 0 {
			out = append(out, bx(m, p0, 0, c0, p1, hgt, c1))
		} else {
			out = append(out, bx(m, c0, 0, p0, c1, hgt, p1))
		}
	}
	return out
}

func tree(x, z float64) []MBox {
	return []MBox{bx("bark", x-0.3, 0, z-0.3, x+0.3, 3, z+0.3), bx("leaves", x-1.8, 2.6, z-1.8, x+1.8, 5.2, z+1.8), bx("leaves", x-1.1, 5.2, z-1.1, x+1.1, 6.3, z+1.1)}
}

func stall(x, z float64) []MBox {
	out := []MBox{}
	for _, p := range [][2]float64{{x, z}, {x + 5.7, z}, {x, z + 3.7}, {x + 5.7, z + 3.7}} {
		out = append(out, bx("bark", p[0], 0, p[1], p[0]+0.3, 2.6, p[1]+0.3))
	}
	out = append(out, bx("cloth", x-0.2, 2.6, z-0.2, x+6.2, 2.8, z+4.2), bx("wood", x+0.5, 0, z+0.5, x+5.5, 1, z+1.4))
	return out
}

func townMap() MapDef {
	b := []MBox{
		bx("sand", -48, -3, -48, 48, 0, -3), bx("sand", -48, -3, 3, 48, 0, 48),
		bx("water", -48, -3, -3, 48, -1, 3),
		bx("paving", -16, -0.5, 8, 16, 0.06, 34),
		// городские стены
		bx("brick", -49, -3, -49, 49, 8, -48), bx("brick", -49, -3, 48, 49, 8, 49),
		bx("brick", -49, -3, -48, -48, 8, 48), bx("brick", 48, -3, -48, 49, 8, 48),
		// фонтан
		bx("stone", -4, 0, 17, 4, 0.8, 17.6), bx("stone", -4, 0, 24.4, 4, 0.8, 25),
		bx("stone", -4, 0, 17.6, -3.4, 0.8, 24.4), bx("stone", 3.4, 0, 17.6, 4, 0.8, 24.4),
		bx("water", -3.4, 0, 17.6, 3.4, 0.45, 24.4),
		bx("stone", -0.6, 0, 20.4, 0.6, 2.6, 21.6), bx("stone", -1.5, 2.6, 19.5, 1.5, 3, 22.5),
		// башни по углам
		bx("brick", 40, 0, 40, 48, 9, 48), bx("brick", -48, 0, -48, -40, 9, -40),
		// баррикады и ящики
		bx("stone", -30, 0, -20, -24, 1.2, -19.4), bx("stone", 24, 0, 19.4, 30, 1.2, 20),
		bx("stone", -10, 0, 38, -4, 1.2, 38.6), bx("stone", 14, 0, -30, 14.6, 1.2, -24),
		bx("crate", -20, 0, 36, -18, 1.6, 38), bx("crate", -18, 0, 36.4, -16.4, 1.2, 38),
		bx("crate", 18, 0, -40, 20, 1.6, -38), bx("crate", 20, 0, -40, 21.6, 1.2, -38.4),
		bx("crate", 40, 0, -10, 42, 1.6, -8), bx("crate", 40.2, 1.6, -9.8, 41.8, 2.8, -8.2),
		bx("crate", -42, 0, 6, -40, 1.6, 8), bx("crate", -26, 0, 8, -24.4, 1.4, 9.6),
		bx("crate", 8, 0, 36, 10, 2, 38), bx("crate", -14, 0, -6, -12, 1.4, -4),
		// джамп-пад на крышу собора
		bx("pad", -1.5, 0, -29, 1.5, 0.2, -26),
	}
	b[len(b)-1].P = &V3{0, 21, -6}
	// мосты через канал и ступени из воды
	for _, x := range []float64{-30, 0, 30} {
		b = append(b, bx("wood", x-2.5, -0.3, -4, x+2.5, 0.15, 4), bx("trim", x-2.5, 0.15, -4, x-2.3, 1, 4), bx("trim", x+2.3, 0.15, -4, x+2.5, 1, 4))
	}
	for _, x := range []float64{-15, 15, -40, 40} {
		b = append(b, bx("stone", x-1.5, -1, -3, x+1.5, -0.5, -2), bx("stone", x-1.5, -1, 2, x+1.5, -0.5, 3))
	}
	// зубцы на стенах
	for i := -45.0; i <= 45; i += 6 {
		b = append(b, bx("brick", i-1, 8, -49, i+1, 9, -48), bx("brick", i-1, 8, 48, i+1, 9, 49), bx("brick", -49, 8, i-1, -48, 9, i+1), bx("brick", 48, 8, i-1, 49, 9, i+1))
	}
	// дома
	b = append(b, house("plaster", -44, 10, 14, 11, 4.5, "s")...)
	b = append(b, house("plaster", 30, 10, 14, 11, 4.5, "s")...)
	b = append(b, house("brick", -44, -30, 12, 14, 4.5, "e")...)
	b = append(b, house("plaster", 30, -28, 13, 12, 4.5, "w")...)
	b = append(b, house("brick", -7, -44, 14, 10, 9, "n")...)
	// лестницы на крыши и башни
	b = append(b, stairs("stone", 0, 29.7, 1, 11, 13, 4.9, 9, 0.7)...)
	b = append(b, stairs("stone", 2, -28.3, 1, 36, 38, 4.9, 9, 0.7)...)
	b = append(b, stairs("stone", 0, 40, 1, 44, 47, 9, 17, 0.7)...)
	b = append(b, stairs("stone", 0, -40, -1, -47, -44, 9, 17, 0.7)...)
	// рынок и деревья
	for _, x := range []float64{-20, -10, 4, 14} {
		b = append(b, stall(x, -14)...)
	}
	for _, p := range [][2]float64{{-22, 28}, {22, 30}, {-34, -8}, {36, -12}, {24, -42}, {-24, 42}, {-10, -24}, {40, 26}} {
		b = append(b, tree(p[0], p[1])...)
	}

	items := []MItem{
		{K: "w2", P: V3{-37, 0.06, 15}}, {K: "w3", P: V3{0, 0.06, 12}}, {K: "w4", P: V3{-38, 0.06, -23}},
		{K: "w5", P: V3{0, 9.4, -40}}, {K: "w6", P: V3{44, 9, 44}}, {K: "w7", P: V3{-44, 9, -44}},
		{K: "w8", P: V3{-1, 0, -7}}, {K: "w9", P: V3{37, 4.9, 15}},
		{K: "hp", P: V3{-25, 0, 25}}, {K: "hp", P: V3{25, 0, 25}}, {K: "hp", P: V3{-25, 0, -24}},
		{K: "hp", P: V3{24, 0, -16}}, {K: "hp", P: V3{0, 0.06, -39}}, {K: "hp", P: V3{36, 0.06, -22}},
		{K: "ar", P: V3{0, 0.45, 23.5}}, {K: "ar", P: V3{36, 4.9, -22}}, {K: "ar", P: V3{30, 0.15, 0}},
	}
	for i := range items {
		items[i].On = true
	}
	spawns := []V3{{-40, 0, 40}, {0, 0, 44}, {20, 0, 42}, {-40, 0, 6}, {40, 0, -6}, {-20, 0, -30}, {20, 0, -34},
		{-12, 0, 40}, {12, 0, 30}, {-28, 0, -40}, {10, 0, -24}, {0, 0.06, 6}, {-36, 0, -16}, {44, 0, -40}}
	return MapDef{Theme{Name: "Городок", Sky: V3{.74, .85, .95}, Zen: V3{.27, .5, .86}, Fog: .0065, Amb: V3{.5, .52, .58},
		Sun: V3{-.45, -1, -.3}, SunC: V3{.78, .72, .6}, Bound: 48.4, FloorMin: -3, lavaY: -1000}, b, nil, items, spawns}
}
