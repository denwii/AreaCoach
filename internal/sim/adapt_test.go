package sim

import (
	"fmt"
	"math"
	"math/rand"
	"path/filepath"
	"testing"
	"time"

	"areacoach/internal/analysis"
	"areacoach/internal/osu"
	"areacoach/internal/otd"
)

// adaptPlayer is a player whose hand is h.
func adaptPlayer(seed int64, h *Hand) *Player {
	return &Player{
		Offset: [2]float64{0.25, -0.15},
		APar:   0.55, BPar: 0.02, CPar: 50, P: 2,
		APerp: 0.45, BPerp: 0.012, CPerp: 27,
		TimingSD: 12, MissRate: 0.005,
		Rng:  rand.New(rand.NewSource(seed)),
		Hand: h,
	}
}

// The map measure finds the over/undershoot and the offset of the hand.
func TestAdaptMeasure(t *testing.T) {
	maps := liveMaps(t)
	area := otd.Rect{W: 100, H: 56.25, X: 76, Y: 47.5}
	h := &Hand{Natural: otd.Rect{W: 70, H: 47, X: 80, Y: 46}, Residual: 1, CenterR: 1}
	var gxs, gys, dxs, dys []float64
	for i, bm := range maps {
		setup := testSetup()
		setup.Area = area
		pl := adaptPlayer(int64(40+i), h)
		frames := pl.Play(bm, 0, setup)
		objs, d := bm.Objects(0)
		hits, _ := analysis.Judge(objs, d, frames)
		p := &analysis.PlayData{ID: 1, Time: time.Now(), Setup: setup, Rate: 1, Hits: hits, Duration: bm.LengthMs()}
		for _, w := range analysis.MapWindows(p) {
			gxs = append(gxs, w.GX)
			gys = append(gys, w.GY)
			dxs = append(dxs, w.DX)
			dys = append(dys, w.DY)
			t.Logf("map %d: %d notes %.0f s G %.3f±%.3f GX %.3f±%.3f GY %.3f±%.3f DX %.2f±%.2f DY %.2f±%.2f", i, w.Notes, w.Sec, w.G, w.SG, w.GX, w.SGX, w.GY, w.SGY, w.DX, w.SDX, w.DY, w.SDY)
		}
	}
	if len(gxs) == 0 {
		t.Fatal("no stretch measured")
	}
	wantGX, wantGY := math.Log(70.0/100), math.Log(47/56.25)
	if g := mean(gxs); math.Abs(g-wantGX) > 0.06 {
		t.Errorf("GX %.3f, want %.3f", g, wantGX)
	}
	if g := mean(gys); math.Abs(g-wantGY) > 0.06 {
		t.Errorf("GY %.3f, want %.3f", g, wantGY)
	}
	if d := mean(dxs); math.Abs(d-4.25) > 0.6 {
		t.Errorf("DX %.2f, want 4.25", d)
	}
	if d := mean(dys); math.Abs(d+1.65) > 0.6 {
		t.Errorf("DY %.2f, want -1.65", d)
	}
}

