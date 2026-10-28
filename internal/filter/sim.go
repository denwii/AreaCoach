package filter

import (
	"math"
	"math/rand"
	"runtime"
	"sort"
	"sync"
)

// Window is the pen path before one click, sampled at the tablet report
// rate. Positions are tablet millimetres; the last sample is the press.
type Window struct {
	Play   int
	X, Y   []float32
	TX, TY float32 // circle centre
	R      float32 // circle radius
	DirX   float32 // direction of the movement into the circle (unit)
	DirY   float32
}

// Dataset is every window used by the simulation.
type Dataset struct {
	Rate    float64 // tablet reports per second
	Windows []Window
	Plays   int
	Areas   int
	// Workers limits the threads used (0: up to 8); Stop, when it returns
	// true, ends the search early (the result is then marked Stopped)
	Workers int
	Stop    func() bool
}

// Eval is the effect of one setting on the recorded movements.
type Eval struct {
	Params  Params  `json:"params"`
	N       int     `json:"n"`
	MissRaw float64 `json:"missRaw"` // clicks outside the circle per 1000, as played
	Miss    float64 `json:"miss"`    // with the filter
	Gain    float64 `json:"gain"`    // clicks moved inside per 1000 (negative: worse)
	GainLo  float64 `json:"gainLo"`
	GainHi  float64 `json:"gainHi"`
	ErrRaw  float64 `json:"errRaw"` // mean distance from the centre / radius
	Err     float64 `json:"err"`
	Delay   float64 `json:"delay"`   // mean extra time to reach the circle (ms)
	DelayHi float64 `json:"delayHi"` // 90th percentile
	// clicks the filter moved out of the circle (late, mostly) and into it,
	// per 1000
	Lost float64 `json:"lost,omitempty"`
	Won  float64 `json:"won,omitempty"`
	// how much the cursor wobbles on the circle just before the press, as
	// played and with the filter (share of the radius; see Wobble)
	TremRaw float64 `json:"tremRaw,omitempty"`
	Trem    float64 `json:"trem,omitempty"`
}

type winResult struct {
	rawIn, in bool
	rawErr    float64
	err       float64
	delay     float64 // ms, NaN if not measurable
	// wobble on the circle before the press, as played and filtered (NaN:
	// not on the circle then)
	tremRaw, trem float64
}

// simulate runs the filter over one window.
func simulate(w *Window, p Params, rate float64) winResult {
	n := len(w.X)
	core := NewCore(p)
	core.Reset(float64(w.X[0]), float64(w.Y[0]))
	tx, ty, r := float64(w.TX), float64(w.TY), float64(w.R)
	var res winResult
	rawEnter, enter := math.NaN(), math.NaN()
	prevRaw, prevF := math.Inf(1), math.Inf(1)
	var cx, cy float64
	fx := make([]float32, n)
	fy := make([]float32, n)
	for i := 0; i < n; i++ {
		px, py := float64(w.X[i]), float64(w.Y[i])
		if i == 0 {
			cx, cy = px, py
		} else {
			cx, cy = core.Step(px, py)
		}
		fx[i], fy[i] = float32(cx), float32(cy)
		dr := math.Hypot(px-tx, py-ty)
		df := math.Hypot(cx-tx, cy-ty)
		// first entry into the circle, interpolated between reports
		if math.IsNaN(rawEnter) && dr <= r && i > 0 && prevRaw > r {
			rawEnter = float64(i-1) + (prevRaw-r)/math.Max(prevRaw-dr, 1e-9)
		}
		if math.IsNaN(enter) && df <= r && i > 0 && prevF > r {
			enter = float64(i-1) + (prevF-r)/math.Max(prevF-df, 1e-9)
		}
		prevRaw, prevF = dr, df
		if i == n-1 {
			res.rawIn, res.in = dr <= r, df <= r
			res.rawErr, res.err = math.Min(dr/r, 3), math.Min(df/r, 3)
		}
	}
	res.delay = math.NaN()
	if !math.IsNaN(rawEnter) && rawEnter < float64(n-2) {
		e := enter
		if math.IsNaN(e) || e < rawEnter {
			if math.IsNaN(e) {
				e = float64(n - 1)
			} else {
				e = rawEnter
			}
		}
		res.delay = (e - rawEnter) * 1000 / rate
	}
	res.tremRaw = wobble(w.X, w.Y, w, rate)
	res.trem = wobble(fx, fy, w, rate)
	return res
}

