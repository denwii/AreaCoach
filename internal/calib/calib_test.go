package calib

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// player is a simulated player: at width w they land long by land(w) of
// each jump (short if negative) and scatter by spread(w) radii.
type player struct {
	land   func(w float64) float64
	spread func(w float64) float64
}

// click is one press at width w, scattered extra times more (getting used
// to a change).
func (p player) click(rng *rand.Rand, w, extra float64) Click {
	const r = 36.0
	j := 0.5 + rng.Float64()*9.5
	a := rng.Float64() * 2 * math.Pi
	mx, my := j*r*math.Cos(a), j*r*math.Sin(a)
	s := p.spread(w) * (1 + 0.04*j) * r * extra
	par := p.land(w)*j*r + rng.NormFloat64()*s
	perp := rng.NormFloat64() * s
	ex := par*math.Cos(a) - perp*math.Sin(a)
	ey := par*math.Sin(a) + perp*math.Cos(a)
	return Click{Width: w, Dist: math.Hypot(ex, ey) / r, ErrX: ex, ErrY: ey, Radius: r, MoveX: mx, MoveY: my, Gap: 100 + rng.Float64()*350, Weight: 1}
}

func (p player) clicks(rng *rand.Rand, w float64, n int) []Click {
	out := make([]Click, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, p.click(rng, w, 1))
	}
	return out
}

func newStore(t *testing.T) *Store { return Open(filepath.Join(t.TempDir(), "calibration.json")) }

var day0 = time.Date(2026, 11, 22, 20, 0, 0, 0, time.UTC)

func TestCrossFromLongToShort(t *testing.T) {
	s := newStore(t)
	rng := rand.New(rand.NewSource(1))
	p := player{land: func(w float64) float64 { return 0.006 * (90 - w) }, spread: func(float64) float64 { return 0.3 }}
	for w := 86.0; w <= 95; w++ {
		for k := 0; k < 5; k++ {
			s.Add("k", day0, fmt.Sprint(w, k), p.clicks(rng, w+0.5, 50))
		}
	}
	r := s.Analyze("k", day0)
	if !r.HasCross || math.Abs(r.Cross-90.5) > 0.8 || r.Habit {
		t.Fatalf("cross %v %.2f habit %v", r.HasCross, r.Cross, r.Habit)
	}
}

func TestHabitFollowsYou(t *testing.T) {
	s := newStore(t)
	rng := rand.New(rand.NewSource(2))
	p := player{land: func(float64) float64 { return -0.04 }, spread: func(float64) float64 { return 0.3 }}
	for w := 86.0; w <= 95; w++ {
		for k := 0; k < 5; k++ {
			s.Add("k", day0, fmt.Sprint(w, k), p.clicks(rng, w+0.5, 50))
		}
	}
	r := s.Analyze("k", day0)
	if !r.Habit || math.Abs(r.HabitK+0.04) > 0.01 || r.HasCross {
		t.Fatalf("habit %v %.3f cross %v", r.Habit, r.HabitK, r.HasCross)
	}
	// the clicks ask for a smaller area: not past the band
	d := r.Next(90, 84, 0.3, Plan{})
	if d.Width < r.BandLo-1e-9 {
		t.Fatalf("chased the habit: %+v (band %.0f-%.0f)", d, r.BandLo, r.BandHi)
	}
}

// Below 87 mm this player scatters more and more: a wall forms there.
func TestBandWallWhereYouMissMore(t *testing.T) {
	s := newStore(t)
	rng := rand.New(rand.NewSource(3))
	p := player{land: func(float64) float64 { return 0 }, spread: func(w float64) float64 { return 0.3 * (1 + 0.15*math.Max(0, 87-w)) }}
	for w := 82.0; w <= 95; w++ {
		for k := 0; k < 8; k++ {
			s.Add("k", day0, fmt.Sprint(w, k), p.clicks(rng, w+0.5, 50))
		}
	}
	r := s.Analyze("k", day0)
	if !r.HasBand || r.BandLo < 85 || r.BandLo > 87 || !r.LoWall {
		t.Fatalf("band %v %.0f-%.0f wall %v; widths %+v", r.HasBand, r.BandLo, r.BandHi, r.LoWall, r.Widths)
	}
	d := r.Next(88, 83, 0.3, Plan{})
	if d.Width < r.BandLo-1e-9 {
		t.Fatalf("went past the wall: %+v", d)
	}
}