func mean(v []float64) float64 {
	s := 0.0
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

type loopCase struct {
	name     string
	locked   bool
	nat      otd.Rect
	anchor   otd.Rect
	start    otd.Rect // the area of the first map (default: all of the tablet)
	residual float64
	plays    int
	moveAt   int     // play from which the wrist is moved
	moveX    float64 // mm
	tol      float64 // share the width may be off (0: 5%)
	firstMax float64 // the area of the second map must be at most this wide
	absolute float64 // share of the mismatch shown by position (see Hand)
	// the hand gets fully used to wherever the area is (its landing offset
	// does not depend on where the area is), with this habit (mm)
	anyPlace bool
	habit    [2]float64
	fixPos   bool    // the place is kept still where the area was before
	hold     bool    // no change after a map with clean aim
	missA    float64 // > 0: the expected misses by width come from replays of a hand this shaky (mm)
	local    bool    // no replays: the expected misses come from the maps of the diagnosis
	// habits that do not follow the area (see Player)
	lazy    float64
	habitG  [2]float64
	shape   float64 // > 0: locked to this height / width instead of the screen's
	shake   float64 // > 0: shakier on narrower areas than the natural one (see Player.ShakeExp)
	fixSize bool    // the size stays as it starts, only the place adapts
	handA   float64 // > 0: the steadiness noise of the hand (mm), instead of the usual
	// the hand turns around a pivot this far from the natural centre (mm),
	// and aims straight this far to the right of it
	pivot      [2]float64
	kappa      float64
	straightAt float64
	// the widths allowed
	minW, maxW float64
	corner     [2]float64 // see Player.Corner
	// the hand is steadiest with the area centred here, less so away (see
	// Player.PlaceRef, PlaceK)
	steadyAt [2]float64
	steadyK  float64
}

type loopResult struct {
	fit    analysis.AdaptFit
	areas  []otd.Rect // the area of each map
	bests  []otd.Rect // the best area found after each map
	scores []float64  // how close to a perfect area after each map (%)
	// the side each decision could not try, and the limit in the way
	blocked []string
	limits  []float64
}

// runLoop plays maps one after the other with the diagnosis as in the
// program: every map with one area all the way through, measured on its
// replay, then the area of the next map.
func runLoop(t *testing.T, c loopCase) loopResult {
	maps := liveMaps(t)
	ratio := 1080.0 / 1920
	if c.shape > 0 {
		ratio = c.shape
	}
	o := analysis.AdaptOpts{Locked: c.locked, Ratio: ratio, TabletW: 152, TabletH: 95, Anchor: c.anchor, FixPos: c.fixPos, PosX: c.anchor.X, PosY: c.anchor.Y,
		MinW: c.minW, MaxW: c.maxW}
	if c.fixSize {
		o.FixSize, o.SizeW, o.SizeH = true, c.start.W, c.start.H
	}
	if c.missA > 0 {
		o.Miss = missModelFor(t, c.nat, c.missA)
		if !o.Miss.OK() {
			t.Fatal("no miss model")
		}
	}
	su := testSetup()
	cur := c.start
	if !cur.Valid() {
		cur = analysis.AdaptStart(o)
	}
	hand := &Hand{Natural: c.nat, Residual: c.residual, CenterR: c.residual, Fade: 2500, Absolute: c.absolute}
	if c.anyPlace {
		hand.CenterR = 0
	}
	run := analysis.NewAdaptRun(o, cur, nil)
	run.HoldClean = c.hold
	wall := time.Date(2026, 10, 1, 18, 0, 0, 0, time.UTC)
	var res loopResult
	var plays []*analysis.PlayData
	for play := 0; play < c.plays; play++ {
		if c.moveAt > 0 && play == c.moveAt {
			hand.Natural.X += c.moveX
		}
		bm := maps[play%len(maps)]
		setup := su
		setup.Area = cur
		pl := adaptPlayer(int64(500+play), hand)
		if c.anyPlace {
			pl.Offset = c.habit
		}
		pl.Lazy, pl.HabitG, pl.Corner = c.lazy, c.habitG, c.corner
		pl.PlaceRef, pl.PlaceK = c.steadyAt, c.steadyK
		if c.shake > 0 {
			pl.ShakeRef, pl.ShakeExp = c.nat.W, c.shake
		}
		if c.handA > 0 {
			pl.APar, pl.APerp = c.handA, 0.8*c.handA
		}
		if c.kappa != 0 {
			pl.Pivot = [2]float64{c.nat.X + c.pivot[0], c.nat.Y + c.pivot[1]}
			pl.Kappa, pl.Phi0 = c.kappa, PivotAngle(pl.Pivot, c.nat.X+c.straightAt, c.nat.Y)
		}
		frames := pl.Play(bm, 0, setup)
		objs, d := bm.Objects(0)
		hits, _ := analysis.Judge(objs, d, frames)
		end := wall.Add(time.Duration(bm.LengthMs()) * time.Millisecond)
		pd := &analysis.PlayData{ID: play + 1, Time: end, Setup: setup, Rate: 1, Hits: hits, Duration: bm.LengthMs(), Epoch: 1}
		ws := analysis.MapWindows(pd)
		line, lineN := analysis.PathLines(hits, frames, setup.Mapper(), 1)
		for i := range ws {
			ws[i].Line, ws[i].LineN = line, lineN/len(ws)
		}
		run.AddMap(ws)
		plays = append(plays, pd)
		if c.local && !run.Opts.Miss.OK() {
			if mm := analysis.DiagMissModel(plays, run.Obs, nil); mm.OK() {
				oo := run.Opts
				oo.Miss = mm
				run.SetOpts(oo)
				t.Logf("play %2d: misses expected by width from the diagnosis maps", play+1)
			}
		}
		clicks, miss, _ := analysis.AimQuality(hits)
		best := run.Rest()
		next, try := run.Next(miss == 0)
		res.blocked, res.limits = append(res.blocked, run.Blocked), append(res.limits, run.Limit)
		if c.hold && miss == 0 && !sameR(next, cur) {
			t.Errorf("play %d: no aim miss, yet the area changed", play+1)
		}
		res.areas = append(res.areas, cur)
		res.bests = append(res.bests, best)
		f := run.Fit
		res.scores = append(res.scores, f.Score.Pct)
		t.Logf("play %2d: %.1f×%.1f at %.1f,%.1f: %d clicks %d aim misses, lines %.1f%% → best %.1f, next %.1f×%.1f (%+.1f%%) at %.1f,%.1f [W %.1f±%.1f b %.2f | miss %.1f | arc %.2f°/10mm shift %+.1f | perfect %.1f%% sure %.0f clean %.0f spread %.1f%%] n=%d",
			play+1, cur.W, cur.H, cur.X, cur.Y, clicks, miss, 100*line, best.W, next.W, next.H, 100*(math.Exp(try)-1), next.X, next.Y,
			f.W.Value, f.W.SD, f.W.Slope, f.MissBest, math.Hypot(f.Arc.GX, f.Arc.GY), f.Arc.Shift, f.Score.Pct, f.Score.Sure, f.Score.Clean, 100*f.Score.Spread, f.N)
		run.Set(next)
		cur = next
		wall = end.Add(30 * time.Second)
	}
	res.fit = run.Fit
	if known, ok := analysis.AreaReaches(run.Obs, time.Time{}); known && !ok {
		t.Errorf("the areas set were judged not to reach the tablet")
	}
	if c.missA > 0 || c.local {
		t.Logf("expected aim misses: %.1f per 1000 with %.1f mm (natural %.1f: %.1f); precision %+v", run.Fit.MissBest, run.Fit.Target.W, run.Fit.Natural, run.Fit.MissNat, run.Fit.Prec)
	}
	return res
}

// missModelFor fits the model of the aim misses by width on normal plays
// made with the area nat by a hand that scatters a mm when still.
func missModelFor(t *testing.T, nat otd.Rect, a float64) *analysis.MissModel {
	maps := liveMaps(t)
	setup := testSetup()
	setup.Area = nat
	var plays []*analysis.PlayData
	for i := 0; i < 16; i++ {
		bm := maps[i%len(maps)]
		pl := &Player{APar: a, BPar: 0.02, CPar: 50, P: 2, APerp: a * 0.8, BPerp: 0.012, CPerp: 27,
			TimingSD: 12, Rng: rand.New(rand.NewSource(int64(900 + i)))}
		frames := pl.Play(bm, 0, setup)
		objs, d := bm.Objects(0)
		hits, _ := analysis.Judge(objs, d, frames)
		plays = append(plays, &analysis.PlayData{ID: i + 1, Time: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Hour * 5),
			Epoch: 1, Setup: setup, Rate: 1, Hits: hits, Duration: bm.LengthMs()})
	}
	ep := analysis.EpochInfo{ID: 1, Setup: setup, From: plays[0].Time.Add(-time.Hour), Established: true}
	rep := analysis.Analyze(analysis.Input{Plays: plays, Epochs: []analysis.EpochInfo{ep}, Current: &ep, Now: plays[len(plays)-1].Time})
	return rep.MissModel()
}

