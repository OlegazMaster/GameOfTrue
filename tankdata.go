// «Танки»: характеристики техники M0–M3, звания и цены.
package main

import "math"

type Hull struct {
	Name  string     `json:"n"`
	Desc  string     `json:"ds"`
	HP    [4]float64 `json:"hp"` // броня
	Speed [4]float64 `json:"sp"` // м/с
	Turn  [4]float64 `json:"tr"` // поворот корпуса, °/с
	Mass  [4]float64 `json:"m"`  // кг
	Power [4]float64 `json:"pw"` // мощность
	HW    float64    `json:"hw"` // полуширина модели
	HL    float64    `json:"hl"` // полудлина
	HH    float64    `json:"hh"` // высота
	Rank  [4]int     `json:"rk"` // звание для M0–M3
	Price [4]int     `json:"pr"` // цена M0 и улучшений
}

type Turret struct {
	Name   string        `json:"n"`
	Desc   string        `json:"ds"`
	Kind   string        `json:"k"`  // shot, rail, flame, freeze, beam, proj, shaft, vulcan
	Rate   [4]float64    `json:"r"`  // мс между выстрелами (у непрерывных — такт 100 мс)
	Dmg    [4][2]float64 `json:"d"`  // урон от–до (у непрерывных — в секунду, у Молота — за дробину)
	RMax   [4]float64    `json:"rx"` // до этой дальности — полный урон
	RMin   [4]float64    `json:"rn"` // с этой дальности — слабый урон
	Weak   float64       `json:"wk"` // доля слабого урона
	Turn   [4]float64    `json:"tr"` // поворот башни, °/с
	TAcc   [4]float64    `json:"ta"` // разгон башни, °/с²
	Crit   [4]float64    `json:"cr,omitempty"`
	CritD  [4]float64    `json:"cd,omitempty"`
	Cost   [4]float64    `json:"cost,omitempty"` // расход энергии (из 1000): в секунду или за выстрел (Рикошет)
	Regen  float64       `json:"rgn,omitempty"`  // восполнение энергии в секунду
	Charge [4]float64    `json:"ch,omitempty"`   // мс: зарядка Рельсы, полное прицеливание Шафта
	Pel    int           `json:"pel,omitempty"`
	Spread [2]float64    `json:"spr,omitempty"` // разброс по горизонтали и вертикали, рад (полуугол)
	Splash float64       `json:"spl,omitempty"` // радиус взрыва, м
	SplW   float64       `json:"splw,omitempty"`
	Speed  [4]float64    `json:"spd,omitempty"` // скорость снаряда, м/с
	Bounce int           `json:"bnc,omitempty"`
	Freeze float64       `json:"frz,omitempty"`
	Snipe  [4][2]float64 `json:"sn,omitempty"`  // урон в прицеле от–до
	Pierce [4]float64    `json:"prc,omitempty"` // урон по следующим танкам за первым, доля
	Heal   [4]float64    `json:"heal,omitempty"`
	Burn   [4]float64    `json:"burn,omitempty"`
	Mag    int           `json:"mag,omitempty"`
	MagR   [4]float64    `json:"magr,omitempty"` // мс перезарядки обоймы
	Spin   [4]float64    `json:"spin,omitempty"` // мс раскрутки Вулкана
	Heat   [4]float64    `json:"heat,omitempty"` // с до перегрева Вулкана
	Slow   [4]float64    `json:"slow,omitempty"` // замедление башни при стрельбе, доля
	Cone   float64       `json:"cn,omitempty"`   // полуугол конуса, рад
	Rank   [4]int        `json:"rk"`
	Price  [4]int        `json:"pr"`
}

// звания по возрастанию
const (
	rRecruit = iota
	rPrivate
	rGefreiter
	rCorporal
	rMasterCorporal
	rSergeant
	rStaffSergeant
	rMasterSergeant
	rFirstSergeant
	rSergeantMajor
	rWO1
	rWO2
	rWO3
	rWO4
	rWO5
	rThirdLt
	rSecondLt
	rFirstLt
	rCaptain
	rMajor
	rLtColonel
	rColonel
	rBrigadier
	rMajorGeneral
	rLtGeneral
	rGeneral
	rMarshal
	rFieldMarshal
	rCommander
	rGeneralissimo
)

const deg = math.Pi / 180

