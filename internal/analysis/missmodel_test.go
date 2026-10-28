package analysis

import (
	"math"
	"math/rand"
	"testing"
)

// The width with the fewest aim misses moves away from the one the hand is
// used to only when the pen scatter says so: shaky hands want more area,
// hands that struggle with speed want less, and a hand that keeps most of
// any mismatch stays close to its own size.
func TestMissModelBest(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	var pts []predPoint
	for i := 0; i < 2500; i++ {
		D := 3 + rng.Float64()*45 // mm at the reference width (100)
		pts = append(pts, predPoint{cat: uint8(rng.Intn(NumCats - 1)), D: D, V: D / (90 + rng.Float64()*260), R: 3.2 + rng.Float64()*1.5})
	}
	var w [NumCats]float64
	for i := range w {
		w[i] = 1
	}
	best := func(m NoiseModel, nat, persist float64) (float64, float64, float64) {
		mm := newMissModel(NoiseFit{Model: m}, append([]predPoint(nil), pts...), w, 100, 100)
		b, miss, missNat, ok := mm.Best(nat, persist, 30, 152)
		if !ok {
			t.Fatal("no result")
		}
		return b, miss, missNat
	}
	shaky := NewNoiseModel(1.4, 0.01, 0.05, 1.2, 0.008, 0.04, 2, 5)
	fast := NewNoiseModel(0.05, 0.01, 14, 0.04, 0.008, 10, 2, 5)
	b1, m1, n1 := best(shaky, 60, 0.15)
	b2, m2, n2 := best(fast, 60, 0.15)
	b3, _, _ := best(shaky, 60, 1)
	t.Logf("shaky: %.1f mm (%.1f vs %.1f misses), fast: %.1f mm (%.1f vs %.1f), shaky but keeps the mismatch: %.1f mm", b1, m1, n1, b2, m2, n2, b3)
	if b1 <= 61.5 {
		t.Errorf("a shaky hand should get more area than 60 mm: %.1f", b1)
	}
	if b2 >= 60 {
		t.Errorf("a hand slow on big jumps should get less area than 60 mm: %.1f", b2)
	}
	if m1 > n1 || m2 > n2 {
		t.Errorf("the width chosen has more misses than the natural one")
	}
	if b3 > b1 {
		t.Errorf("keeping more of the mismatch should stay closer: %.1f vs %.1f", b3, b1)
	}
}

// The width with the fewest predicted aim misses, given what the diagnosis
// measured: a habit of jumping short that no width fixes does not shrink it,
// a hand found shakier on small areas gets a wider one, and jumps that
// follow the width keep it near the size the hand is used to.
func TestMissModelBestFor(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	var pts []predPoint
	for i := 0; i < 2500; i++ {
		D := 3 + rng.Float64()*45
		pts = append(pts, predPoint{cat: uint8(rng.Intn(NumCats - 1)), D: D, V: D / (90 + rng.Float64()*260), R: 3.2 + rng.Float64()*1.5})
	}
	var w [NumCats]float64
	for i := range w {
		w[i] = 1
	}
	m := newMissModel(NoiseFit{Model: NewNoiseModel(0.7, 0.01, 5, 0.6, 0.008, 3.5, 2, 5)}, pts, w, 100, 80)
	plan := func(a, b float64, cal PrecCal) float64 {
		p, ok := m.BestFor(a, b, cal, 80, 40, 152)
		if !ok {
			t.Fatal("no result")
		}
		if got := m.MissAt(p.W, a, b, cal); got != p.Miss {
			t.Errorf("misses at %.1f: %.2f, the plan says %.2f", p.W, got, p.Miss)
		}
		return p.W
	}
	base := plan(0, 0, PrecCal{})
	habit := plan(-0.04, 0, PrecCal{})
	shaky := plan(0, 0, PrecCal{Gamma: -1.5, Lo: 50, Hi: 120, N: 30})
	follows := plan(0.75*math.Log(60), 0.75, PrecCal{})
	t.Logf("no bias %.1f mm, 4%% short by habit %.1f, shakier on small areas %.1f, hand used to 60 mm %.1f", base, habit, shaky, follows)
	// jumping short whatever the width: a smaller area does not fix it (only
	// a steadier hand helps the circles it barely reaches)
	if habit < base-2 {
		t.Errorf("a habit no width fixes shrank the width from %.1f to %.1f", base, habit)
	}
	if shaky < base+3 {
		t.Errorf("a hand shakier on small areas got %.1f mm, %.1f without", shaky, base)
	}
	if follows < 50 || follows > 75 {
		t.Errorf("a hand used to 60 mm got %.1f", follows)
	}
}