func sameR(a, b otd.Rect) bool {
	return math.Abs(a.W-b.W) < 1e-6 && math.Abs(a.H-b.H) < 1e-6 && math.Abs(a.X-b.X) < 1e-6 && math.Abs(a.Y-b.Y) < 1e-6
}

func TestAdaptLoop(t *testing.T) {
	ratio := 1080.0 / 1920
	cases := []loopCase{
		{name: "locked", locked: true, nat: otd.Rect{W: 78, H: 78 * ratio, X: 71, Y: 50}, anchor: otd.Rect{W: 70, H: 70 * ratio, X: 76, Y: 47.5}, residual: 0.35, plays: 10, moveAt: 6, moveX: -8},
		{name: "free", locked: false, nat: otd.Rect{W: 84, H: 50, X: 80, Y: 46}, anchor: otd.Rect{W: 70, H: 70 * ratio, X: 76, Y: 47.5}, residual: 0.35, plays: 8},
		// starts from the area in use, not from all of the tablet
		{name: "from-current", locked: true, nat: otd.Rect{W: 62, H: 62 * ratio, X: 74, Y: 48}, anchor: otd.Rect{W: 80, H: 45, X: 76, Y: 47.5}, start: otd.Rect{W: 80, H: 45, X: 76, Y: 47.5}, residual: 0.35, plays: 6},
		// lands a little to the right whatever the area: the area must not chase it
		{name: "habit-right", locked: true, nat: otd.Rect{W: 70, H: 70 * ratio, X: 76, Y: 47.5}, anchor: otd.Rect{W: 70, H: 70 * ratio, X: 76, Y: 47.5}, residual: 0.35, plays: 10, anyPlace: true, habit: [2]float64{0.6, 0}},
		// plays far from the middle of the tablet, starting from all of it
		{name: "corner", locked: true, nat: otd.Rect{W: 60, H: 60 * ratio, X: 55, Y: 38}, anchor: otd.Rect{W: 152, H: 85.5, X: 76, Y: 47.5}, residual: 0.35, plays: 8},
		// the place kept still: only the size adapts
		{name: "fixed-place", locked: true, nat: otd.Rect{W: 78, H: 78 * ratio, X: 71, Y: 50}, anchor: otd.Rect{W: 70, H: 70 * ratio, X: 76, Y: 47.5}, residual: 0.35, plays: 6, fixPos: true},
		// no change after a clean map: it still finds the area from all of
		// the tablet, where nothing is hit
		{name: "hold-clean", locked: true, nat: otd.Rect{W: 62, H: 62 * ratio, X: 74, Y: 48}, anchor: otd.Rect{W: 152, H: 85.5, X: 76, Y: 47.5}, residual: 0.35, plays: 6, hold: true, tol: 0.12},
		// the misses expected by width (from the replays) say a shaky hand
		// misses less with a little more area than it is used to
		{name: "shaky-hand", locked: true, nat: otd.Rect{W: 62, H: 62 * ratio, X: 74, Y: 48}, anchor: otd.Rect{W: 70, H: 70 * ratio, X: 76, Y: 47.5}, residual: 0.35, plays: 6, missA: 1.2, tol: 0.25},
		// the player of the field report: used to a small area, starts from
		// the whole tablet and hits nothing; the hand barely adapts
		{name: "hits-nothing", locked: true, nat: otd.Rect{W: 62, H: 62 * ratio, X: 74, Y: 48}, anchor: otd.Rect{W: 152, H: 85.5, X: 76, Y: 47.5}, residual: 0.9, plays: 4, firstMax: 80},
		{name: "hits-nothing-absolute", locked: true, nat: otd.Rect{W: 62, H: 62 * ratio, X: 74, Y: 48}, anchor: otd.Rect{W: 152, H: 85.5, X: 76, Y: 42.75}, residual: 0.9, plays: 3, firstMax: 80, absolute: 1},
		{name: "half-absolute", locked: true, nat: otd.Rect{W: 78, H: 78 * ratio, X: 71, Y: 50}, anchor: otd.Rect{W: 70, H: 70 * ratio, X: 76, Y: 47.5}, residual: 0.35, plays: 8, absolute: 0.5},
		// undershoots the vertical movements by 6% whatever the area: the
		// shape must not chase it (it would need 15% less height; one area a
		// map varies the shape little, so it is held back only part of the way)
		{name: "free-habit-y", locked: false, nat: otd.Rect{W: 84, H: 50, X: 80, Y: 46}, anchor: otd.Rect{W: 70, H: 70 * ratio, X: 76, Y: 47.5}, residual: 0.35, plays: 10, habitG: [2]float64{0, -0.06}, tol: 0.1},
		// a shape chosen by the player: only the size and the place adapt
		{name: "fixed-shape", locked: true, shape: 0.74, nat: otd.Rect{W: 72, H: 72 * 0.74, X: 77, Y: 49}, anchor: otd.Rect{W: 70, H: 70 * ratio, X: 76, Y: 47.5}, residual: 0.35, plays: 6},
		// a hand that gets used to most of any width: what the jumps say is
		// too little to go by, the misses expected decide (from the maps of
		// the diagnosis, as in the program)
		{name: "well-adapted", locked: true, nat: otd.Rect{W: 95, H: 95 * ratio, X: 76, Y: 47.5}, anchor: otd.Rect{W: 60, H: 60 * ratio, X: 76, Y: 47.5}, residual: 0.15, plays: 10, local: true, tol: 0.3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := runLoop(t, c)
			nat := c.nat
			if c.moveAt > 0 {
				nat.X += c.moveX
			}
			if c.fixPos {
				nat.X, nat.Y = c.anchor.X, c.anchor.Y
			}
			f := r.fit
			t.Logf("result %+v (natural %+v)", f.Target, nat)
			tol := c.tol
			if tol == 0 {
				tol = 0.05
			}
			if math.Abs(f.Target.W/nat.W-1) > tol {
				t.Errorf("width %.1f, the hand is used to %.1f", f.Target.W, nat.W)
			}
			if math.Abs(f.Target.X-nat.X) > 2 || math.Abs(f.Target.Y-nat.Y) > 2 {
				t.Errorf("centre %.1f,%.1f, the hand plays at %.1f,%.1f", f.Target.X, f.Target.Y, nat.X, nat.Y)
			}
			if !c.locked && math.Abs(f.Target.H/nat.H-1) > tol {
				t.Errorf("height %.1f, the hand is used to %.1f", f.Target.H, nat.H)
			}
			want := ratio
			if c.shape > 0 {
				want = c.shape
			}
			if c.locked && math.Abs(f.Target.H/f.Target.W-want) > 1e-3 {
				t.Errorf("proportions not kept: %+v", f.Target)
			}
			if c.firstMax > 0 && len(r.bests) > 0 && r.bests[0].W > c.firstMax {
				t.Errorf("after the first map the best area was %.1f mm (should be under %.0f)", r.bests[0].W, c.firstMax)
			}
			if c.missA >= 1 && f.Natural > 0 && f.Target.W <= f.Natural {
				t.Errorf("a shaky hand got %.1f mm, less than the %.1f it is used to", f.Target.W, f.Natural)
			}
		})
	}
}

