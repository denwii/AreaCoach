package aim

import (
	"math"
	"math/rand"
	"testing"

	"areacoach/internal/geom"
	"areacoach/internal/osu"
	"areacoach/internal/otd"
)

var display = otd.Rect{W: 1920, H: 1080, X: 960, Y: 540}

func setupWith(a otd.Rect) geom.Setup {
	return geom.Setup{TabletW: 152, TabletH: 95, Area: a, Display: display, GameW: 1920, GameH: 1080, StretchX: 1, StretchY: 1}
}

// play simulates presses: the hand puts the pen where the circle would be
// with the area hand, off by noise (mm) and bias (mm); the cursor goes where
// the area used puts it.
func play(hand, used otd.Rect, noise float64, bias [2]float64, n int, seed int64) []Click {
	rng := rand.New(rand.NewSource(seed))
	mh, mu := setupWith(hand).Mapper(), setupWith(used).Mapper()
	const r = 36.48
	out := make([]Click, 0, n)
	for i := 0; i < n; i++ {
		t := osu.Vec{X: 30 + rng.Float64()*452, Y: 30 + rng.Float64()*324}
		px, py := mh.Point(t.X, t.Y)
		px += rng.NormFloat64()*noise + bias[0]
		py += rng.NormFloat64()*noise + bias[1]
		cx, cy := mu.Osu(px, py)
		c := Click{Obj: i, T: float64(i) * 300, Target: t, Radius: r, Pressed: true, Cursor: osu.Vec{X: cx, Y: cy}}
		c.Hit = c.Dist() <= 1
		out = append(out, c)
	}
	return out
}

func fitOf(t *testing.T, cs []Click, used otd.Rect, lim Limits) Result {
	t.Helper()
	s := Samples(cs, setupWith(used), func(int) otd.Rect { return used })
	res, ok := Fit(s, lim, used, true)
	if !ok {
		t.Fatal("no fit")
	}
	return res
}

func limits(ratio float64) Limits {
	return Limits{Display: display, Ratio: ratio, TabletW: 152, TabletH: 95}
}

func TestMapperRoundTrip(t *testing.T) {
	m := setupWith(otd.Rect{W: 80, H: 45, X: 76, Y: 47.5, Rot: 7}).Mapper()
	x, y := m.Point(100, 200)
	ox, oy := m.Osu(x, y)
	if math.Abs(ox-100) > 1e-9 || math.Abs(oy-200) > 1e-9 {
		t.Fatalf("round trip: %v %v", ox, oy)
	}
}

func TestFitFindsTheHandArea(t *testing.T) {
	hand := otd.Rect{W: 80, H: 45, X: 76, Y: 47.5}
	used := otd.Rect{W: 70, H: 39.375, X: 73, Y: 49}
	cs := play(hand, used, 1.2, [2]float64{}, 600, 1)
	res := fitOf(t, cs, used, limits(0.5625))
	a := res.Area
	if math.Abs(a.W-80) > 1 || math.Abs(a.X-76) > 0.4 || math.Abs(a.Y-47.5) > 0.4 {
		t.Fatalf("got %+v", a)
	}
	if math.Abs(a.H-a.W*0.5625) > 0.002 {
		t.Fatalf("ratio not kept: %+v", a)
	}
	if res.After.Misses >= res.Before.Misses {
		t.Fatalf("misses %d -> %d", res.Before.Misses, res.After.Misses)
	}
	t.Logf("misses %d -> %d, area %+v, se %v", res.Before.Misses, res.After.Misses, a, res.SE)
}

// With the area that matches the hand, the fit must stay there on average:
// it does not drift towards bigger or smaller areas.
func TestFitDoesNotDrift(t *testing.T) {
	hand := otd.Rect{W: 80, H: 45, X: 76, Y: 47.5}
	sum, sumX := 0.0, 0.0
	const n = 12
	for k := 0; k < n; k++ {
		cs := play(hand, hand, 1.4, [2]float64{}, 400, int64(10+k))
		res := fitOf(t, cs, hand, limits(0.5625))
		sum += res.Area.W
		sumX += res.Area.X
	}
	if mw := sum / n; math.Abs(mw-80) > 0.35 {
		t.Fatalf("average width %.3f, want 80", mw)
	}
	if mx := sumX / n; math.Abs(mx-76) > 0.15 {
		t.Fatalf("average X %.3f, want 76", mx)
	}
}

// Pressing always a little to the right of the circles: the area moves so
// that the presses land on the centres.
func TestFitFollowsBias(t *testing.T) {
	hand := otd.Rect{W: 80, H: 45, X: 76, Y: 47.5}
	cs := play(hand, hand, 1.0, [2]float64{1.2, -0.6}, 500, 3)
	res := fitOf(t, cs, hand, limits(0.5625))
	if math.Abs(res.Area.X-77.2) > 0.3 || math.Abs(res.Area.Y-46.9) > 0.3 {
		t.Fatalf("got %+v", res.Area)
	}
	if res.Before.OffX < 0.15 || math.Abs(res.After.OffX) > 0.08 {
		t.Fatalf("offsets %+v -> %+v", res.Before, res.After)
	}
}

