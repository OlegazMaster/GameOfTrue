package main

import (
	"testing"
	"time"
)

func fakeP(r *TRoom, id int, uid string, team int) *TPlayer {
	p := &TPlayer{id: id, uid: uid, name: uid, out: make(chan []byte, 8192), team: team}
	r.players[id] = p
	r.respawn(p)
	return p
}

func eqT(t *testing.T, p *TPlayer, tur, m int) {
	p.tur, p.um, p.last, p.mag = tur, m, time.Time{}, turrets[tur].Mag
}

func TestTanksFlow(t *testing.T) {
	r := trooms["sand-tdm"]
	r.mu.Lock()
	defer r.mu.Unlock()
	a, b := fakeP(r, 1, "ua", 1), fakeP(r, 2, "ub", 2)
	a.prot, b.prot = false, false
	if a.hull != 2 || a.maxHP() != 105 {
		t.Fatal("новичок не на Хантере M0", a.hull, a.maxHP())
	}
	shoot := func() { r.fire(a, a.pos.add(V3{0, 1.3, 0}), V3{0, 0, 1}, 0) }
	// Смоки M0: 16–20 урона (или крит 36) в упор
	a.pos, a.yaw, a.tyaw = V3{0, 0, -34}, 3.14159, 3.14159
	b.pos, b.yaw = V3{0, 0, -20}, 0
	eqT(t, a, 0, 0)
	shoot()
	if d := b.maxHP() - b.hp; d < 16 || d > 36.1 {
		t.Fatal("смоки урон", d)
	}
	// Тандер: брызги задевают второго врага рядом
	c := fakeP(r, 3, "uc", 2)
	c.prot = false
	c.pos = V3{3.5, 0, -20}
	eqT(t, a, 6, 3)
	h0 := c.hp
	shoot()
	if c.hp >= h0 {
		t.Fatal("тандер без брызг", c.hp)
	}
	// Твинс: снаряд долетает
	eqT(t, a, 2, 0)
	b.hp, b.alive = b.maxHP(), true
	shoot()
	for i := 0; i < 20; i++ {
		r.step(time.Now())
	}
	if b.hp >= b.maxHP() {
		t.Fatal("твинс не попал", len(r.projs))
	}
	// Фриз замораживает
	eqT(t, a, 7, 0)
	a.pos = V3{0, 0, -26}
	shoot()
	if b.frz <= 0 {
		t.Fatal("нет заморозки")
	}
	// Шафт M3 в прицеле: 106–302
	eqT(t, a, 9, 3)
	b.hm, b.hp, b.alive = 3, 285, true
	a.scopeAt = time.Now().Add(-3200 * time.Millisecond)
	shoot()
	if d := 285 - b.hp; b.alive && d < 280 {
		t.Fatal("шафт слабый", d)
	}
	// Молот: три выстрела, затем перезарядка обоймы
	eqT(t, a, 5, 0)
	b.hp, b.alive, b.hm = 105, true, 0
	for i := 0; i < 4; i++ {
		a.last = time.Time{}
		shoot()
	}
	if a.mag != 0 || a.magAt.IsZero() {
		t.Fatal("обойма Молота", a.mag)
	}
	// Вулкан стреляет только после раскрутки
	eqT(t, a, 10, 0)
	b.hp, b.alive = 105, true
	shoot()
	if b.hp < 105 {
		t.Fatal("вулкан без раскрутки")
	}
	a.spinAt = time.Now().Add(-2 * time.Second)
	shoot()
	if b.hp >= 105 {
		t.Fatal("вулкан не стреляет")
	}
	// Двойной урон и двойная броня
	tp.Lock()
	profOf("ua").Sup = [5]int{1, 1, 1, 1, 3}
	tp.Unlock()
	r.useSupply(a, 2)
	if !a.ddUntil.After(time.Now()) {
		t.Fatal("двойной урон не включился")
	}
	r.useSupply(a, 2)
	tp.Lock()
	left := profOf("ua").Sup[2]
	tp.Unlock()
	if left != 0 {
		t.Fatal("припас списан неверно", left)
	}
	// Мина под врагом
	a.pos = V3{10, 0, 10}
	r.useSupply(a, 4)
	if len(r.mines) != 1 {
		t.Fatal("мина не поставлена")
	}
	r.mines[0].armAt = time.Now().Add(-time.Second)
	b.pos, b.hp, b.alive, b.prot = V3{10.5, 0, 10}, 105, true, false
	r.stepMines(time.Now())
	if len(r.mines) != 0 || b.hp > 105-60 {
		t.Fatal("мина не сработала", b.hp)
	}
	// Награды в конце битвы
	a.score, b.score, c.score = 30, 10, 0
	r.fund = 100
	tp.Lock()
	cr0 := profOf("ua").Cr
	tp.Unlock()
	r.teamK = [3]int{0, 5, 1}
	r.finish()
	tp.Lock()
	got := profOf("ua").Cr - cr0
	tp.Unlock()
	if got < 70 {
		t.Fatal("мало кристаллов победителю", got)
	}
	// Покупка
	tp.Lock()
	profOf("ua").XP, profOf("ua").Cr = 0, 1000
	tp.Unlock()
	if e, _ := buy("ua", "h", 6, 1); e == "" {
		t.Fatal("Мамонт куплен без звания")
	}
	if e, _ := buy("ua", "c", 1, 1); e != "" {
		t.Fatal(e)
	}
	if e, _ := buy("ua", "s", 1, 10); e != "" {
		t.Fatal(e)
	}
	if r := award("ua", 500, 0); r != 2 {
		t.Fatal("нет звания Ефрейтор", r)
	}
}