// Every map tries a width a little different from the best one so far:
// with a hand shakier on small areas than the replays say, the widths
// tried show it, with no map played on purpose with another area. The
// closeness to a perfect area grows with the maps. A habit of jumping
// short that no width fixes is not chased.
func TestAdaptTries(t *testing.T) {
	ratio := 1080.0 / 1920
	base := loopCase{locked: true, nat: otd.Rect{W: 62, H: 62 * ratio, X: 74, Y: 48}, anchor: otd.Rect{W: 152, H: 85.5, X: 76, Y: 47.5},
		residual: 0.15, plays: 20, missA: 1, handA: 1, tol: 0.5}
	steady := base
	steady.name = "steady"
	shaky := base
	shaky.name, shaky.shake = "shaky-small", 3
	var ws, gs [2]float64
	for i, c := range []loopCase{steady, shaky} {
		t.Run(c.name, func(t *testing.T) {
			r := runLoop(t, c)
			ws[i], gs[i] = r.fit.Target.W, r.fit.Prec.Gamma
			t.Logf("%s: %.1f mm, precision %+v, score %v", c.name, r.fit.Target.W, r.fit.Prec, r.scores)
			for k := 1; k < len(r.areas); k++ {
				if sameR(r.areas[k], r.areas[k-1]) {
					t.Errorf("maps %d and %d were played with the same area", k, k+1)
				}
			}
			n := len(r.scores)
			if r.scores[n-1] <= r.scores[1]+10 {
				t.Errorf("the closeness to a perfect area did not grow: %v", r.scores)
			}
			if r.scores[n-1] >= 100 || r.scores[n-1] <= 0 {
				t.Errorf("closeness %.1f%%", r.scores[n-1])
			}
		})
	}
	// small differences of width show the trend only weakly (the probes of
	// before showed more, at the cost of maps played on purpose with an
	// area far from the best): its direction is enough
	if gs[1] >= gs[0] {
		t.Errorf("the hand shakier on small areas was not seen: trend %+.2f against %+.2f", gs[1], gs[0])
	}
	if ws[1] < ws[0]-1 {
		t.Errorf("a hand shakier on small areas got %.1f mm, a steady one %.1f", ws[1], ws[0])
	}
	// jumps 4% short whatever the width, and a hand that gets used to any
	// width: the area must not keep shrinking. The same with and without the
	// misses expected by width.
	for _, miss := range []float64{0.55, 0} {
		var got [2]float64
		for i, hg := range []float64{0, -0.04} {
			c := base
			c.name, c.residual, c.habitG, c.missA, c.plays = fmt.Sprintf("habit-%.2f-miss-%v", hg, miss > 0), 0.03, [2]float64{hg, hg}, miss, 9
			t.Run(c.name, func(t *testing.T) {
				r := runLoop(t, c)
				got[i] = r.fit.Target.W
				t.Logf("%s: %.1f mm (b %.2f)", c.name, r.fit.Target.W, r.fit.W.Slope)
			})
		}
		if miss > 0 && got[1] < 0.93*got[0] {
			t.Errorf("a habit of jumping short shrank the area to %.1f mm (%.1f without it)", got[1], got[0])
		}
		if got[1] < 0.9*base.nat.W {
			t.Errorf("a habit of jumping short shrank the area to %.1f mm (the hand is used to %.1f)", got[1], base.nat.W)
		}
	}
}

