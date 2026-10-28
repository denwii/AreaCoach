package analysis

import (
	"math"
	"math/rand"
)

// NoiseModel describes how much the pen scatters around the intended point
// (after removing the systematic bias), in mm:
//
//	σ² = a² + (b·D)² + (c·V^p)²
//
// along the movement (par) and across it (perp). D is the movement length in
// mm, V the required speed in m/s (= mm/ms). a is the "steadiness" term that
// does not depend on the movement: it penalises small areas. A super-linear
// speed term (p > 1) penalises large areas, which need faster hands.
type NoiseModel struct {
	APar, BPar, CPar    float64
	APerp, BPerp, CPerp float64
	P                   float64
	N                   int
	VMax                float64 // 97th percentile of observed speed
	DMax                float64
	ok                  bool
}

// OK reports whether the model was fitted.
func (m NoiseModel) OK() bool { return m.ok }

// Sigma returns (σpar, σperp) for a movement.
func (m NoiseModel) Sigma(D, V float64) (float64, float64) {
	vp := math.Pow(math.Max(V, 0), m.P)
	sp := math.Sqrt(m.APar*m.APar + m.BPar*m.BPar*D*D + m.CPar*m.CPar*vp*vp)
	sq := math.Sqrt(m.APerp*m.APerp + m.BPerp*m.BPerp*D*D + m.CPerp*m.CPerp*vp*vp)
	return sp, sq
}

const (
	nDBins = 18
	nVBins = 18
)

type noiseCell struct {
	n           float64
	sumD, sumV  float64
	sPar, sPerp float64 // sum of squared residuals
}

type noiseGrid [nDBins * nVBins]noiseCell

func dBin(D float64) int {
	// 0.3 mm .. 120 mm, log spaced
	b := int(math.Floor((math.Log(math.Max(D, 0.3)) - math.Log(0.3)) / (math.Log(120) - math.Log(0.3)) * nDBins))
	if b < 0 {
		b = 0
	}
	if b >= nDBins {
		b = nDBins - 1
	}
	return b
}

func vBin(V float64) int {
	// 0.005 .. 4 m/s
	b := int(math.Floor((math.Log(math.Max(V, 0.005)) - math.Log(0.005)) / (math.Log(4) - math.Log(0.005)) * nVBins))
	if b < 0 {
		b = 0
	}
	if b >= nVBins {
		b = nVBins - 1
	}
	return b
}

type noiseObs struct {
	D, V        float64
	rPar, rPerp float64
	cluster     int
}

// collectNoise computes residuals after the bias model, decomposed along
// and across the movement direction.
func collectNoise(samples []*Sample, biasFor func(*Sample) *BiasFit, cluster func(*Sample) int) []noiseObs {
	var obs []noiseObs
	for _, s := range samples {
		if !s.HasMM || !isAimHit(s) || !s.HasOrigin() || !finite(s.DtReal) || s.DtReal <= 0 {
			continue
		}
		bias := biasFor(s)
		if bias == nil || !bias.OK() {
			continue
		}
		px, py := bias.Predict(s)
		ex := s.Cx - s.Tx - px
		ey := s.Cy - s.Ty - py
		mx, my := s.Tx-s.Ox, s.Ty-s.Oy
		D := math.Hypot(mx, my)
		var par, perp float64
		if D >= 1.5 {
			ux, uy := mx/D, my/D
			par = ex*ux + ey*uy
			perp = -ex*uy + ey*ux
		} else {
			par, perp = ex, ey
		}
		obs = append(obs, noiseObs{D: D, V: D / s.DtReal, rPar: par, rPerp: perp, cluster: cluster(s)})
	}
	return obs
}

func gridOf(obs []noiseObs, capPar func(o *noiseObs) (float64, float64)) map[int]*noiseGrid {
	grids := map[int]*noiseGrid{}
	for i := range obs {
		o := &obs[i]
		g, ok := grids[o.cluster]
		if !ok {
			g = &noiseGrid{}
			grids[o.cluster] = g
		}
		c := &g[dBin(o.D)*nVBins+vBin(o.V)]
		lp, lq := capPar(o)
		rp := clamp(o.rPar, -lp, lp)
		rq := clamp(o.rPerp, -lq, lq)
		c.n++
		c.sumD += o.D
		c.sumV += o.V
		c.sPar += rp * rp
		c.sPerp += rq * rq
	}
	return grids
}