func TestBandEdgeIsElastic(t *testing.T) {
	s := newStore(t)
	rng := rand.New(rand.NewSource(4))
	p := player{land: func(float64) float64 { return 0 }, spread: func(float64) float64 { return 0.3 }}
	for w := 88.0; w <= 93; w++ {
		for k := 0; k < 6; k++ {
			s.Add("k", day0, fmt.Sprint(w, k), p.clicks(rng, w+0.5, 50))
		}
	}
	r := s.Analyze("k", day0)
	if !r.HasBand || r.BandLo != 88 || r.BandHi != 94 || r.LoWall {
		t.Fatalf("band %.0f-%.0f wall %v", r.BandLo, r.BandHi, r.LoWall)
	}
	d := r.Next(88.2, 80, 0.3, Plan{})
	if d.Width < r.BandLo-math.Max(1, 0.015*88.2)-1e-9 {
		t.Fatalf("too far past the edge: %+v", d)
	}
	in := r.Next(90, 91, 0.3, Plan{})
	if in.Width < 91 || in.Factor < 0.6 || in.Hold {
		t.Fatalf("inside: %+v", in)
	}
}

func TestMemoryKeepsAndFades(t *testing.T) {
	path := filepath.Join(t.TempDir(), "calibration.json")
	s := Open(path)
	rng := rand.New(rand.NewSource(5))
	p := player{land: func(float64) float64 { return 0 }, spread: func(float64) float64 { return 0.3 }}
	s.Add("a", day0.Add(-20*24*time.Hour), "x", p.clicks(rng, 90.5, 1000))
	s.Add("a", day0, "y", p.clicks(rng, 92.5, 1000))
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	r := Open(path).Analyze("a", day0)
	if r.Days != 2 || r.Clicks < 1200 || r.Clicks > 1300 {
		t.Fatalf("days %d clicks %.0f (20 days ago should count about a quarter)", r.Days, r.Clicks)
	}
	if s.Seen("a", "x") || !s.Seen("a", "x") {
		t.Fatal("seen")
	}
	s.Forget("a")
	if r := s.Analyze("a", day0); r.Clicks != 0 {
		t.Fatal("not forgotten")
	}
}

// run is what a simulated calibration did.
type run struct {
	last, lowest, highest float64 // width at the end (average of the last blocks), lowest, highest
	changes, clicks       int
	rep                   Report
	rels                  []float64 // reliability after every stretch
}

// sculpt plays the live calibration the way the program does: stretches of
// about 150 clicks (counting less the first ones after a change, which this
// player also scatters more), then a decision.
func sculpt(t *testing.T, p player, start float64, blocks int, seed int64, explore bool) run {
	t.Helper()
	s := newStore(t)
	rng := rand.New(rand.NewSource(seed))
	w, pre := start, start
	since, need := 0, 0.0
	lastChange := 0.0
	plan := Plan{Explore: explore}
	out := run{lowest: start, highest: start}
	var sum float64
	n := 0
	for b := 0; b < blocks; b++ {
		day := day0.Add(time.Duration(b/40) * 24 * time.Hour)
		var cs []Click
		adapted := 0.0
		for adapted < 150 {
			// getting used to a change: more scatter at first
			extra := 1 + 0.6*math.Min(1, lastChange)*math.Exp(-float64(since)/20)
			c := p.click(rng, w, extra)
			c.Weight = AdaptWeight(since, need)
			since++
			adapted += c.Weight
			cs = append(cs, c)
		}
		out.clicks += len(cs)
		s.Add("k", day, fmt.Sprint(b), cs)
		r := s.Analyze("k", day)
		out.rels = append(out.rels, r.Reliability)
		base := w
		if plan.WasProbe {
			base = pre
		}
		fit := base*(1+p.land(base)) + rng.NormFloat64()*0.3
		d := r.Next(base, fit, 0.3, plan)
		next := base
		if d.Probe != 0 {
			pre, next = base, d.Width
			plan.WasProbe, plan.LastSide = true, d.Probe
		} else {
			plan.WasProbe = false
			if !d.Hold {
				lim := 0.015 * base * d.Factor
				next = base + math.Max(-lim, math.Min(lim, 0.6*d.Factor*(d.Width-base)))
			}
		}
		if ch := math.Abs(next - w); ch >= 0.05 {
			since, need, lastChange = 0, AdaptNeed(ch), ch
			out.changes++
			w = next
		}
		out.lowest, out.highest = math.Min(out.lowest, w), math.Max(out.highest, w)
		if b >= blocks-20 {
			sum += base
			n++
		}
		out.rep = r
	}
	out.last = sum / float64(n)
	return out
}