// Evaluate runs a setting over the windows in idx (all if nil).
func Evaluate(ds *Dataset, p Params, idx []int, boot int, seed int64) Eval {
	if idx == nil {
		idx = make([]int, len(ds.Windows))
		for i := range idx {
			idx[i] = i
		}
	}
	res := make([]winResult, len(idx))
	workers := runtime.NumCPU()
	if workers > 8 {
		workers = 8
	}
	if ds.Workers > 0 && ds.Workers < workers {
		workers = ds.Workers
	}
	var wg sync.WaitGroup
	chunk := (len(idx) + workers - 1) / workers
	for w := 0; w < workers; w++ {
		lo, hi := w*chunk, (w+1)*chunk
		if hi > len(idx) {
			hi = len(idx)
		}
		if lo >= hi {
			break
		}
		wg.Add(1)
		go func(lo, hi int) {
			defer wg.Done()
			for k := lo; k < hi; k++ {
				res[k] = simulate(&ds.Windows[idx[k]], p, ds.Rate)
			}
		}(lo, hi)
	}
	wg.Wait()
	e := Eval{Params: p, N: len(idx)}
	if len(idx) == 0 {
		return e
	}
	type acc struct{ n, raw, f int }
	perPlay := map[int]*acc{}
	var delays []float64
	missRaw, miss, lost, won := 0, 0, 0, 0
	var se, sr float64
	var trs, trf []float64
	for k, r := range res {
		if r.rawIn && !r.in {
			lost++
		}
		if !r.rawIn && r.in {
			won++
		}
		if !math.IsNaN(r.tremRaw) && !math.IsNaN(r.trem) {
			trs = append(trs, r.tremRaw)
			trf = append(trf, r.trem)
		}
		pl := ds.Windows[idx[k]].Play
		a := perPlay[pl]
		if a == nil {
			a = &acc{}
			perPlay[pl] = a
		}
		a.n++
		if !r.rawIn {
			missRaw++
			a.raw++
		}
		if !r.in {
			miss++
			a.f++
		}
		sr += r.rawErr
		se += r.err
		if !math.IsNaN(r.delay) {
			delays = append(delays, r.delay)
		}
	}
	n := float64(len(idx))
	e.MissRaw = 1000 * float64(missRaw) / n
	e.Miss = 1000 * float64(miss) / n
	e.Gain = e.MissRaw - e.Miss
	e.Lost, e.Won = 1000*float64(lost)/n, 1000*float64(won)/n
	e.TremRaw, e.Trem = trimmedMean(trs), trimmedMean(trf)
	e.ErrRaw, e.Err = sr/n, se/n
	if len(delays) > 0 {
		sort.Float64s(delays)
		// trimmed mean: a few clicks that never reach the circle should not
		// dominate the latency
		k := int(float64(len(delays)) * 0.97)
		if k < 1 {
			k = 1
		}
		t := 0.0
		for _, d := range delays[:k] {
			t += d
		}
		e.Delay = t / float64(k)
		e.DelayHi = delays[int(float64(len(delays)-1)*0.9)]
	}
	// uncertainty of the gain: resample whole plays
	if boot > 0 && len(perPlay) >= 3 {
		plays := make([]*acc, 0, len(perPlay))
		for _, a := range perPlay {
			plays = append(plays, a)
		}
		rng := rand.New(rand.NewSource(seed))
		var gs []float64
		for b := 0; b < boot; b++ {
			var tn, tr, tf int
			for range plays {
				a := plays[rng.Intn(len(plays))]
				tn += a.n
				tr += a.raw
				tf += a.f
			}
			if tn > 0 {
				gs = append(gs, 1000*float64(tr-tf)/float64(tn))
			}
		}
		sort.Float64s(gs)
		e.GainLo = gs[int(float64(len(gs)-1)*0.05)]
		e.GainHi = gs[int(float64(len(gs)-1)*0.95)]
	} else {
		e.GainLo, e.GainHi = e.Gain, e.Gain
	}
	return e
}