func sumGrids(grids map[int]*noiseGrid, mult map[int]int) noiseGrid {
	var tot noiseGrid
	for k, g := range grids {
		m := 1
		if mult != nil {
			m = mult[k]
			if m == 0 {
				continue
			}
		}
		fm := float64(m)
		for i := range g {
			tot[i].n += fm * g[i].n
			tot[i].sumD += fm * g[i].sumD
			tot[i].sumV += fm * g[i].sumV
			tot[i].sPar += fm * g[i].sPar
			tot[i].sPerp += fm * g[i].sPerp
		}
	}
	return tot
}

func paramsToModel(x []float64) NoiseModel {
	return NoiseModel{APar: math.Exp(x[0]), BPar: math.Exp(x[1]), CPar: math.Exp(x[2]),
		APerp: math.Exp(x[3]), BPerp: math.Exp(x[4]), CPerp: math.Exp(x[5]),
		P: 1 + 2.5/(1+math.Exp(-x[6])), ok: true}
}

func fitGrid(g *noiseGrid, x0 []float64) ([]float64, NoiseModel) {
	nll := func(x []float64) float64 {
		m := paramsToModel(x)
		tot := 0.0
		for i := range g {
			c := &g[i]
			if c.n < 1 {
				continue
			}
			sp, sq := m.Sigma(c.sumD/c.n, c.sumV/c.n)
			sp2, sq2 := sp*sp, sq*sq
			tot += c.n*math.Log(sp2) + c.sPar/sp2 + c.n*math.Log(sq2) + c.sPerp/sq2
		}
		// weak priors keep the parameters in a sensible range when data is thin
		for k := 0; k < 6; k++ {
			tot += 0.02 * (x[k] + 3) * (x[k] + 3)
		}
		tot += 0.15 * x[6] * x[6] // speed exponent: weak pull towards ~2
		return tot
	}
	if x0 == nil {
		x0 = []float64{math.Log(0.6), math.Log(0.03), math.Log(1), math.Log(0.5), math.Log(0.02), math.Log(0.6), 0}
	}
	step := []float64{0.6, 0.6, 0.8, 0.6, 0.6, 0.8, 1.0}
	best, _ := nelderMead(nll, x0, step, 900)
	best, _ = nelderMead(nll, best, []float64{0.2, 0.2, 0.3, 0.2, 0.2, 0.3, 0.4}, 700)
	return best, paramsToModel(best)
}

func fitGridQuick(g *noiseGrid, x0 []float64) ([]float64, NoiseModel) {
	nll := func(x []float64) float64 {
		m := paramsToModel(x)
		tot := 0.0
		for i := range g {
			c := &g[i]
			if c.n < 1 {
				continue
			}
			sp, sq := m.Sigma(c.sumD/c.n, c.sumV/c.n)
			sp2, sq2 := sp*sp, sq*sq
			tot += c.n*math.Log(sp2) + c.sPar/sp2 + c.n*math.Log(sq2) + c.sPerp/sq2
		}
		for k := 0; k < 6; k++ {
			tot += 0.02 * (x[k] + 3) * (x[k] + 3)
		}
		tot += 0.15 * x[6] * x[6] // speed exponent: weak pull towards ~2
		return tot
	}
	best, _ := nelderMead(nll, x0, []float64{0.15, 0.15, 0.25, 0.15, 0.15, 0.25, 0.3}, 500)
	return best, paramsToModel(best)
}

// NoiseFit holds a model and bootstrap replicates.
type NoiseFit struct {
	Model NoiseModel
	Reps  []NoiseModel
}