// Your case: the hand pulls towards 84 mm, but below about 87 you scatter
// more. It goes down, and settles above where the misses grow.
func TestSculptStopsWhereMissesGrow(t *testing.T) {
	p := player{
		land:   func(w float64) float64 { return 0.5 * (84 - w) / w },
		spread: func(w float64) float64 { return 0.3 * (1 + 0.2*math.Max(0, 87-w)) },
	}
	for seed := int64(1); seed <= 4; seed++ {
		r := sculpt(t, p, 92, 120, seed, true)
		t.Logf("settled around %.2f, lowest %.2f, %d changes in %d clicks, reliability %.2f, band %.0f-%.0f best %.1f",
			r.last, r.lowest, r.changes, r.clicks, r.rep.Reliability, r.rep.BandLo, r.rep.BandHi, r.rep.Best)
		if r.last < 86 || r.last > 90 || r.lowest < 83.5 {
			t.Fatalf("seed %d: settled around %.2f, lowest %.2f", seed, r.last, r.lowest)
		}
		if r.clicks/max(1, r.changes) < 150 {
			t.Fatalf("seed %d: a change every %d clicks", seed, r.clicks/max(1, r.changes))
		}
		if r.rep.Reliability < Reliable || r.rep.Best < 86 {
			t.Fatalf("seed %d: best %.1f reliability %.2f", seed, r.rep.Best, r.rep.Reliability)
		}
	}
}

// Someone else: the hand pulls towards 84 mm and the small area costs
// nothing: it goes all the way.
func TestSculptGoesWhereTheHandIs(t *testing.T) {
	p := player{
		land:   func(w float64) float64 { return 0.5 * (84 - w) / w },
		spread: func(float64) float64 { return 0.3 },
	}
	r := sculpt(t, p, 92, 120, 7, true)
	t.Logf("settled around %.2f", r.last)
	if r.last > 86 {
		t.Fatalf("held back for nothing: %.2f", r.last)
	}
}

// And someone who needs a bigger area.
func TestSculptGrowsToo(t *testing.T) {
	p := player{
		land:   func(w float64) float64 { return 0.5 * (100 - w) / w },
		spread: func(w float64) float64 { return 0.3 },
	}
	r := sculpt(t, p, 92, 120, 8, true)
	t.Logf("settled around %.2f", r.last)
	if r.last < 98 {
		t.Fatalf("didn't grow: %.2f", r.last)
	}
}

// TestDemoMemory writes a memory like the one of the "your case" player to
// $AREACOACH_DEMO_MEMORY, to look at the interface by hand. Skipped
// otherwise.
func TestDemoMemory(t *testing.T) {
	path := os.Getenv("AREACOACH_DEMO_MEMORY")
	if path == "" {
		t.Skip("AREACOACH_DEMO_MEMORY not set")
	}
	p := player{
		land:   func(w float64) float64 { return 0.5 * (84 - w) / w },
		spread: func(w float64) float64 { return 0.3 * (1 + 0.2*math.Max(0, 87-w)) },
	}
	s := Open(path)
	key := Key("Wacom CTL-472", 0.5625)
	rng := rand.New(rand.NewSource(66))
	w, pre := 92.0, 92.0
	since, need := 0, 0.0
	plan := Plan{Explore: true}
	now := time.Now()
	for b := 0; b < 70; b++ {
		day := now.Add(-time.Duration(1-b/35) * 24 * time.Hour)
		var cs []Click
		adapted := 0.0
		for adapted < 150 {
			c := p.click(rng, w, 1)
			c.Weight = AdaptWeight(since, need)
			since++
			adapted += c.Weight
			cs = append(cs, c)
		}
		s.Add(key, day, fmt.Sprint(b), cs)
		r := s.Analyze(key, day)
		base := w
		if plan.WasProbe {
			base = pre
		}
		d := r.Next(base, base*(1+p.land(base))+rng.NormFloat64()*0.3, 0.3, plan)
		next := base
		if d.Probe != 0 {
			pre, next = base, d.Width
			plan.WasProbe, plan.LastSide = true, d.Probe
		} else {
			plan.WasProbe = false
			if !d.Hold {
				lim := 0.015 * base * d.Factor
				next = base + math.Max(-lim, math.Min(lim, 0.6*d.Factor*(d.Width-base)))
			}
		}
		if ch := math.Abs(next - w); ch >= 0.05 {
			since, need = 0, AdaptNeed(ch)
			w = next
		}
	}
	s.SetPos(key, 76.6, 47.3)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
}

