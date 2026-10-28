package engine

import (
	"math"
	"testing"
	"time"

	"areacoach/internal/filter"
	"areacoach/internal/store"
)

// a simulated family whose best strength is at bestOuter (mm) for the
// default shape, a little worse for the others
func testFamily(bestOuter float64) *filter.Family {
	f := &filter.Family{Rate: 133, N: 3000, Plays: 6, MissRaw: 30, TremRaw: 0.04, Outers: filter.FamilyOuters, Shapes: filter.FamilyShapes}
	for _, o := range f.Outers {
		row := make([]filter.Eval, len(f.Shapes))
		for k, s := range f.Shapes {
			d := math.Log(o / bestOuter)
			g := 9 - 10*d*d
			if s != coachDefShape {
				g -= 2
			}
			row[k] = filter.Eval{Params: s.Params(o), N: 3000, MissRaw: 30, Miss: 30 - g, Gain: g, Delay: 7 * o,
				Lost: 2 * o, TremRaw: 0.04, Trem: 0.04 * math.Exp(-o*s.Inner/0.15)}
		}
		f.Evals = append(f.Evals, row)
	}
	return f
}

// a map played with p (nil: no filter) with this many aim misses against
// those expected
func testMap(p *filter.Params, miss int, exp float64, trem float64) store.AdaptMap {
	m := store.AdaptMap{Windows: 10, Clicks: 800, AimMiss: miss, Exp: exp, Trem: trem, CurN: 700, At: time.Now()}
	if p == nil {
		m.Filter = "off"
	} else {
		q := *p
		m.Filter, m.FParams = "on", &q
	}
	return m
}

func TestCoachStart(t *testing.T) {
	// nothing known: the default values
	d := coachDecide(coachIn{})
	if !d.AskOn || math.Abs(d.Best.Outer-coachDefOuter) > 0.3 || d.Best.Inner <= 0 {
		t.Fatalf("start without a simulation: %+v", d)
	}
	// with the simulation: about its best strength
	d = coachDecide(coachIn{fam: testFamily(1.2)})
	if math.Abs(math.Log(d.Best.Outer/1.2)) > 0.15 {
		t.Errorf("best %.2f mm, the simulation says 1.2", d.Best.Outer)
	}
	if d.Best.Inner/d.Best.Outer < 0.39 || d.Best.Inner/d.Best.Outer > 0.41 || d.Best.Coef != 0.5 {
		t.Errorf("shape %+v, want the default one (best in the simulation)", d.Best)
	}
	if d.Pct >= 10 {
		t.Errorf("%.1f%% perfect before playing a map with it", d.Pct)
	}
	if !d.AskOn || d.AskTry == 0 {
		t.Errorf("the first map should try a little off the best: %+v", d)
	}
}

// The game says stronger filters do worse than the simulation thinks: the
// best one gets lighter, map after map, and the tries go either side.
func TestCoachFollowsTheGame(t *testing.T) {
	fam := testFamily(1.2)
	var maps []store.AdaptMap
	for i := 0; i < 4; i++ {
		maps = append(maps, testMap(nil, 20, 40, 0.04))
	}
	in := coachIn{fam: fam}
	var prev *filter.Params
	var asks []float64
	for k := 0; k < 14; k++ {
		in.maps, in.dither, in.prev = maps, k, prev
		d := coachDecide(in)
		asks = append(asks, d.Ask.Outer)
		b := d.Best
		prev = &b
		// the hand: best at 0.5 mm, worse the stronger beyond it, as good
		// as with no filter at its best
		p := d.Ask
		if !d.AskOn {
			maps = append(maps, testMap(nil, 20, 40, 0.04))
			t.Logf("map %2d: no filter (to compare)", k+1)
			continue
		}
		u := math.Log(p.Outer / 0.5)
		exp := 40.0
		miss := int(math.Round(20 * math.Exp(0.7*u*u)))
		maps = append(maps, testMap(&p, miss, exp, 0.01))
		t.Logf("map %2d: asked %.2f (try %+.2f) → %d misses; best %.2f, perfect %.1f%% (sure %.0f, clean %.0f), tilt %.2f±%.2f, place ±%.2f, improve %.2f [%.2f %.2f] trem %.3f off %.3f",
			k+1, p.Outer, d.AskTry, miss, d.Best.Outer, d.Pct, d.Sure, d.Clean, d.Tilt, d.TiltSE, d.PlaceSE, d.Improve, d.ImpLo, d.ImpHi, d.Trem, d.TremOff)
	}
	in.maps, in.prev = maps, prev
	d := coachDecide(in)
	if d.Best.Outer > 0.85 {
		t.Errorf("best %.2f mm after the game said lighter is better (0.5)", d.Best.Outer)
	}
	up, down := 0, 0
	for i, a := range asks {
		if i > 0 && a > asks[i-1] {
			up++
		}
		if i > 0 && a < asks[i-1] {
			down++
		}
	}
	if up < 3 || down < 3 {
		t.Errorf("the tries do not go either side: %v", asks)
	}
}