// FitNoise fits the scatter model with a robust two-pass procedure and
// cluster bootstrap replicates. Samples may come from different areas:
// biasFor gives the systematic model of each sample's area, so that only the
// scatter of the hand (which does not depend on the area) is pooled.
func FitNoise(samples []*Sample, biasFor func(*Sample) *BiasFit, cluster func(*Sample) int, reps int, seed int64) NoiseFit {
	var nf NoiseFit
	obs := collectNoise(samples, biasFor, cluster)
	if len(obs) < 150 {
		return nf
	}
	// pass 1: global robust caps
	ap := make([]float64, len(obs))
	aq := make([]float64, len(obs))
	vs := make([]float64, len(obs))
	ds := make([]float64, len(obs))
	for i, o := range obs {
		ap[i] = math.Abs(o.rPar)
		aq[i] = math.Abs(o.rPerp)
		vs[i] = o.V
		ds[i] = o.D
	}
	sp := median(ap) * 1.4826
	sq := median(aq) * 1.4826
	g1 := gridOf(obs, func(*noiseObs) (float64, float64) { return 5 * sp, 5 * sq })
	tot := sumGrids(g1, nil)
	x, m := fitGrid(&tot, nil)
	// pass 2: caps relative to the modelled spread
	capf := func(o *noiseObs) (float64, float64) {
		a, b := m.Sigma(o.D, o.V)
		return 3.5 * a, 3.5 * b
	}
	g2 := gridOf(obs, capf)
	tot = sumGrids(g2, nil)
	x, m = fitGrid(&tot, x)
	m.N = len(obs)
	m.VMax = quantile(vs, 0.97)
	m.DMax = quantile(ds, 0.97)
	nf.Model = m
	// bootstrap
	keys := make([]int, 0, len(g2))
	for k := range g2 {
		keys = append(keys, k)
	}
	if len(keys) >= 3 {
		rng := rand.New(rand.NewSource(seed))
		for r := 0; r < reps; r++ {
			mult := map[int]int{}
			for i := 0; i < len(keys); i++ {
				mult[keys[rng.Intn(len(keys))]]++
			}
			t := sumGrids(g2, mult)
			_, rm := fitGridQuick(&t, x)
			rm.VMax, rm.DMax, rm.N = m.VMax, m.DMax, m.N
			nf.Reps = append(nf.Reps, rm)
		}
	}
	return nf
}

// predPoint is one hit used to predict performance at other area sizes.
type predPoint struct {
	cat uint8
	csb uint8   // circle-size bucket
	D   float64 // required movement mm (at the current area)
	V   float64 // required speed m/s
	R   float64 // radius mm
}

// BestScale picks the scale with the highest hit probability inside
// [lo, hi]. Scales whose result is practically the same as the best (within
// 0.05 misses per 1000 notes or 8% of the remaining misses) count as equal:
// among them the one closest to the anchor wins (the previous advice), or,
// with a negative anchor, the middle of that plateau, the choice with the
// most margin on both sides. The advice never moves for a difference that
// would not show up in the game.
func BestScale(scales, vals []float64, lo, hi, anchor float64) float64 {
	return bestScaleTol(scales, vals, lo, hi, anchor, 0.08)
}

// bestScaleTol is BestScale with practically equal meaning within frac of
// the remaining misses.
func bestScaleTol(scales, vals []float64, lo, hi, anchor, frac float64) float64 {
	bv := -1.0
	for i, sc := range scales {
		if sc < lo-1e-9 || sc > hi+1e-9 || !finite(vals[i]) {
			continue
		}
		if vals[i] > bv {
			bv = vals[i]
		}
	}
	if bv < 0 {
		if anchor < 0 {
			return (lo + hi) / 2
		}
		return clamp(anchor, lo, hi)
	}
	tol := math.Max(0.00005, frac*(1-bv))
	if anchor < 0 {
		// the contiguous plateau around the best value
		bi := -1
		for i, sc := range scales {
			if sc >= lo-1e-9 && sc <= hi+1e-9 && finite(vals[i]) && vals[i] == bv {
				bi = i
				break
			}
		}
		a, b := bi, bi
		for a-1 >= 0 && scales[a-1] >= lo-1e-9 && finite(vals[a-1]) && vals[a-1] >= bv-tol {
			a--
		}
		for b+1 < len(scales) && scales[b+1] <= hi+1e-9 && finite(vals[b+1]) && vals[b+1] >= bv-tol {
			b++
		}
		mid := (scales[a] + scales[b]) / 2
		best, bd := scales[bi], math.Inf(1)
		for i := a; i <= b; i++ {
			if d := math.Abs(scales[i] - mid); d < bd {
				best, bd = scales[i], d
			}
		}
		return best
	}
	best, bd := anchor, math.Inf(1)
	for i, sc := range scales {
		if sc < lo-1e-9 || sc > hi+1e-9 || !finite(vals[i]) {
			continue
		}
		if vals[i] >= bv-tol && math.Abs(sc-anchor) < bd {
			best, bd = sc, math.Abs(sc-anchor)
		}
	}
	return best
}