// With the size kept, every map has the same size and only the place
// adapts.
func TestAdaptFixedSize(t *testing.T) {
	ratio := 1080.0 / 1920
	start := otd.Rect{W: 90, H: 90 * ratio, X: 76, Y: 47.5}
	c := loopCase{name: "fixed-size", locked: true, nat: otd.Rect{W: 70, H: 70 * ratio, X: 70, Y: 50}, anchor: start, start: start,
		residual: 0.35, plays: 7, fixSize: true, missA: 0.55}
	r := runLoop(t, c)
	for i, a := range r.areas {
		if math.Abs(a.W-start.W) > 1e-6 || math.Abs(a.H-start.H) > 1e-6 {
			t.Errorf("map %d played with %.1f×%.1f, the size kept is %.1f×%.1f", i+1, a.W, a.H, start.W, start.H)
		}
	}
	last := r.bests[len(r.bests)-1]
	if math.Abs(last.X-c.nat.X) > 2.5 || math.Abs(last.Y-c.nat.Y) > 2.5 {
		t.Errorf("the place found is %.1f,%.1f, the hand plays at %.1f,%.1f", last.X, last.Y, c.nat.X, c.nat.Y)
	}
}

// A hand that turns its movements around the wrist, which rests to the
// right of the area, and aims straight 14 mm to the right of where the area
// is: the turning is measured, and the area slides there, without being
// rotated. Where the hand already aims straight, it stays.
func TestAdaptArc(t *testing.T) {
	ratio := 1080.0 / 1920
	nat := otd.Rect{W: 78, H: 78 * ratio, X: 76, Y: 47.5}
	for _, tc := range []struct {
		name     string
		pivot    [2]float64
		straight float64
		place    bool // the hand is used to its place: it lands off when the area moves
		lo, hi   float64
	}{
		{"wrist-right", [2]float64{60, 110}, 14, false, 11, 17},
		{"wrist-below", [2]float64{0, 120}, -10, false, -13, -7},
		{"already-straight", [2]float64{60, 110}, 0, false, -3, 3},
		// moving the area 14 mm would make the clicks land 5 mm off: it moves
		// only as far as that costs less than the turned aim
		{"wrist-right-place", [2]float64{60, 110}, 14, true, -1, 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := loopCase{name: tc.name, locked: true, nat: nat, anchor: nat, start: nat, residual: 0.35, plays: 8,
				anyPlace: !tc.place, pivot: tc.pivot, kappa: 0.5, straightAt: tc.straight}
			r := runLoop(t, c)
			a := r.fit.Arc
			t.Logf("arc %+v", a)
			if !a.Strong {
				t.Fatalf("the turning across the tablet was not seen: %+v", a)
			}
			if tc.pivot[0] > 20 && a.Dir > -5 {
				t.Errorf("the wrist is to the right, yet the arc says %.1f°", a.Dir)
			}
			last := r.areas[len(r.areas)-1]
			if d := last.X - nat.X; d < tc.lo || d > tc.hi {
				t.Errorf("the area ended at X %.1f (%+.1f): it should be %+.0f to %+.0f mm from %.1f (the hand aims straight at %+.0f)", last.X, d, tc.lo, tc.hi, nat.X, tc.straight)
			}
			for _, ar := range r.areas {
				if ar.Rot != nat.Rot {
					t.Errorf("the area was rotated: %+v", ar)
				}
			}
		})
	}
}