func TestCTF(t *testing.T) {
	r := trooms["sand-ctf"]
	r.mu.Lock()
	defer r.mu.Unlock()
	a, b := fakeP(r, 11, "fa", 1), fakeP(r, 12, "fb", 2)
	a.prot, b.prot = false, false
	b.pos = V3{-30, 0, 0}
	ef := r.flags[2]
	a.pos = ef.home
	r.stepFlags(time.Now())
	if ef.carrier != a.id {
		t.Fatal("флаг не взят")
	}
	// убили — флаг падает, союзник синих возвращает
	r.kill(a, b, "Смоки")
	if ef.carrier != 0 || ef.atHome {
		t.Fatal("флаг не упал")
	}
	b.pos = ef.pos
	r.stepFlags(time.Now())
	if !ef.atHome {
		t.Fatal("флаг не вернулся")
	}
	// захват
	r.respawn(a)
	a.prot = false
	b.pos = V3{30, 0, 30}
	a.pos = ef.home
	r.stepFlags(time.Now())
	a.pos = r.flags[1].home
	r.stepFlags(time.Now())
	if r.teamK[1] != 1 {
		t.Fatal("захват не засчитан", r.teamK)
	}
}

func TestCP(t *testing.T) {
	r := trooms["kungur-cp"]
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.cps) != 3 {
		t.Fatal("точек", len(r.cps))
	}
	a := fakeP(r, 21, "ca", 2)
	a.pos = r.cps[1].pos
	for i := 0; i < 220; i++ {
		r.stepCP(time.Now())
	}
	if r.cps[1].owner != 2 {
		t.Fatal("точка не захвачена", r.cps[1].prog)
	}
	for i := 0; i < 20*8; i++ {
		r.stepCP(time.Now())
	}
	if r.teamK[2] < 1 {
		t.Fatal("очки за точку не идут", r.teamPts)
	}
}

func TestZones(t *testing.T) {
	for _, k := range tmapOrder {
		m := trooms[k+"-ctf"].m
		if len(m.drops) < 15 || len(m.cps) != 3 || m.flags[1] == m.flags[2] {
			t.Fatal(k, len(m.drops), len(m.cps), m.flags)
		}
		t.Log(k, "флаги", m.flags[1], m.flags[2], "точки", m.cps, "ящиков", len(m.drops))
	}
}