// persistFrac is the share of the mismatch between the area and the hand's
// natural scale that is assumed to survive adaptation, until the areas the
// player has used allow measuring it.
const persistFrac = 0.3

// SweepResult is the predicted hit probability per category vs area scale.
type SweepResult struct {
	Scales   []float64
	ByCat    [NumCats][]float64 // mean hit probability
	Lo, Hi   [NumCats][]float64 // bootstrap band
	Combined []float64
	CombLo   []float64
	CombHi   []float64
	Best     float64
	BestLo   float64
	BestHi   float64
	TrustLo  float64
	TrustHi  float64
	Counts   [NumCats]int
	Anchor   float64
	// the speed limit cut the range (never moved the pen fast enough)
	SpeedCapped bool
}

func predictCat(m NoiseModel, pts []predPoint, s, lambda, persist float64, out *[NumCats]float64, cnt *[NumCats]int) {
	var sum [NumCats]float64
	var n [NumCats]int
	for _, p := range pts {
		D := s * p.D
		V := s * p.V
		sp, sq := m.Sigma(D, V)
		// part of the mismatch with the natural size survives adaptation
		mu := persist * (lambda - s) * p.D
		pr := hitProbability(s*p.R, sp, sq, mu)
		sum[p.cat] += pr
		n[p.cat]++
	}
	for c := 0; c < NumCats; c++ {
		if n[c] > 0 {
			out[c] = sum[c] / float64(n[c])
		} else {
			out[c] = math.NaN()
		}
	}
	if cnt != nil {
		*cnt = n
	}
}

// SweepOpts configures a sweep over area widths, expressed as multiples of a
// reference width (the prediction points are measured at that width).
type SweepOpts struct {
	Scales     []float64
	Lambda     float64 // natural width of the hand / reference width
	Persist    float64 // share of the mismatch that survives adaptation
	LambdaReps []float64
	Weights    [NumCats]float64
	TrustLo    float64 // allowed range, before the speed limit
	TrustHi    float64
	Anchor     float64 // preferred scale among practically equal results
}