// Порядок корпусов и пушек не менять: на индексы ссылаются профили игроков.
var hulls = []Hull{
	{Name: "Оса", Desc: "Самый быстрый и лёгкий, но брони мало",
		HP: [4]float64{65, 105, 140, 180}, Speed: [4]float64{10, 11, 12, 13}, Turn: [4]float64{75.1, 100.8, 123.8, 150},
		Mass: [4]float64{1100, 1483, 1817, 2200}, Power: [4]float64{820, 990, 1130, 1300}, HW: .95, HL: 1.55, HH: .8,
		Rank: [4]int{rPrivate, rFirstSergeant, rThirdLt, rMajorGeneral}, Price: [4]int{200, 7650, 62450, 172600}},
	{Name: "Шершень", Desc: "Низкий и юркий, крепче Осы",
		HP: [4]float64{98, 140, 181, 210}, Speed: [4]float64{9.5, 10.4, 11.3, 12}, Turn: [4]float64{80.2, 98.5, 116.9, 130},
		Mass: [4]float64{1409, 1774, 2139, 2400}, Power: [4]float64{950, 1110, 1280, 1400}, HW: 1.02, HL: 1.7, HH: .74,
		Rank: [4]int{rMasterCorporal, rWO2, rCaptain, rMarshal}, Price: [4]int{500, 21000, 86600, 215500}},
	{Name: "Хантер", Desc: "Средний: золотая середина скорости и брони",
		HP: [4]float64{105, 160, 215, 285}, Speed: [4]float64{7.5, 8.3, 9, 10}, Turn: [4]float64{57.9, 83.1, 107.7, 140},
		Mass: [4]float64{1700, 2096, 2491, 3000}, Power: [4]float64{850, 1020, 1190, 1400}, HW: 1.15, HL: 1.85, HH: .92,
		Rank: [4]int{rRecruit, rMasterSergeant, rWO5, rBrigadier}, Price: [4]int{0, 3200, 54400, 158300}},
	{Name: "Викинг", Desc: "Средний, низкий и манёвренный, броня выше Хантера",
		HP: [4]float64{163, 226, 288, 315}, Speed: [4]float64{7.5, 8.1, 8.7, 9}, Turn: [4]float64{72.8, 88.2, 103.7, 110},
		Mass: [4]float64{2039, 2435, 2830, 3000}, Power: [4]float64{1040, 1230, 1420, 1500}, HW: 1.2, HL: 1.95, HH: .84,
		Rank: [4]int{rStaffSergeant, rWO4, rLtColonel, rCommander}, Price: [4]int{700, 29900, 102700, 244200}},
	{Name: "Диктатор", Desc: "Высокий средне-тяжёлый: стреляет поверх укрытий",
		HP: [4]float64{145, 215, 285, 345}, Speed: [4]float64{6.7, 7.2, 7.6, 8}, Turn: [4]float64{77.9, 96.3, 114.6, 130},
		Mass: [4]float64{2170, 2565, 2961, 3300}, Power: [4]float64{980, 1160, 1340, 1500}, HW: 1.25, HL: 2.05, HH: 1.22,
		Rank: [4]int{rCorporal, rWO1, rFirstLt, rGeneral}, Price: [4]int{400, 16550, 78550, 201200}},
	{Name: "Титан", Desc: "Тяжёлый и медленный, держит много урона",
		HP: [4]float64{169, 253, 336, 420}, Speed: [4]float64{5.1, 5.4, 5.7, 6}, Turn: [4]float64{46.4, 60.7, 75.6, 90},
		Mass: [4]float64{3174, 3783, 4391, 5000}, Power: [4]float64{1050, 1240, 1420, 1600}, HW: 1.4, HL: 2.15, HH: 1.05,
		Rank: [4]int{rGefreiter, rSergeantMajor, rSecondLt, rLtGeneral}, Price: [4]int{300, 12100, 70500, 187000}},
	{Name: "Мамонт", Desc: "Самый тяжёлый: огромный запас брони, еле ползёт",
		HP: [4]float64{243, 343, 443, 500}, Speed: [4]float64{4.2, 4.5, 4.8, 5}, Turn: [4]float64{47, 59.6, 72.8, 80},
		Mass: [4]float64{3935, 4543, 5152, 5500}, Power: [4]float64{1010, 1200, 1390, 1500}, HW: 1.55, HL: 2.35, HH: 1.15,
		Rank: [4]int{rSergeant, rWO3, rMajor, rFieldMarshal}, Price: [4]int{600, 25450, 94650, 229900}},
}