// Every filter played does worse than none: after a few maps, no filter.
func TestCoachOffBetter(t *testing.T) {
	fam := testFamily(1.0)
	var maps []store.AdaptMap
	for i := 0; i < 5; i++ {
		maps = append(maps, testMap(nil, 15, 40, 0.01))
	}
	in := coachIn{fam: fam}
	var prev *filter.Params
	for k := 0; k < 8; k++ {
		in.maps, in.dither, in.prev = maps, k, prev
		d := coachDecide(in)
		b := d.Best
		prev = &b
		if d.AskOn {
			p := d.Ask
			maps = append(maps, testMap(&p, 34, 40, 0.005))
		} else {
			maps = append(maps, testMap(nil, 15, 40, 0.01))
		}
	}
	in.maps, in.prev = maps, prev
	d := coachDecide(in)
	if d.BestOn {
		t.Errorf("the filter is still best (%+v) with twice the misses of no filter; improve %.2f [%.2f, %.2f]", d.Best, d.Improve, d.ImpLo, d.ImpHi)
	}
	if d.Improve > -0.3 {
		t.Errorf("improve %.2f, the filter doubles the aim misses", d.Improve)
	}
}

// With no map played without a filter, the coach asks for one now and then,
// to have something to compare with.
func TestCoachAsksReference(t *testing.T) {
	p := filter.Params{Outer: 1, Inner: 0.4, Coef: 0.5, Knee: 1}
	maps := []store.AdaptMap{testMap(&p, 10, 20, 0.01)}
	refs := 0
	for k := 0; k < coachOffEvery; k++ {
		d := coachDecide(coachIn{fam: testFamily(1), maps: maps, dither: k})
		if d.AskRef && !d.AskOn {
			refs++
		}
		if d.Known {
			t.Errorf("the effect against no filter looks known with no map without one")
		}
	}
	if refs != 1 {
		t.Errorf("%d maps without a filter asked in %d, want 1", refs, coachOffEvery)
	}
}

func TestFilterAt(t *testing.T) {
	t0 := time.Date(2026, 10, 25, 18, 0, 0, 0, time.UTC)
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	p1 := filter.Params{Outer: 1, Inner: 0.4, Coef: 0.5, Knee: 1}
	c := &store.CalibSession{FSeen: []store.FilterSeen{{At: at(0), On: false}, {At: at(100), On: true, Params: p1, Tablet: true}}}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{st: st}
	for _, tc := range []struct {
		from, to int
		want     string
	}{{10, 90, "off"}, {50, 150, "mixed"}, {110, 200, "on"}, {97, 200, "on"}, {-50, -10, ""}} {
		key, p, src := e.filterAt(c, at(tc.from), at(tc.to))
		if key != tc.want || (key != "" && src != "otd") {
			t.Errorf("%d-%d: %q from %q, want %q", tc.from, tc.to, key, src, tc.want)
		}
		if key == "on" && (p == nil || !filter.SameParams(*p, p1)) {
			t.Errorf("values %+v", p)
		}
	}
	// not readable: the values asked, once the player said they are in
	c2 := &store.CalibSession{FCoach: &store.FilterCoach{Active: true, Ask: p1, AskOn: true, AskAt: at(0), Confirmed: at(20)}}
	if key, p, src := e.filterAt(c2, at(30), at(90)); key != "on" || src != "ok" || p == nil {
		t.Errorf("confirmed values: %q %v %q", key, p, src)
	}
	if key, _, _ := e.filterAt(c2, at(10), at(90)); key == "on" {
		t.Errorf("a map started before the values were in counts as played with them")
	}
}

// With as little latency as possible, the values proposed keep within the
// small delay, and are lighter than with some latency allowed.
func TestCoachLowLag(t *testing.T) {
	fam := testFamily(1.6) // in the simulation stronger is better, up to 1.6 mm (11 ms)
	normal := coachDecide(coachIn{fam: fam})
	low := coachDecide(coachIn{fam: fam, lowLag: true})
	t.Logf("normal: %+v (%.1f ms); low: %+v (%.1f ms)", normal.Best, normal.Delay, low.Best, low.Delay)
	if low.Best.Outer >= normal.Best.Outer {
		t.Errorf("as little latency as possible gave %.2f mm, not lighter than %.2f", low.Best.Outer, normal.Best.Outer)
	}
	if low.Delay > coachLowDelay+0.01 {
		t.Errorf("delay %.1f ms over the %.0f allowed", low.Delay, coachLowDelay)
	}
	for k := 0; k < 3; k++ {
		d := coachDecide(coachIn{fam: fam, lowLag: true, dither: k})
		if ev, _ := fam.At(d.Ask); d.AskOn && ev.Delay > coachLowDelay+0.3 {
			t.Errorf("try %d asks %.2f mm: %.1f ms of delay", k, d.Ask.Outer, ev.Delay)
		}
	}
	// without a simulation: a lighter start
	if d := coachDecide(coachIn{lowLag: true}); d.Best.Outer > coachDefOuter*0.6 {
		t.Errorf("start with as little latency as possible: %.2f mm", d.Best.Outer)
	}
}