// Misses always in the same corner (one the hand reaches badly) are not
// chased: the area neither moves toward the corner nor grows for it.
func TestAdaptCornerMisses(t *testing.T) {
	ratio := 1080.0 / 1920
	nat := otd.Rect{W: 78, H: 78 * ratio, X: 76, Y: 47.5}
	base := loopCase{locked: true, nat: nat, anchor: nat, start: nat, residual: 0.35, plays: 8, missA: 0.55}
	var got [2]analysis.AdaptFit
	for i, corner := range [][2]float64{{0, 0}, {2.2, 1.6}} {
		c := base
		c.name, c.corner = fmt.Sprintf("corner-%v", corner[0] > 0), corner
		t.Run(c.name, func(t *testing.T) {
			r := runLoop(t, c)
			got[i] = r.fit
		})
	}
	a, b := got[0].Target, got[1].Target
	t.Logf("without the corner %+v, with it %+v", a, b)
	if b.X-a.X > 1 || b.Y-a.Y > 1 {
		t.Errorf("the area moved toward the corner: %.1f,%.1f against %.1f,%.1f", b.X, b.Y, a.X, a.Y)
	}
	if b.W > a.W*1.04 {
		t.Errorf("the area grew for the corner: %.1f mm against %.1f", b.W, a.W)
	}
}