// Diagnosis describes the movements the filter acts on.
type Diagnosis struct {
	Overshoot float64 `json:"overshoot"` // share of clicks after going past the circle and back
	Tremor    float64 `json:"tremor"`    // pen movement per report just before the press (mm)
	Moving    float64 `json:"moving"`    // share of clicks made while still moving fast
	MissRaw   float64 `json:"missRaw"`
}

// Diagnose measures overshoot and tremor on the raw windows.
func Diagnose(ds *Dataset) Diagnosis {
	var d Diagnosis
	n := 0
	var trem float64
	tn := 0
	for i := range ds.Windows {
		w := &ds.Windows[i]
		m := len(w.X)
		if m < 6 {
			continue
		}
		n++
		r := float64(w.R)
		// furthest point past the centre along the movement, after the start
		best := math.Inf(-1)
		for k := m / 3; k < m; k++ {
			pr := (float64(w.X[k])-float64(w.TX))*float64(w.DirX) + (float64(w.Y[k])-float64(w.TY))*float64(w.DirY)
			best = math.Max(best, pr)
		}
		last := (float64(w.X[m-1])-float64(w.TX))*float64(w.DirX) + (float64(w.Y[m-1])-float64(w.TY))*float64(w.DirY)
		if best > 0.5*r && best-last > 0.25*r {
			d.Overshoot++
		}
		// tremor: movement per report in the last reports
		k0 := m - 4
		for k := k0 + 1; k < m; k++ {
			trem += math.Hypot(float64(w.X[k]-w.X[k-1]), float64(w.Y[k]-w.Y[k-1]))
			tn++
		}
		v := math.Hypot(float64(w.X[m-1]-w.X[m-2]), float64(w.Y[m-1]-w.Y[m-2])) * ds.Rate // mm/s
		if v > 150 {
			d.Moving++
		}
		if math.Hypot(float64(w.X[m-1]-w.TX), float64(w.Y[m-1]-w.TY)) > r {
			d.MissRaw++
		}
	}
	if n > 0 {
		d.Overshoot /= float64(n)
		d.Moving /= float64(n)
		d.MissRaw = 1000 * d.MissRaw / float64(n)
	}
	if tn > 0 {
		d.Tremor = trem / float64(tn)
	}
	return d
}

// Option is one recommended setting for a latency budget.
type Option struct {
	Key    string  `json:"key"`
	Name   string  `json:"name"`
	Budget float64 `json:"budget"` // max mean extra latency (ms)
	Eval   Eval    `json:"eval"`   // on the plays not used to choose it
	Sure   bool    `json:"sure"`   // the gain is clearly above zero
}

// Result is the full filter study.
type Result struct {
	Rate    float64  `json:"rate"`
	Notes   int      `json:"notes"`
	Plays   int      `json:"plays"`
	Areas   int      `json:"areas"`
	Options []Option `json:"options"`
	// for each latency budget, the best setting that does filter (outer
	// radius above zero): what to try in game even when the simulation says
	// no filter helps, since it cannot know how the hand changes with one
	Tries     []Option  `json:"tries"`
	Best      string    `json:"best"` // key of the recommended option, "none" if no filter helps
	Frontier  []Eval    `json:"frontier"`
	Diagnosis Diagnosis `json:"diagnosis"`
	Current   *Eval     `json:"current"` // the settings in use now, on the same data
	Default   Eval      `json:"default"` // the plugin's defaults
	Stopped   bool      `json:"stopped,omitempty"`
}

