package filter

import (
	"math"
	"math/rand"
	"testing"
)

func TestCoreLagBounded(t *testing.T) {
	c := NewCore(Params{Outer: 1, Inner: 0, Coef: 0.95, Knee: 1, Leak: 0})
	c.Reset(0, 0)
	var x float64
	for i := 0; i < 200; i++ {
		x += 2 // fast movement: 2 mm per report
		cx, _ := c.Step(x, 0)
		if x-cx > 1.0001 {
			t.Fatalf("lag %.4f beyond the outer radius", x-cx)
		}
	}
	// the pen stops: the cursor catches up
	for i := 0; i < 400; i++ {
		c.Step(x, 0)
	}
	cx, _ := c.Step(x, 0)
	if math.Abs(x-cx) > 0.01 {
		t.Fatalf("cursor did not settle: %.4f", x-cx)
	}
	// deadzone
	d := NewCore(Params{Outer: 1, Inner: 0.5, Coef: 0.5, Knee: 1})
	d.Reset(0, 0)
	if cx, _ := d.Step(0.3, 0); cx != 0 {
		t.Fatalf("inner radius should hold the cursor, moved to %.3f", cx)
	}
}

// synthetic movements: the pen reaches a point inside the circle but
// shakes around it; smoothing must recover clicks that the shake pushed out.
func TestOptimizeTremor(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	ds := &Dataset{Rate: 200, Plays: 40}
	for k := 0; k < 4000; k++ {
		r := 2.0
		dist := 15 + rng.Float64()*30
		a := rng.Float64() * 2 * math.Pi
		rest := math.Sqrt(rng.Float64()) * 0.8 * r
		ex, ey := rest*math.Cos(a), rest*math.Sin(a)
		n := 40
		w := Window{Play: k % 40, R: float32(r), DirX: 1}
		for i := 0; i < n; i++ {
			u := math.Min(1, float64(i)/float64(n-12))
			x := ex - dist*math.Pow(1-u, 2)
			y := ey
			if u >= 1 {
				x += rng.NormFloat64() * 0.9
				y += rng.NormFloat64() * 0.9
			}
			w.X = append(w.X, float32(x))
			w.Y = append(w.Y, float32(y))
		}
		ds.Windows = append(ds.Windows, w)
	}
	res := Optimize(ds, nil, nil)
	t.Logf("diagnosis %+v", res.Diagnosis)
	for _, o := range res.Options {
		t.Logf("%s: %+v gain %.1f [%.1f, %.1f] per 1000, delay %.2f ms (p90 %.1f) sure=%v", o.Name, o.Eval.Params, o.Eval.Gain, o.Eval.GainLo, o.Eval.GainHi, o.Eval.Delay, o.Eval.DelayHi, o.Sure)
	}
	t.Logf("default: gain %.1f delay %.2f; best=%s", res.Default.Gain, res.Default.Delay, res.Best)
	if res.Best == "none" {
		t.Errorf("a shaking hand should benefit from smoothing")
	}
}

// the pen overshoots and the click happens on the way back: smoothing makes
// the cursor come back later, so no filter must be recommended.
func TestOptimizeNoHelp(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	ds := &Dataset{Rate: 200, Plays: 40}
	for k := 0; k < 3000; k++ {
		r := 2.0
		dist := 15 + rng.Float64()*30
		over := (1.1 + rng.NormFloat64()*0.4) * r
		n := 40
		w := Window{Play: k % 40, R: float32(r), DirX: 1}
		for i := 0; i < n; i++ {
			u := float64(i) / float64(n-1)
			var x float64
			if u < 0.8 {
				x = -dist + (dist+over)*(1-math.Pow(1-u/0.8, 2))
			} else {
				x = over - (over*0.2)*(u-0.8)/0.2
			}
			w.X = append(w.X, float32(x))
			w.Y = append(w.Y, 0)
		}
		ds.Windows = append(ds.Windows, w)
	}
	res := Optimize(ds, nil, nil)
	for _, o := range res.Options {
		t.Logf("%s: %+v gain %.1f [%.1f, %.1f], delay %.2f ms sure=%v", o.Name, o.Eval.Params, o.Eval.Gain, o.Eval.GainLo, o.Eval.GainHi, o.Eval.Delay, o.Sure)
	}
	t.Logf("best=%s default gain %.1f", res.Best, res.Default.Gain)
	// with little lag nothing helps; only the smoothest level, with a lot of
	// lag, may
	if res.Best != "none" && res.Best != "smooth" {
		t.Errorf("no low-latency filter should be recommended here, got %s", res.Best)
	}
	for _, o := range res.Options {
		if o.Key != "smooth" && o.Sure && o.Eval.Delay < 4 {
			t.Errorf("%s should not help here: %+v", o.Name, o.Eval)
		}
	}
}