// The widths the player allows are never left.
func TestAdaptRange(t *testing.T) {
	ratio := 1080.0 / 1920
	c := loopCase{name: "range", locked: true, nat: otd.Rect{W: 62, H: 62 * ratio, X: 74, Y: 48}, anchor: otd.Rect{W: 120, H: 67.5, X: 76, Y: 47.5},
		start: otd.Rect{W: 120, H: 67.5, X: 76, Y: 47.5}, residual: 0.35, plays: 5, minW: 110, maxW: 150, tol: 10}
	r := runLoop(t, c)
	for i, a := range r.areas {
		if a.W < 110-1e-6 || a.W > 150+1e-6 {
			t.Errorf("map %d played with %.1f mm, outside 110-150", i+1, a.W)
		}
	}
	if w := r.fit.Target.W; w < 110-1e-6 || w > 150+1e-6 {
		t.Errorf("area found %.1f mm, outside 110-150", w)
	}
}

// The hand would want a narrower area than the player allows: the area
// found stays at the limit, the narrower tries are not played (the best
// one is, said why), and the best width without the limit is told.
func TestAdaptRangeBlocked(t *testing.T) {
	ratio := 1080.0 / 1920
	nat := otd.Rect{W: 70, H: 70 * ratio, X: 74, Y: 48}
	c := loopCase{name: "range-blocked", locked: true, nat: nat, anchor: nat, start: otd.Rect{W: 84, H: 84 * ratio, X: 74, Y: 48},
		residual: 0.35, plays: 8, minW: 84, missA: 0.55, tol: 10}
	r := runLoop(t, c)
	n := 0
	for i, b := range r.blocked {
		if b != "" {
			n++
			if b != "min" || math.Abs(r.limits[i]-84) > 1e-6 {
				t.Errorf("decision %d blocked by %q at %.1f, want min at 84", i+1, b, r.limits[i])
			}
		}
	}
	if n == 0 {
		t.Errorf("no narrower try was ever blocked by the 84 mm limit: %v", r.blocked)
	}
	f := r.fit
	if math.Abs(f.Target.W-84) > 0.5 {
		t.Errorf("area found %.1f mm, want the limit 84", f.Target.W)
	}
	if !(f.FreeW > 0 && f.FreeW < 83) {
		t.Errorf("best width without the limit %.1f, want below 84", f.FreeW)
	}
	t.Logf("without the limit: %.1f mm, %.1f misses per 1000 (with 84: %.1f)", f.FreeW, f.FreeMiss, f.MissBest)
}

// With no replays to tell the misses expected by width (a new grip), the
// diagnosis builds them from its own maps and keeps recommending: about the
// width the replays of the same hand would have said.
func TestAdaptLocalModel(t *testing.T) {
	ratio := 1080.0 / 1920
	c := loopCase{name: "local", locked: true, nat: otd.Rect{W: 100, H: 100 * ratio, X: 76, Y: 47.5}, anchor: otd.Rect{W: 120, H: 67.5, X: 76, Y: 47.5},
		start: otd.Rect{W: 120, H: 67.5, X: 76, Y: 47.5}, residual: 0.1, plays: 7, local: true, habitG: [2]float64{-0.03, -0.03}}
	r := runLoop(t, c)
	f := r.fit
	if f.MissBest <= 0 || len(f.MissCurve) == 0 {
		t.Fatalf("no misses expected by width from the diagnosis maps")
	}
	moved := false
	for i := 1; i < len(r.areas); i++ {
		if !sameR(r.areas[i], r.areas[i-1]) {
			moved = true
		}
	}
	if !moved {
		t.Errorf("the area never changed")
	}
	ref := c
	ref.name, ref.local, ref.missA = "replays", false, 0.55
	rr := runLoop(t, ref)
	t.Logf("from the diagnosis maps %.1f mm, from the replays %.1f mm", f.Target.W, rr.fit.Target.W)
	if math.Abs(f.Target.W/rr.fit.Target.W-1) > 0.12 {
		t.Errorf("from the diagnosis maps %.1f mm, from the replays of the same hand %.1f mm", f.Target.W, rr.fit.Target.W)
	}
}