var (
	gridOuter = []float64{0, 0.1, 0.2, 0.3, 0.45, 0.6, 0.8, 1.0, 1.3, 1.7, 2.2, 3.0}
	gridInner = []float64{0, 0.1, 0.25, 0.4, 0.6, 0.8} // share of the outer radius
	gridCoef  = []float64{0.1, 0.3, 0.5, 0.65, 0.8, 0.9, 0.95, 0.98}
	gridKnee  = []float64{0, 0.25, 0.5, 1, 2, 4}
	gridLeak  = []float64{0, 0.1, 0.25, 0.5}
)

type cand struct{ o, i, c, k, l int }

// smoothGrid are settings with a deadzone (inner radius) that is a good part
// of the outer one, more or less smoothing.
func smoothGrid() []cand {
	var out []cand
	for _, o := range []int{3, 5, 7, 8, 9, 10} {
		for _, i := range []int{3, 4, 5} {
			for _, c := range []int{1, 2, 4} {
				out = append(out, cand{o, i, c, 3, 0})
			}
		}
	}
	return out
}

// candLess orders the settings, so that ties are broken the same way every
// time.
func candLess(a, b cand) bool {
	for _, d := range [][2]int{{a.o, b.o}, {a.i, b.i}, {a.c, b.c}, {a.k, b.k}, {a.l, b.l}} {
		if d[0] != d[1] {
			return d[0] < d[1]
		}
	}
	return false
}

func (c cand) params() Params {
	o := gridOuter[c.o]
	return Params{Outer: o, Inner: round(o*gridInner[c.i], 3), Coef: gridCoef[c.c], Knee: gridKnee[c.k], Leak: gridLeak[c.l]}
}

func round(x float64, d int) float64 {
	p := math.Pow(10, float64(d))
	return math.Round(x*p) / p
}