func TestFitKeepsTheRange(t *testing.T) {
	hand := otd.Rect{W: 80, H: 45, X: 76, Y: 47.5}
	used := otd.Rect{W: 70, H: 39.375, X: 76, Y: 47.5}
	cs := play(hand, used, 1.0, [2]float64{}, 400, 4)
	lim := limits(0.5625)
	lim.MaxW = 75
	res := fitOf(t, cs, used, lim)
	if res.Area.W != 75 || !res.AtMax {
		t.Fatalf("got %+v", res)
	}
	lim = limits(0.5625)
	lim.MinW = 85
	res = fitOf(t, cs, used, lim)
	if res.Area.W != 85 || !res.AtMin {
		t.Fatalf("got %+v", res)
	}
}

func TestFitStaysOnTheTablet(t *testing.T) {
	hand := otd.Rect{W: 80, H: 45, X: 135, Y: 47.5} // past the right edge
	used := otd.Rect{W: 80, H: 45, X: 110, Y: 47.5}
	cs := play(hand, used, 1.0, [2]float64{}, 400, 5)
	res := fitOf(t, cs, used, limits(0.5625))
	if !geom.FitsTablet(res.Area, 152, 95) {
		t.Fatalf("off the tablet: %+v", res.Area)
	}
	if res.Area.X < 111.5 {
		t.Fatalf("should move right as far as it can: %+v", res.Area)
	}
}

func TestJudgeNotelock(t *testing.T) {
	bm := &osu.Beatmap{}
	_ = bm
	objs := []osu.HitObject{
		{Time: 1000, Pos: osu.Vec{X: 100, Y: 100}, Kind: osu.KindCircle},
		{Time: 1300, Pos: osu.Vec{X: 300, Y: 100}, Kind: osu.KindCircle},
		{Time: 1600, Pos: osu.Vec{X: 300, Y: 300}, Kind: osu.KindCircle},
	}
	d := osu.Difficulty{Radius: 30, Window50: 150}
	presses := []Press{
		{T: 1010, X: 110, Y: 100}, // hit
		{T: 1290, X: 350, Y: 100}, // aim miss (50 px away)
		{T: 1600, X: 300, Y: 290}, // hit
	}
	cs := Judge(objs, d, presses)
	if len(cs) != 3 || !cs[0].Hit || cs[1].Hit || !cs[1].Pressed || !cs[2].Hit {
		t.Fatalf("got %+v", cs)
	}
	if math.Abs(cs[1].Dist()-50.0/30) > 1e-9 {
		t.Fatalf("dist %v", cs[1].Dist())
	}
}

// A map with all its notes in one small part of the screen can't tell the
// width: the uncertainty says so.
func TestFitUncertaintyOnClusteredNotes(t *testing.T) {
	hand := otd.Rect{W: 80, H: 45, X: 76, Y: 47.5}
	rng := rand.New(rand.NewSource(9))
	mh := setupWith(hand).Mapper()
	var cs []Click
	for i := 0; i < 300; i++ {
		tg := osu.Vec{X: 256 + rng.NormFloat64()*15, Y: 192 + rng.NormFloat64()*15}
		px, py := mh.Point(tg.X, tg.Y)
		cx, cy := mh.Osu(px+rng.NormFloat64()*1.2, py+rng.NormFloat64()*1.2)
		c := Click{Obj: i, Target: tg, Radius: 36.48, Pressed: true, Cursor: osu.Vec{X: cx, Y: cy}}
		cs = append(cs, c)
	}
	res := fitOf(t, cs, hand, limits(0.5625))
	wide := fitOf(t, play(hand, hand, 1.2, [2]float64{}, 300, 9), hand, limits(0.5625))
	t.Logf("clustered se %v, spread se %v", res.SE, wide.SE)
	if res.SE[0] < 3*wide.SE[0] || res.SE[0] < 1.5 {
		t.Fatalf("clustered notes should leave the width unsure: %v vs %v", res.SE, wide.SE)
	}
}

// With the area that fits, the advice never has more misses than it.
func TestFitNeverMoreMisses(t *testing.T) {
	hand := otd.Rect{W: 80, H: 45, X: 76, Y: 47.5}
	for seed := int64(20); seed < 32; seed++ {
		cs := play(hand, hand, 1.0, [2]float64{}, 250, seed)
		res := fitOf(t, cs, hand, limits(0.5625))
		if res.After.Misses > res.Before.Misses {
			t.Fatalf("seed %d: misses %d -> %d", seed, res.Before.Misses, res.After.Misses)
		}
	}
}