// One easy map played at a width far away doesn't move the band there.
func TestOneMapIsNotABand(t *testing.T) {
	s := newStore(t)
	rng := rand.New(rand.NewSource(9))
	p := player{land: func(float64) float64 { return 0 }, spread: func(float64) float64 { return 0.35 }}
	for w := 87.0; w <= 92; w++ {
		for k := 0; k < 6; k++ {
			s.Add("k", day0, fmt.Sprint(w, k), p.clicks(rng, w+0.5, 50))
		}
	}
	easy := player{land: func(float64) float64 { return 0 }, spread: func(float64) float64 { return 0.2 }}
	s.Add("k", day0, "easy", easy.clicks(rng, 79.5, 400))
	r := s.Analyze("k", day0)
	if !r.HasBand || r.BandLo < 86 || r.Best < 86 {
		t.Fatalf("band %.0f-%.0f best %.1f", r.BandLo, r.BandHi, r.Best)
	}
}

// A player who always lands 4% short: it stops chasing that.
func TestSculptDoesNotChaseAHabit(t *testing.T) {
	p := player{land: func(float64) float64 { return -0.04 }, spread: func(float64) float64 { return 0.3 }}
	r := sculpt(t, p, 92, 120, 9, true)
	t.Logf("settled around %.2f, lowest %.2f, habit %v", r.last, r.lowest, r.rep.Habit)
	if r.last < 85 || r.lowest < 83 {
		t.Fatalf("chased the habit: settled %.2f lowest %.2f", r.last, r.lowest)
	}
}

// Getting used to a change costs misses at every width: with the first
// clicks after a change counting less, no width looks worse for it.
func TestGettingUsedIsNotAWorseWidth(t *testing.T) {
	p := player{land: func(w float64) float64 { return 0.5 * (89 - w) / w }, spread: func(float64) float64 { return 0.3 }}
	r := sculpt(t, p, 92, 120, 10, true)
	worse := 0
	for _, w := range r.rep.Widths {
		if w.Worse {
			worse++
		}
	}
	t.Logf("band %.0f-%.0f, worse %d, reliability %.2f, settled %.2f", r.rep.BandLo, r.rep.BandHi, worse, r.rep.Reliability, r.last)
	if worse > 0 || math.Abs(r.last-89) > 1.5 {
		t.Fatalf("band %.0f-%.0f, worse %d, settled %.2f", r.rep.BandLo, r.rep.BandHi, worse, r.last)
	}
}

func TestWeightsCountLess(t *testing.T) {
	s := newStore(t)
	rng := rand.New(rand.NewSource(11))
	p := player{land: func(float64) float64 { return 0 }, spread: func(float64) float64 { return 0.3 }}
	cs := p.clicks(rng, 90.5, 200)
	for i := range cs {
		switch {
		case i < 50:
			cs[i].Weight = 0
		case i < 100:
			cs[i].Weight = 0.5
		}
	}
	s.Add("k", day0, "a", cs)
	if r := s.Analyze("k", day0); math.Abs(r.Clicks-125) > 1e-9 {
		t.Fatalf("clicks %.1f, want 125", r.Clicks)
	}
	if AdaptWeight(0, 40) != 0 || AdaptWeight(30, 40) != 0.5 || AdaptWeight(40, 40) != 1 || AdaptWeight(3, 0) != 1 {
		t.Fatal("adapt weights")
	}
	if AdaptNeed(0.01) != 0 || AdaptNeed(0.2) != 15 || AdaptNeed(1) != 30 || AdaptNeed(9) != 90 {
		t.Fatal("adapt need")
	}
}

func TestReliabilityGrows(t *testing.T) {
	s := newStore(t)
	rng := rand.New(rand.NewSource(12))
	p := player{land: func(float64) float64 { return 0 }, spread: func(w float64) float64 { return 0.3 * (1 + 0.04*math.Abs(w-90)) }}
	add := func(w float64, plays int) {
		for k := 0; k < plays; k++ {
			s.Add("k", day0, fmt.Sprint(w, k, rng.Int()), p.clicks(rng, w, 100))
		}
	}
	add(90.5, 4)
	r1 := s.Analyze("k", day0)
	add(88.5, 4)
	add(92.5, 4)
	r2 := s.Analyze("k", day0)
	for i := 0; i < 5; i++ {
		add(88.5, 3)
		add(90.5, 3)
		add(92.5, 3)
	}
	r3 := s.Analyze("k", day0)
	t.Logf("reliability %.2f -> %.2f -> %.2f, best %.1f", r1.Reliability, r2.Reliability, r3.Reliability, r3.Best)
	if !(r1.Reliability < 0.45 && r2.Reliability > r1.Reliability && r3.Reliability > r2.Reliability && r3.Reliability >= Reliable) {
		t.Fatalf("reliability %.2f %.2f %.2f", r1.Reliability, r2.Reliability, r3.Reliability)
	}
	if math.Abs(r3.Best-90.5) > 1.01 {
		t.Fatalf("best %.1f", r3.Best)
	}
}