// Sweep predicts hit probability for uniform scale factors of the area.
func Sweep(nf NoiseFit, pts []predPoint, o SweepOpts) SweepResult {
	var res SweepResult
	res.Scales = o.Scales
	ns := len(res.Scales)
	lambda, persist, weights := o.Lambda, o.Persist, o.Weights
	for c := 0; c < NumCats; c++ {
		res.ByCat[c] = make([]float64, ns)
		res.Lo[c] = make([]float64, ns)
		res.Hi[c] = make([]float64, ns)
	}
	res.Combined = make([]float64, ns)
	res.CombLo = make([]float64, ns)
	res.CombHi = make([]float64, ns)
	comb := func(v [NumCats]float64) float64 {
		t, wt := 0.0, 0.0
		for c := 0; c < NumCats; c++ {
			if weights[c] > 0 && finite(v[c]) {
				t += weights[c] * v[c]
				wt += weights[c]
			}
		}
		if wt == 0 {
			return math.NaN()
		}
		return t / wt
	}
	for i, s := range res.Scales {
		var v [NumCats]float64
		predictCat(nf.Model, pts, s, lambda, persist, &v, &res.Counts)
		for c := 0; c < NumCats; c++ {
			res.ByCat[c][i] = v[c]
		}
		res.Combined[i] = comb(v)
	}
	// bootstrap bands and optimum distribution
	var bests []float64
	repVals := make([][NumCats][]float64, len(nf.Reps))
	repComb := make([][]float64, len(nf.Reps))
	bpts := pts
	if len(bpts) > 700 {
		bpts = bpts[:700]
	}
	for r, rm := range nf.Reps {
		lam := lambda
		if len(o.LambdaReps) > 0 {
			lam = o.LambdaReps[r%len(o.LambdaReps)]
		}
		repComb[r] = make([]float64, ns)
		for c := 0; c < NumCats; c++ {
			repVals[r][c] = make([]float64, ns)
		}
		for i, s := range res.Scales {
			var v [NumCats]float64
			predictCat(rm, bpts, s, lam, persist, &v, nil)
			for c := 0; c < NumCats; c++ {
				repVals[r][c][i] = v[c]
			}
			repComb[r][i] = comb(v)
		}
	}
	for i := range res.Scales {
		for c := 0; c < NumCats; c++ {
			var xs []float64
			for r := range nf.Reps {
				if finite(repVals[r][c][i]) {
					xs = append(xs, repVals[r][c][i])
				}
			}
			if len(xs) >= 5 {
				res.Lo[c][i] = quantile(xs, 0.05)
				res.Hi[c][i] = quantile(xs, 0.95)
			} else {
				res.Lo[c][i], res.Hi[c][i] = res.ByCat[c][i], res.ByCat[c][i]
			}
		}
		var xs []float64
		for r := range nf.Reps {
			if finite(repComb[r][i]) {
				xs = append(xs, repComb[r][i])
			}
		}
		if len(xs) >= 5 {
			res.CombLo[i] = quantile(xs, 0.05)
			res.CombHi[i] = quantile(xs, 0.95)
		} else {
			res.CombLo[i], res.CombHi[i] = res.Combined[i], res.Combined[i]
		}
	}
	// trust region: the range allowed by the caller, and speeds within what
	// the player has actually produced
	res.TrustLo, res.TrustHi = o.TrustLo, o.TrustHi
	vmax := nf.Model.VMax
	if vmax > 0 && len(pts) > 0 {
		for i, s := range res.Scales {
			if s <= res.TrustLo || i == 0 {
				continue
			}
			over := 0
			for _, p := range pts {
				if p.V*s > vmax*1.05 {
					over++
				}
			}
			if float64(over)/float64(len(pts)) > 0.06 {
				// the previous point of the grid is the last one allowed
				if lim := res.Scales[i-1]; lim < res.TrustHi {
					res.TrustHi = math.Max(res.TrustLo, lim)
					res.SpeedCapped = true
				}
				break
			}
		}
	}
	anchor := o.Anchor
	if anchor >= 0 {
		anchor = clamp(anchor, res.TrustLo, res.TrustHi)
	}
	res.Best = BestScale(res.Scales, res.Combined, res.TrustLo, res.TrustHi, anchor)
	for r := range nf.Reps {
		bests = append(bests, BestScale(res.Scales, repComb[r], res.TrustLo, res.TrustHi, anchor))
	}
	if len(bests) >= 5 {
		res.BestLo = quantile(bests, 0.05)
		res.BestHi = quantile(bests, 0.95)
	} else {
		res.BestLo, res.BestHi = res.Best, res.Best
	}
	res.Anchor = anchor
	return res
}

// predPointsFrom builds prediction points from samples using the CURRENT
// area mapping (mm per osu!pixel), so that the map mix reflects what the
// player plays, whatever area those plays were made with.
func predPointsFrom(samples []*Sample, toMM float64, max int, seed int64) []predPoint {
	n := 0
	for _, s := range samples {
		if finite(s.DtReal) && s.DtReal > 0 && finite(s.Dpx) {
			n++
		}
	}
	keep := 1.0
	if max > 0 && n > max {
		keep = float64(max) / float64(n)
	}
	rng := rand.New(rand.NewSource(seed))
	pts := make([]predPoint, 0, int(float64(n)*keep)+16)
	for _, s := range samples {
		if !finite(s.DtReal) || s.DtReal <= 0 || !finite(s.Dpx) {
			continue
		}
		if keep < 1 && rng.Float64() >= keep {
			continue
		}
		D := s.Dpx * toMM
		pts = append(pts, predPoint{cat: s.Cat, csb: uint8(csBucket(s.Rpx)), D: D, V: D / s.DtReal, R: s.Rpx * toMM})
	}
	rng.Shuffle(len(pts), func(i, j int) { pts[i], pts[j] = pts[j], pts[i] })
	return pts
}