func liveMaps(t *testing.T) []*osu.Beatmap {
	var out []*osu.Beatmap
	for _, name := range []string{"1124896", "1341554", "2593923", "801165"} {
		bm, err := osu.ParseBeatmapFile(filepath.Join(lazerBeatmaps, name+".osu"))
		if err != nil {
			t.Skip("reference maps not available")
		}
		out = append(out, bm)
	}
	return out
}

// A hand that gets used to wherever the area is (where the clicks land does
// not change with its place), playing far from the area it had before the
// diagnosis: nothing in the aim says the area should go back there, so it
// stays about where the hand has been playing, and it never jumps.
func TestAdaptHomeMoved(t *testing.T) {
	ratio := 1080.0 / 1920
	for _, tc := range []struct {
		name  string
		start otd.Rect
	}{
		{"from-here", otd.Rect{W: 80, H: 80 * ratio, X: 72, Y: 52}},
		{"from-whole", otd.Rect{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			anchor := otd.Rect{W: 116, H: 116 * ratio, X: 64.5, Y: 36.3}
			nat := otd.Rect{W: 80, H: 80 * ratio, X: 72, Y: 52}
			start := tc.start
			c := loopCase{name: tc.name, locked: true, nat: nat, anchor: anchor, start: start, residual: 0.35, plays: 12, anyPlace: true, tol: 0.12}
			r := runLoop(t, c)
			// where the hand first played (after the whole tablet, when it
			// started there)
			first := start
			if !first.Valid() {
				first = r.areas[1]
			}
			for i := 1; i < len(r.areas); i++ {
				a, b := r.areas[i-1], r.areas[i]
				if i > 1 || start.Valid() {
					if d := math.Hypot(b.X-a.X, b.Y-a.Y); d > 1.01*math.Sqrt2*analysis.AdaptStepMax {
						t.Errorf("map %d: the area jumped %.1f mm", i+1, d)
					}
				}
			}
			end := r.fit.Target
			if dy := end.Y - first.Y; math.Abs(dy) > 3 {
				t.Errorf("the area went from Y %.1f to %.1f: toward the area of before (Y %.1f) with nothing in the aim asking for it", first.Y, end.Y, anchor.Y)
			}
			if dx := end.X - first.X; math.Abs(dx) > 3 {
				t.Errorf("the area went from X %.1f to %.1f", first.X, end.X)
			}
		})
	}
}

// A hand that clicks a little right of the circles wherever the area is
// (the wrist follows it), and is steadier with the area a little to the
// left: the area does not follow the habit to the right, it goes toward
// where the hand is steadier.
func TestAdaptSteadyPlace(t *testing.T) {
	ratio := 1080.0 / 1920
	nat := otd.Rect{W: 70, H: 70 * ratio, X: 76, Y: 47.5}
	base := loopCase{locked: true, nat: nat, anchor: nat, start: nat, residual: 0.35, plays: 16, anyPlace: true, habit: [2]float64{0.6, 0}, missA: 0.55, tol: 0.15}
	var xs [2]float64
	for i, k := range []float64{0, 0.03} {
		c := base
		c.name = fmt.Sprintf("steady-k%.2f", k)
		c.steadyAt, c.steadyK = [2]float64{72, 47.5}, k
		r := runLoop(t, c)
		f := r.fit
		xs[i] = f.Target.X
		t.Logf("k %.2f: area found at X %.1f Y %.1f; steadiness by place %+.4f±%.4f /mm (x), %+.4f±%.4f (y); moved %+.2f, %+.2f for it",
			k, f.Target.X, f.Target.Y, f.Steady.GX, f.Steady.GXSE, f.Steady.GY, f.Steady.GYSE, f.Steady.ShiftX, f.Steady.ShiftY)
		tries := 0
		for i := 1; i < len(r.areas); i++ {
			if math.Abs(r.areas[i].X-r.bests[i-1].X) > 0.5 || math.Abs(r.areas[i].Y-r.bests[i-1].Y) > 0.5 {
				tries++
			}
		}
		if tries < len(r.areas)/2 {
			t.Errorf("k %.2f: the place was tried off the best in %d maps of %d", k, tries, len(r.areas))
		}
	}
	if xs[1] > xs[0]-0.8 {
		t.Errorf("steadier to the left: the area found at X %.1f, %.1f without it", xs[1], xs[0])
	}
	if xs[1] < 72-2.5 {
		t.Errorf("went past where the hand is steadiest: X %.1f", xs[1])
	}
}
