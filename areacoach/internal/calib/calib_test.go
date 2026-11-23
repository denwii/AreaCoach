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

func (p player) clicks(rng *rand.Rand, w float64, n int) []Click {
	const r = 36.0
	out := make([]Click, 0, n)
	for i := 0; i < n; i++ {
		j := 0.5 + rng.Float64()*9.5
		a := rng.Float64() * 2 * math.Pi
		mx, my := j*r*math.Cos(a), j*r*math.Sin(a)
		s := p.spread(w) * (1 + 0.04*j) * r
		par := p.land(w)*j*r + rng.NormFloat64()*s
		perp := rng.NormFloat64() * s
		ex := par*math.Cos(a) - perp*math.Sin(a)
		ey := par*math.Sin(a) + perp*math.Cos(a)
		out = append(out, Click{Width: w, Dist: math.Hypot(ex, ey) / r, ErrX: ex, ErrY: ey, Radius: r, MoveX: mx, MoveY: my, Gap: 100 + rng.Float64()*350})
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
	d := r.Decide(90, 84)
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
	d := r.Decide(88, 83)
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
	d := r.Decide(88.2, 80)
	if d.Width > 87.9 || d.Width < 86.5 || d.Factor > 0.5 {
		t.Fatalf("past the edge: %+v", d)
	}
	in := r.Decide(90, 91)
	if in.Width != 91 || in.Factor < 0.6 {
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

// sculpt plays rounds of 40 clicks at the width the decisions lead to.
func sculpt(t *testing.T, p player, start float64, rounds int, seed int64) (last, lowest float64) {
	t.Helper()
	s := newStore(t)
	rng := rand.New(rand.NewSource(seed))
	w, lowest := start, start
	var sum float64
	n := 0
	for i := 0; i < rounds; i++ {
		day := day0.Add(time.Duration(i/100) * 24 * time.Hour)
		cs := p.clicks(rng, w, 40)
		s.Add("k", day, fmt.Sprint(i/3), cs)
		// what the last clicks ask for: as much smaller as they land short
		fit := w * (1 + p.land(w) + rng.NormFloat64()*0.01)
		d := s.Analyze("k", day).Decide(w, fit)
		step := 0.4 * d.Factor * (d.Width - w)
		lim := 0.012 * w * d.Factor
		w += math.Max(-lim, math.Min(lim, step))
		lowest = math.Min(lowest, w)
		if i >= rounds-100 {
			sum += w
			n++
		}
	}
	return sum / float64(n), lowest
}

// Your case: the hand pulls towards 84 mm, but below about 87 you scatter
// more. It goes down, and settles above where the misses grow.
func TestSculptStopsWhereMissesGrow(t *testing.T) {
	p := player{
		land:   func(w float64) float64 { return 0.5 * (84 - w) / w },
		spread: func(w float64) float64 { return 0.3 * (1 + 0.2*math.Max(0, 87-w)) },
	}
	last, lowest := sculpt(t, p, 92, 300, 6)
	t.Logf("settled around %.2f, lowest %.2f", last, lowest)
	if last < 86 || last > 89.5 || lowest < 83.5 {
		t.Fatalf("settled around %.2f, lowest %.2f", last, lowest)
	}
}

// Someone else: the hand pulls towards 84 mm and the small area costs
// nothing: it goes all the way.
func TestSculptGoesWhereTheHandIs(t *testing.T) {
	p := player{
		land:   func(w float64) float64 { return 0.5 * (84 - w) / w },
		spread: func(float64) float64 { return 0.3 },
	}
	last, _ := sculpt(t, p, 92, 300, 7)
	t.Logf("settled around %.2f", last)
	if last > 86 {
		t.Fatalf("held back for nothing: %.2f", last)
	}
}

// And someone who needs a bigger area.
func TestSculptGrowsToo(t *testing.T) {
	p := player{
		land:   func(w float64) float64 { return 0.5 * (100 - w) / w },
		spread: func(w float64) float64 { return 0.3 },
	}
	last, _ := sculpt(t, p, 92, 300, 8)
	t.Logf("settled around %.2f", last)
	if last < 98 {
		t.Fatalf("didn't grow: %.2f", last)
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
	rng := rand.New(rand.NewSource(66))
	w := 92.0
	now := time.Now()
	for i := 0; i < 160; i++ {
		day := now.Add(-time.Duration(1-i/80) * 24 * time.Hour)
		s.Add(Key("Wacom CTL-472", 0.5625), day, fmt.Sprint(i/3), p.clicks(rng, w, 40))
		fit := w * (1 + p.land(w) + rng.NormFloat64()*0.01)
		d := s.Analyze(Key("Wacom CTL-472", 0.5625), day).Decide(w, fit)
		step := 0.4 * d.Factor * (d.Width - w)
		lim := 0.012 * w * d.Factor
		w += math.Max(-lim, math.Min(lim, step))
	}
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
