package filter

import (
	"math"
	"sort"
	"time"
)

// What the cursor does around the clicks, measured the same way on the
// movements recorded in a replay (with the filter that was on while it was
// played) and on the ones the simulation filters: how much it wobbles on the
// circle, whether it got there in time for the press, whether it went past.

// wobbleMs is how long before the press the cursor is looked at for its
// wobble.
const wobbleMs = 60.0

// wobble is how much the path wobbles on the circle just before the press:
// the length it runs beyond the straight line between its ends, over the
// radius. A still cursor, or one gliding straight across the circle, gives
// 0; a trembling one more. NaN when the path is not on the circle then.
func wobble(x, y []float32, w *Window, rate float64) float64 {
	n := len(x)
	k := int(math.Round(wobbleMs*rate/1000)) + 1
	if k < 4 {
		k = 4
	}
	if n < k || w.R <= 0 {
		return math.NaN()
	}
	r := float64(w.R)
	tx, ty := float64(w.TX), float64(w.TY)
	var run float64
	for i := n - k; i < n; i++ {
		if math.Hypot(float64(x[i])-tx, float64(y[i])-ty) > 2*r {
			return math.NaN()
		}
		if i > n-k {
			run += math.Hypot(float64(x[i]-x[i-1]), float64(y[i]-y[i-1]))
		}
	}
	chord := math.Hypot(float64(x[n-1]-x[n-k]), float64(y[n-1]-y[n-k]))
	return math.Max(0, run-chord) / r
}

func trimmedMean(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	k := int(math.Ceil(float64(len(s)) * 0.97))
	t := 0.0
	for _, x := range s[:k] {
		t += x
	}
	return t / float64(k)
}

// Measures are what the cursor did around the clicks of one play.
type Measures struct {
	N int `json:"n"`
	// wobble on the circle just before the press (share of the radius)
	Tremor float64 `json:"tremor"`
	// share of the clicks pressed less than lateMs after the cursor got into
	// the circle, or outside it
	Late float64 `json:"late"`
	// share of the clicks after going past the circle and back
	Overshoot float64 `json:"overshoot"`
}

// lateMs: a press this soon after the cursor got into the circle was a close
// call: a little more lag and it would have been outside.
const lateMs = 12.0

// Measure measures the cursor around the clicks of the windows of one play,
// as they were recorded.
func Measure(ws []Window, rate float64) Measures {
	var out Measures
	var trem []float64
	late := 0
	for i := range ws {
		w := &ws[i]
		n := len(w.X)
		if n < 6 || w.R <= 0 {
			continue
		}
		out.N++
		if t := wobble(w.X, w.Y, w, rate); !math.IsNaN(t) {
			trem = append(trem, t)
		}
		r := float64(w.R)
		in := func(j int) bool {
			return math.Hypot(float64(w.X[j]-w.TX), float64(w.Y[j]-w.TY)) <= r
		}
		// how long the cursor had been in the circle at the press
		j := n - 1
		for j >= 0 && in(j) {
			j--
		}
		dwell := float64(n-2-j) * 1000 / rate
		if !in(n-1) || dwell < lateMs {
			late++
		}
		best := math.Inf(-1)
		for k := n / 3; k < n; k++ {
			pr := (float64(w.X[k])-float64(w.TX))*float64(w.DirX) + (float64(w.Y[k])-float64(w.TY))*float64(w.DirY)
			best = math.Max(best, pr)
		}
		last := (float64(w.X[n-1])-float64(w.TX))*float64(w.DirX) + (float64(w.Y[n-1])-float64(w.TY))*float64(w.DirY)
		if best > 0.5*r && best-last > 0.25*r {
			out.Overshoot++
		}
	}
	if out.N > 0 {
		out.Late = float64(late) / float64(out.N)
		out.Overshoot /= float64(out.N)
	}
	out.Tremor = trimmedMean(trem)
	return out
}

/* ---------- the family of settings the coach moves along ---------- */

// Shape is a kind of Radial Follow, kept while its strength (the outer
// radius) changes: the inner radius as a share of the outer one (the part
// where the cursor stays still), and how it smooths.
type Shape struct {
	Inner float64 `json:"inner"` // share of the outer radius
	Coef  float64 `json:"coef"`
	Knee  float64 `json:"knee"`
	Leak  float64 `json:"leak"`
}

// Params is the setting of the shape with outer radius outer (mm).
func (s Shape) Params(outer float64) Params {
	return Params{Outer: outer, Inner: outer * s.Inner, Coef: s.Coef, Knee: s.Knee, Leak: s.Leak}
}

// FamilyShapes are the kinds tried: all with a still part (a deadzone that
// absorbs small pen jitter), more or less of it, smoothing more or less.
var FamilyShapes = func() []Shape {
	var out []Shape
	for _, in := range []float64{0.25, 0.4, 0.55} {
		for _, c := range []float64{0.3, 0.5, 0.7, 0.85} {
			out = append(out, Shape{Inner: in, Coef: c, Knee: 1, Leak: 0})
		}
	}
	return out
}()