var turrets = []Turret{
	{Name: "Смоки", Desc: "Пушка средней дальности, бывают критические попадания", Kind: "shot",
		Rate: [4]float64{2000, 1787, 1574, 1300}, Dmg: [4][2]float64{{16, 20}, {25, 31}, {33, 42}, {44, 56}},
		RMax: [4]float64{40, 46.09, 52.17, 60}, RMin: [4]float64{120, 138.26, 156.52, 180}, Weak: .1,
		Turn: [4]float64{57.3, 77.3, 96.8, 122.6}, TAcc: [4]float64{114.6, 140.4, 166.2, 199.5},
		Crit: [4]float64{.05, .10, .14, .20}, CritD: [4]float64{36, 56, 75, 100},
		Rank: [4]int{rRecruit, rMasterSergeant, rWO5, rBrigadier}, Price: [4]int{0, 7100, 61400, 177700}},
	{Name: "Огнемёт", Desc: "Жжёт всех в конусе перед собой и поджигает", Kind: "flame",
		Rate: [4]float64{100, 100, 100, 100}, Dmg: [4][2]float64{{32, 32}, {44, 44}, {56, 56}, {72, 72}},
		RMax: [4]float64{5, 5, 5, 5}, RMin: [4]float64{17, 19.4, 21.8, 25}, Weak: .5,
		Turn: [4]float64{75.6, 103.1, 130.6, 166.2}, Cost: [4]float64{166, 166, 166, 166}, Regen: 166,
		Burn: [4]float64{6.2, 13.7, 21.6, 31.2}, Cone: 20 * deg,
		Rank: [4]int{rRecruit, rMasterSergeant, rWO5, rBrigadier}, Price: [4]int{150, 7100, 61400, 177700}},
	{Name: "Твинс", Desc: "Два ствола, быстрые плазменные заряды очередью", Kind: "proj",
		Rate: [4]float64{250, 250, 250, 250}, Dmg: [4][2]float64{{6.5, 7.6}, {9.8, 11.9}, {13.2, 16.1}, {17, 21}},
		RMax: [4]float64{15, 15, 15, 15}, RMin: [4]float64{60.87, 66.96, 73.04, 80}, Weak: .5,
		Turn: [4]float64{77.3, 95.7, 113.4, 134.7}, Speed: [4]float64{60.87, 66.96, 73.04, 80},
		Rank: [4]int{rPrivate, rFirstSergeant, rThirdLt, rMajorGeneral}, Price: [4]int{350, 12350, 70300, 188500}},
	{Name: "Рельса", Desc: "Мощный выстрел после зарядки, пробивает танки насквозь", Kind: "rail",
		Rate: [4]float64{7418, 6778, 6140, 5500}, Dmg: [4][2]float64{{50, 81}, {78, 120}, {105, 160}, {133, 199}},
		Turn: [4]float64{45.3, 62.5, 80.2, 97.4}, Charge: [4]float64{1188, 1145, 1103, 1060},
		Pierce: [4]float64{.18, .45, .73, 1}, Weak: 1,
		Rank: [4]int{rGefreiter, rSergeantMajor, rSecondLt, rLtGeneral}, Price: [4]int{800, 17600, 79200, 199300}},
	{Name: "Изида", Desc: "Луч: высасывает здоровье врагов, лечит союзников", Kind: "beam",
		Rate: [4]float64{100, 100, 100, 100}, Dmg: [4][2]float64{{30, 30}, {42, 42}, {55, 55}, {66, 66}},
		RMax: [4]float64{15.91, 18.04, 20.17, 22}, RMin: [4]float64{15.91, 18.04, 20.17, 22}, Weak: 1,
		Heal: [4]float64{15, 21, 27, 33}, Turn: [4]float64{82.5, 100.8, 119.7, 134.7},
		Cost: [4]float64{142, 142, 142, 142}, Regen: 83.3, Cone: 15 * deg,
		Rank: [4]int{rCorporal, rWO1, rFirstLt, rGeneral}, Price: [4]int{1250, 22850, 88100, 221000}},
	{Name: "Молот", Desc: "Дробовик: 3 выстрела в обойме, страшен вблизи", Kind: "shot",
		Rate: [4]float64{2260, 2100, 1950, 1800}, Dmg: [4][2]float64{{2.1, 2.1}, {3.17, 3.17}, {4.23, 4.23}, {5.3, 5.3}},
		RMax: [4]float64{46.7, 52.8, 58.9, 65}, RMin: [4]float64{51.74, 57.83, 63.91, 70}, Weak: .5,
		Turn: [4]float64{75.1, 95.7, 116.9, 137.5}, Pel: 21, Spread: [2]float64{3.5 * deg, 2.5 * deg},
		Mag: 3, MagR: [4]float64{6360, 5500, 4650, 3800},
		Rank: [4]int{rGefreiter, rSergeantMajor, rSecondLt, rLtGeneral}, Price: [4]int{800, 17600, 79200, 210100}},
	{Name: "Тандер", Desc: "Снаряд со взрывом: задевает всех рядом с точкой попадания", Kind: "shot",
		Rate: [4]float64{3326, 3022, 2717, 2500}, Dmg: [4][2]float64{{32, 55}, {49, 75}, {66, 95}, {78, 110}},
		RMax: [4]float64{53.5, 59.6, 65.7, 70}, RMin: [4]float64{106.96, 119.13, 131.3, 140}, Weak: .5,
		Turn: [4]float64{63, 85.9, 108.9, 124.9}, TAcc: [4]float64{98, 121, 143.9, 160.4}, Splash: 12, SplW: .25,
		Rank: [4]int{rMasterCorporal, rWO2, rCaptain, rMarshal}, Price: [4]int{1450, 28100, 97000, 242500}},
	{Name: "Фриз", Desc: "Морозит врагов в конусе: они замедляются", Kind: "freeze",
		Rate: [4]float64{100, 100, 100, 100}, Dmg: [4][2]float64{{39, 39}, {54, 54}, {69, 69}, {80, 80}},
		RMax: [4]float64{5, 5, 5, 5}, RMin: [4]float64{18.4, 20.8, 23.6, 25}, Weak: .3,
		Turn: [4]float64{108.3, 140.9, 173, 196}, Cost: [4]float64{111, 111, 111, 111}, Regen: 111,
		Freeze: .6, Cone: 20 * deg,
		Rank: [4]int{rMasterCorporal, rWO2, rCaptain, rMarshal}, Price: [4]int{1450, 28100, 97100, 253300}},
	{Name: "Рикошет", Desc: "Плазменные шары отскакивают от стен", Kind: "proj",
		Rate: [4]float64{590, 540, 500, 470}, Dmg: [4][2]float64{{16, 19}, {22, 26}, {28, 34}, {32, 38}},
		RMax: [4]float64{64.35, 70.43, 76.5, 80}, RMin: [4]float64{76.52, 85.65, 94.78, 100}, Weak: .5,
		Turn: [4]float64{91.3, 110.2, 129, 139.8}, Speed: [4]float64{88.7, 100.87, 113.04, 120}, Bounce: 4,
		Cost: [4]float64{137, 130, 123, 119}, Regen: 100,
		Rank: [4]int{rSergeant, rWO3, rMajor, rFieldMarshal}, Price: [4]int{1700, 33350, 105900, 264200}},
	{Name: "Шафт", Desc: "Снайпер: удерживайте огонь — прицел, чем дольше, тем сильнее", Kind: "shaft",
		Rate: [4]float64{3740, 3430, 3130, 3000}, Dmg: [4][2]float64{{44, 54}, {63, 76}, {82, 97}, {90, 106}},
		Snipe: [4][2]float64{{54, 153}, {76, 214}, {97, 276}, {106, 302}}, Weak: 1,
		Turn: [4]float64{67, 86.5, 106.6, 114.1}, Charge: [4]float64{2500, 2500, 2500, 2500},
		Rank: [4]int{rStaffSergeant, rWO4, rLtColonel, rCommander}, Price: [4]int{1900, 38600, 114800, 275000}},
	{Name: "Вулкан", Desc: "Пулемёт: раскручивается и стреляет без остановки, но перегревается", Kind: "vulcan",
		Rate: [4]float64{100, 100, 100, 100}, Dmg: [4][2]float64{{27, 27}, {38.5, 38.5}, {50.1, 50.1}, {60, 60}},
		RMax: [4]float64{32.61, 38.7, 44.78, 50}, RMin: [4]float64{130.4, 154.8, 179.1, 200}, Weak: .25,
		Turn: [4]float64{76.2, 93.4, 111.2, 126}, TAcc: [4]float64{107.77, 118.09, 128.41, 137.57},
		Spin: [4]float64{1700, 1450, 1210, 1000}, Heat: [4]float64{4.39, 5.3, 6.22, 7}, Slow: [4]float64{.56, .47, .38, .30},
		Rank: [4]int{rCorporal, rWO1, rFirstLt, rGeneral}, Price: [4]int{1250, 22850, 88100, 231800}},
}

const nPaints = 16

// falloff: доля урона на расстоянии d.
func (t *Turret) falloff(m int, d float64) float64 {
	if t.Weak >= 1 || t.RMin[m] <= 0 {
		return 1
	}
	a, b := t.RMax[m], t.RMin[m]
	if d <= a {
		return 1
	}
	if d >= b || b <= a {
		return t.Weak
	}
	return 1 - (1-t.Weak)*(d-a)/(b-a)
}

// reach: дальность, дальше которой урона нет (для лучей — бесконечность).
func (t *Turret) reach(m int) float64 {
	switch t.Kind {
	case "flame", "freeze", "beam":
		return t.RMin[m]
	case "proj":
		return t.RMin[m] * 1.3
	}
	return 300
}