// Optimize searches the settings with the most clicks inside the circles
// for each latency budget. Settings are chosen on half of the plays and
// judged on the other half, so the gains shown are not flattered by luck.
func Optimize(ds *Dataset, current *Params, progress func(float64)) Result {
	res := Result{Rate: ds.Rate, Notes: len(ds.Windows), Plays: ds.Plays, Areas: ds.Areas}
	res.Diagnosis = Diagnose(ds)
	var train, test []int
	for i, w := range ds.Windows {
		// split by a hash of the play, so that the two halves get a fair mix
		// of maps whatever order the plays came in
		if (uint32(w.Play)*2654435761)>>31 == 0 {
			train = append(train, i)
		} else {
			test = append(test, i)
		}
	}
	if len(train) < 300 || len(test) < 300 {
		// too few plays to split: use everything for both
		train, test = nil, nil
	}
	sub := func(idx []int, max int) []int {
		if idx == nil {
			idx = make([]int, len(ds.Windows))
			for i := range idx {
				idx[i] = i
			}
		}
		if len(idx) <= max {
			return idx
		}
		out := make([]int, 0, max)
		step := float64(len(idx)) / float64(max)
		for k := 0; k < max; k++ {
			out = append(out, idx[int(float64(k)*step)])
		}
		return out
	}
	trainS := sub(train, 5000)
	memo := map[cand]Eval{}
	evals := 0
	eval := func(c cand) Eval {
		if e, ok := memo[c]; ok {
			return e
		}
		e := Evaluate(ds, c.params(), trainS, 0, 0)
		memo[c] = e
		evals++
		return e
	}
	budgets := []struct {
		key, name string
		b         float64
	}{{"min", "Lowest latency", 1}, {"bal", "Balanced", 3}, {"stable", "Steadier", 6}, {"smooth", "Smoothest", 12}}
	score := func(e Eval, b float64) float64 {
		return e.Gain - 50*math.Max(0, e.Delay-b) - 0.05*e.Delay
	}
	starts := []cand{{0, 0, 6, 3, 0}, {7, 0, 6, 3, 0}, {3, 0, 2, 3, 0}, {5, 2, 4, 2, 0}}
	total := float64(len(budgets) * len(starts) * 3 * (len(gridOuter) + len(gridInner) + len(gridCoef) + len(gridKnee) + len(gridLeak)))
	done := 0.0
	for bi, bd := range budgets {
		best, bestS := cand{}, math.Inf(-1)
		for _, st := range starts {
			cur := st
			curS := score(eval(cur), bd.b)
			for pass := 0; pass < 3; pass++ {
				improved := false
				for dim := 0; dim < 5; dim++ {
					size := []int{len(gridOuter), len(gridInner), len(gridCoef), len(gridKnee), len(gridLeak)}[dim]
					for v := 0; v < size; v++ {
						c := cur
						switch dim {
						case 0:
							c.o = v
						case 1:
							c.i = v
						case 2:
							c.c = v
						case 3:
							c.k = v
						case 4:
							c.l = v
						}
						if s := score(eval(c), bd.b); s > curS+1e-9 {
							cur, curS, improved = c, s, true
						}
						done++
						if progress != nil {
							progress(math.Min(0.95, done/total))
						}
						if ds.Stop != nil && ds.Stop() {
							res.Stopped = true
							return res
						}
					}
				}
				if !improved {
					break
				}
			}
			if curS > bestS {
				best, bestS = cur, curS
			}
		}
		e := Evaluate(ds, best.params(), test, 300, int64(41+bi))
		opt := Option{Key: bd.key, Name: bd.name, Budget: bd.b, Eval: e}
		opt.Sure = e.GainLo > 0 && e.Gain >= 0.3
		res.Options = append(res.Options, opt)
		if best.o > 0 && eval(best).Delay >= 0.4*bd.b && (bd.key != "smooth" || gridInner[best.i] >= 0.3) {
			res.Tries = append(res.Tries, opt)
			continue
		}
		// something to try in game at about this latency even if the
		// simulation says it does not help (it cannot know how the hand
		// changes with the filter on): among the settings that do filter
		// with about this much lag, the one with most clicks in the circle
		for _, c := range smoothGrid() {
			eval(c)
		}
		var on cand
		onS := math.Inf(-1)
		for c, ev := range memo {
			if c.o == 0 || ev.Delay > bd.b || ev.Delay < 0.4*bd.b {
				continue
			}
			if bd.key == "smooth" && gridInner[c.i] < 0.3 {
				// the smoothest level: a deadzone where the cursor stays still
				continue
			}
			if sc := ev.Gain; sc > onS || (sc == onS && candLess(c, on)) {
				on, onS = c, sc
			}
		}
		if onS > math.Inf(-1) {
			e := Evaluate(ds, on.params(), test, 300, int64(61+bi))
			res.Tries = append(res.Tries, Option{Key: bd.key, Name: bd.name, Budget: bd.b, Eval: e, Sure: e.GainLo > 0 && e.Gain >= 0.3})
		}
	}
	// the gain/latency cloud for the chart: the best gain at each latency
	var all []Eval
	for _, e := range memo {
		all = append(all, e)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Delay < all[j].Delay })
	bestG := math.Inf(-1)
	for _, e := range all {
		if e.Gain > bestG+0.05 {
			res.Frontier = append(res.Frontier, e)
			bestG = e.Gain
		}
	}
	// recommendation: lowest latency first, a slower option only if it
	// clearly gives more
	res.Best = "none"
	for _, o := range res.Options {
		if !o.Sure {
			continue
		}
		if res.Best == "none" {
			res.Best = o.Key
			continue
		}
		var cur Option
		for _, x := range res.Options {
			if x.Key == res.Best {
				cur = x
			}
		}
		if o.Eval.Gain > cur.Eval.Gain*1.5+0.5 && o.Eval.GainLo > cur.Eval.Gain {
			res.Best = o.Key
		}
	}
	res.Default = Evaluate(ds, Params{Outer: 1, Inner: 0, Coef: 0.95, Knee: 1, Leak: 0}, test, 200, 7)
	if current != nil {
		e := Evaluate(ds, *current, test, 200, 9)
		res.Current = &e
	}
	if progress != nil {
		progress(1)
	}
	return res
}
