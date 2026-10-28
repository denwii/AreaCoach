package analysis

import (
	"math"
	"math/rand"
	"testing"
	"time"

	"areacoach/internal/otd"
)

// When the areas set never reach the tablet the measures stay the same
// whatever the size; when they do, a smaller area makes the jumps longer.
func TestAreaReaches(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	t0 := time.Now()
	mk := func(g func(w float64) float64, ws []float64) []CalibPoint {
		var out []CalibPoint
		for i, w := range ws {
			out = append(out, CalibPoint{Area: otd.Rect{W: w, H: w * 0.56, X: 76, Y: 47}, G: g(w) + rng.NormFloat64()*0.02, SG: 0.03,
				At: t0.Add(time.Duration(i) * 10 * time.Second), Src: "live"})
		}
		return out
	}
	shrink := []float64{152, 129, 116, 97, 80, 70, 62, 58, 40, 39, 38}
	if known, ok := AreaReaches(mk(func(float64) float64 { return -0.18 }, shrink), time.Time{}); !known || ok {
		t.Errorf("areas that change nothing: known %v reaches %v", known, ok)
	}
	if known, ok := AreaReaches(mk(func(w float64) float64 { return math.Log(0.35*(62/w-1) + 1) }, shrink), time.Time{}); !known || !ok {
		t.Errorf("areas that work: known %v reaches %v", known, ok)
	}
	if known, _ := AreaReaches(mk(func(w float64) float64 { return 0 }, []float64{152, 140, 130, 120, 110, 100, 95}), time.Time{}); known {
		t.Errorf("a small range of sizes should not be judged")
	}
}