// FamilyOuters are the strengths tried (outer radius, mm).
var FamilyOuters = func() []float64 {
	const n = 14
	lo, hi := 0.15, 3.0
	out := make([]float64, n)
	for i := range out {
		out[i] = round(lo*math.Pow(hi/lo, float64(i)/(n-1)), 3)
	}
	return out
}()

// Family is the simulation of every shape at every strength on the
// movements recorded without a filter, and of the settings played.
type Family struct {
	Rate    float64   `json:"rate"`
	N       int       `json:"n"` // clicks
	Plays   int       `json:"plays"`
	MissRaw float64   `json:"missRaw"` // clicks outside the circle per 1000, as played
	TremRaw float64   `json:"tremRaw"` // wobble as played (share of the radius)
	Outers  []float64 `json:"outers"`
	Shapes  []Shape   `json:"shapes"`
	Evals   [][]Eval  `json:"evals"` // [outer][shape]
	// the settings the player used, on the same movements
	Played []Eval `json:"played,omitempty"`
	// where the movements come from ("diagnosis": the maps of the diagnosis;
	// "replay": the profile's replays without a filter), and when
	Src  string    `json:"src,omitempty"`
	When time.Time `json:"when,omitempty"`
}

// StudyFamily simulates the family (and the settings in played) on the
// windows of ds (at most maxWin of them, spread over all).
func StudyFamily(ds *Dataset, played []Params, maxWin int) Family {
	f := Family{Rate: ds.Rate, Plays: ds.Plays, Outers: FamilyOuters, Shapes: FamilyShapes}
	idx := make([]int, len(ds.Windows))
	for i := range idx {
		idx[i] = i
	}
	if maxWin > 0 && len(idx) > maxWin {
		out := make([]int, 0, maxWin)
		step := float64(len(idx)) / float64(maxWin)
		for k := 0; k < maxWin; k++ {
			out = append(out, idx[int(float64(k)*step)])
		}
		idx = out
	}
	f.N = len(idx)
	f.Evals = make([][]Eval, len(f.Outers))
	for i, o := range f.Outers {
		f.Evals[i] = make([]Eval, len(f.Shapes))
		for k, s := range f.Shapes {
			if ds.Stop != nil && ds.Stop() {
				return Family{}
			}
			e := Evaluate(ds, s.Params(o), idx, 0, 0)
			f.Evals[i][k] = e
			if i == 0 && k == 0 {
				f.MissRaw, f.TremRaw = e.MissRaw, e.TremRaw
			}
		}
	}
	for _, p := range played {
		f.Played = append(f.Played, Evaluate(ds, p, idx, 0, 0))
	}
	return f
}

// OK tells whether the family was simulated.
func (f *Family) OK() bool {
	return f != nil && len(f.Evals) == len(f.Outers) && len(f.Outers) > 0 && f.N > 0
}

// At is the simulation of the setting p: the one played, if it was
// simulated, else the nearest shape, between the strengths around it. far
// is how unlike the shapes p is (0: one of them).
func (f *Family) At(p Params) (e Eval, far float64) {
	for _, x := range f.Played {
		if sameParams(x.Params, p) {
			return x, 0
		}
	}
	if !f.OK() || p.Outer <= 0 {
		return Eval{Params: p, MissRaw: f.MissRaw, Miss: f.MissRaw, TremRaw: f.TremRaw, Trem: f.TremRaw}, 1
	}
	share := p.Inner / p.Outer
	best, bd := 0, math.Inf(1)
	for k, s := range f.Shapes {
		d := math.Hypot((share-s.Inner)/0.15, (p.Coef-s.Coef)/0.2) + math.Abs(p.Knee-s.Knee)/2 + math.Abs(p.Leak-s.Leak)/0.2
		if d < bd {
			best, bd = k, d
		}
	}
	u := math.Log(p.Outer)
	lo := 0
	for lo+1 < len(f.Outers)-1 && math.Log(f.Outers[lo+1]) <= u {
		lo++
	}
	a, b := f.Evals[lo][best], f.Evals[lo+1][best]
	t := clamp((u-math.Log(f.Outers[lo]))/(math.Log(f.Outers[lo+1])-math.Log(f.Outers[lo])), 0, 1)
	mix := func(x, y float64) float64 { return x + t*(y-x) }
	e = Eval{Params: p, N: a.N, MissRaw: a.MissRaw, Miss: mix(a.Miss, b.Miss), Gain: mix(a.Gain, b.Gain), Delay: mix(a.Delay, b.Delay),
		Lost: mix(a.Lost, b.Lost), Won: mix(a.Won, b.Won), TremRaw: a.TremRaw, Trem: mix(a.Trem, b.Trem)}
	return e, math.Min(1, bd/3)
}

func sameParams(a, b Params) bool {
	near := func(x, y, tol float64) bool { return math.Abs(x-y) <= tol }
	return near(a.Outer, b.Outer, 0.006) && near(a.Inner, b.Inner, 0.006) && near(a.Coef, b.Coef, 0.006) &&
		near(a.Knee, b.Knee, 0.006) && near(a.Leak, b.Leak, 0.006)
}

// SameParams tells whether two settings are the same, as typed (two
// decimals).
func SameParams(a, b Params) bool { return sameParams(a, b) }