// NewNoiseModel builds a model from explicit parameters (tests, demos).
func NewNoiseModel(aPar, bPar, cPar, aPerp, bPerp, cPerp, p, vmax float64) NoiseModel {
	return NoiseModel{APar: aPar, BPar: bPar, CPar: cPar, APerp: aPerp, BPerp: bPerp, CPerp: cPerp, P: p, VMax: vmax, ok: true}
}

// SweepSamples runs Sweep on samples converted with mm-per-pixel toMM.
func SweepSamples(nf NoiseFit, samples []*Sample, toMM, lambda float64, weights [NumCats]float64) SweepResult {
	return Sweep(nf, predPointsFrom(samples, toMM, 2500, 3), SweepOpts{Scales: ScaleGrid(0.8, 1.25, 0.01), Lambda: lambda,
		Persist: persistFrac, Weights: weights, TrustLo: 0.85, TrustHi: 1.15, Anchor: 1})
}

// CSBest is the model prediction restricted to the notes of one circle-size
// bucket, so the player sees which size each kind of map would prefer.
type CSBest struct {
	Key   string    `json:"key"`
	Name  string    `json:"name"`
	Share float64   `json:"share"` // of the notes in the maps played
	Notes int       `json:"notes"`
	BestS float64   `json:"bestS"`
	BestW float64   `json:"bestW"`
	Curve []float64 `json:"curve"` // hit probability per sweep scale
}

// sweepByCS repeats the sweep prediction for each circle-size bucket.
func sweepByCS(nf NoiseFit, pts []predPoint, lambda, persist float64, weights, shares [NumCats]float64, sw SweepResult) []CSBest {
	// notes count as they appear, scaled by the priority the player chose
	var mult [NumCats]float64
	for c := 0; c < NumCats; c++ {
		if shares[c] > 0 {
			mult[c] = weights[c] / shares[c]
		}
	}
	var groups [NumCSB][]predPoint
	for _, p := range pts {
		groups[p.csb] = append(groups[p.csb], p)
	}
	var out []CSBest
	for b := 0; b < NumCSB; b++ {
		g := groups[b]
		share := float64(len(g)) / math.Max(float64(len(pts)), 1)
		if len(g) < 120 || share < 0.03 {
			continue
		}
		cb := CSBest{Key: "cs" + string(rune('0'+b)), Name: CSBNames[b], Share: round(share, 3), Notes: len(g), Curve: make([]float64, len(sw.Scales))}
		for i, s := range sw.Scales {
			var v [NumCats]float64
			var cnt [NumCats]int
			predictCat(nf.Model, g, s, lambda, persist, &v, &cnt)
			t, wt := 0.0, 0.0
			for c := 0; c < NumCats; c++ {
				if mult[c] > 0 && cnt[c] > 0 && finite(v[c]) {
					t += mult[c] * v[c] * float64(cnt[c])
					wt += mult[c] * float64(cnt[c])
				}
			}
			cb.Curve[i] = math.NaN()
			if wt > 0 {
				cb.Curve[i] = t / wt
			}
		}
		cb.BestS = BestScale(sw.Scales, cb.Curve, sw.TrustLo, sw.TrustHi, sw.Anchor)
		out = append(out, cb)
	}
	return out
}

// ScaleGrid returns scales from lo to hi (inclusive) in steps of about step.
func ScaleGrid(lo, hi, step float64) []float64 {
	n := int(math.Round((hi-lo)/step)) + 1
	if n < 2 {
		n = 2
	}
	out := make([]float64, n)
	for i := range out {
		out[i] = round(lo+(hi-lo)*float64(i)/float64(n-1), 4)
	}
	return out
}