func TestProbeAndHold(t *testing.T) {
	s := newStore(t)
	rng := rand.New(rand.NewSource(13))
	p := player{land: func(float64) float64 { return 0 }, spread: func(float64) float64 { return 0.3 }}
	for k := 0; k < 4; k++ {
		s.Add("k", day0, fmt.Sprint(k), p.clicks(rng, 90.5, 100))
	}
	r := s.Analyze("k", day0)
	d := r.Next(90.5, 91.5, 0.3, Plan{Explore: true})
	if d.Probe != 1 || math.Abs(d.Width-(r.Best+2.26)) > 0.05 {
		t.Fatalf("should try a wider width first (the clicks point there): %+v best %.1f", d, r.Best)
	}
	if d := r.Next(90.5, 91.5, 0.3, Plan{Explore: true, WasProbe: true, LastSide: 1}); d.Probe != 0 {
		t.Fatalf("two probes in a row: %+v", d)
	}
	if d := r.Next(90.5, 91.5, 0.3, Plan{Explore: true, Max: 91}); d.Probe != -1 {
		t.Fatalf("should try the other side within the range: %+v", d)
	}
	if d := r.Next(90.5, 91.5, 0.3, Plan{}); d.Probe != 0 || d.Hold {
		t.Fatalf("without exploring it follows the clicks: %+v", d)
	}
	if d := r.Next(90.5, 90.6, 0.3, Plan{}); !d.Hold || d.Width != 90.5 {
		t.Fatalf("a change within the noise should hold: %+v", d)
	}
}

// The reliability grows as you play and doesn't collapse when the best is
// near where you play: a width with few clicks, or the best moving among
// widths about as good, moves it only a little.
func TestReliabilityDoesNotCollapse(t *testing.T) {
	players := map[string]player{
		"misses more below 87": {land: func(w float64) float64 { return 0.5 * (84 - w) / w }, spread: func(w float64) float64 { return 0.3 * (1 + 0.2*math.Max(0, 87-w)) }},
		"small area":           {land: func(w float64) float64 { return 0.5 * (62 - w) / w }, spread: func(w float64) float64 { return 0.3 * (1 + 0.25*math.Max(0, 64-w)) }},
		"all widths alike":     {land: func(w float64) float64 { return 0.5 * (89 - w) / w }, spread: func(float64) float64 { return 0.3 }},
	}
	for name, p := range players {
		start := 92.0
		if name == "small area" {
			start = 67
		}
		for _, seed := range []int64{26, 52} {
			r := sculpt(t, p, start, 120, seed, true)
			drop, reached := 0.0, -1
			for i := 1; i < len(r.rels); i++ {
				if r.rels[i-1] >= 0.4 {
					drop = math.Max(drop, r.rels[i-1]-r.rels[i])
				}
				if reached < 0 && r.rels[i] >= Reliable {
					reached = i
				}
			}
			t.Logf("%s seed %d: reliable after %d stretches, biggest drop %.2f, final %.2f", name, seed, reached, drop, r.rep.Reliability)
			if reached < 0 || reached > 60 || drop > 0.2 || r.rep.Reliability < Reliable {
				t.Fatalf("%s seed %d: reliable after %d stretches, biggest drop %.2f, final %.2f", name, seed, reached, drop, r.rep.Reliability)
			}
		}
	}
}

func TestKeyForKeepsOneMemoryPerShape(t *testing.T) {
	s := newStore(t)
	rng := rand.New(rand.NewSource(14))
	p := player{land: func(float64) float64 { return 0 }, spread: func(float64) float64 { return 0.3 }}
	k := s.KeyFor("T", 0.625)
	s.Add(k, day0, "a", p.clicks(rng, 90.5, 10))
	for _, ratio := range []float64{0.62499, 0.62501, 0.6262} {
		if got := s.KeyFor("T", ratio); got != k {
			t.Fatalf("ratio %v: %q, want %q", ratio, got, k)
		}
	}
	if got := s.KeyFor("T", 0.5625); got == k {
		t.Fatal("another shape shares the memory")
	}
	if got := s.KeyFor("U", 0.625); got == k {
		t.Fatal("another tablet shares the memory")
	}
	// the memories of version 3.1 and 3.2 (two decimals) are found too
	s.Add("T|0.56", day0, "b", p.clicks(rng, 90.5, 10))
	if got := s.KeyFor("T", 0.5625); got != "T|0.56" {
		t.Fatalf("old memory not found: %q", got)
	}
}
