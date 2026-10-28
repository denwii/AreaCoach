package analysis

import (
	"math"
	"math/rand"

	"areacoach/internal/otd"
)

// BiasFit is the systematic mismatch between where the pen should land and
// where it lands, in tablet mm:  e = Av·(t-o) + Ap·(t-a) + d.
type BiasFit struct {
	N     int
	Av    [2][2]float64 // gain on the movement vector (jumps)
	Ap    [2][2]float64 // gain on the absolute position (relative to area centre)
	D     [2]float64    // constant offset at the area centre
	Scale float64       // robust residual scale (mm)
	coef  [5][2]float64
	ok    bool

	clusters []clusterStats
}

type clusterStats struct {
	xtx [5][5]float64
	xty [5][2]float64
	n   int
}

// L returns the linear part of the "natural" mapping: I + Av + Ap.
func (b *BiasFit) L() [2][2]float64 {
	return [2][2]float64{
		{1 + b.Av[0][0] + b.Ap[0][0], b.Av[0][1] + b.Ap[0][1]},
		{b.Av[1][0] + b.Ap[1][0], 1 + b.Av[1][1] + b.Ap[1][1]},
	}
}

// OK reports whether the fit succeeded.
func (b *BiasFit) OK() bool { return b.ok }

func biasRow(s *Sample) (x [5]float64, y [2]float64) {
	dx, dy := 0.0, 0.0
	if s.HasOrigin() {
		dx, dy = s.Tx-s.Ox, s.Ty-s.Oy
	}
	x = [5]float64{dx, dy, s.Tx - s.Ax, s.Ty - s.Ay, 1}
	y = [2]float64{s.Cx - s.Tx, s.Cy - s.Ty}
	return
}

// FitBias runs a robust (Huber IRLS) regression on mm samples. weight gives
// an optional per-sample weight (recency); cluster gives the bootstrap
// cluster of each sample (session or play).
func FitBias(samples []*Sample, weight func(*Sample) float64, cluster func(*Sample) int) BiasFit {
	var fit BiasFit
	type row struct {
		x [5]float64
		y [2]float64
		w float64
		c int
	}
	rows := make([]row, 0, len(samples))
	for _, s := range samples {
		if !s.HasMM || !isAimHit(s) || math.IsNaN(s.Cx) {
			continue
		}
		x, y := biasRow(s)
		w := 1.0
		if weight != nil {
			w = weight(s)
		}
		rows = append(rows, row{x, y, w, cluster(s)})
	}
	fit.N = len(rows)
	if len(rows) < 60 {
		return fit
	}
	rw := make([]float64, len(rows))
	for i := range rw {
		rw[i] = 1
	}
	var coef [5][2]float64
	scale := 1.0
	stride := 1
	if len(rows) > 20000 {
		stride = len(rows) / 20000
	}
	for iter := 0; iter < 8; iter++ {
		var xtx [5][5]float64
		var xty [5][2]float64
		for i, r := range rows {
			w := r.w * rw[i]
			for a := 0; a < 5; a++ {
				for b := 0; b < 5; b++ {
					xtx[a][b] += w * r.x[a] * r.x[b]
				}
				xty[a][0] += w * r.x[a] * r.y[0]
				xty[a][1] += w * r.x[a] * r.y[1]
			}
		}
		c, ok := solveRidge(xtx, xty)
		if !ok {
			return fit
		}
		coef = c
		// residual scale (MAD of residual norms)
		res := make([]float64, len(rows))
		sub := make([]float64, 0, len(rows)/stride+1)
		for i, r := range rows {
			ex, ey := residual(coef, r.x, r.y)
			res[i] = math.Hypot(ex, ey)
			if i%stride == 0 {
				sub = append(sub, res[i])
			}
		}
		scale = median(sub) * 1.4826 / 1.1774 // norm of 2D gaussian: median = 1.1774σ
		if scale < 1e-6 {
			scale = 1e-6
		}
		k := 1.5 * scale * 1.1774
		for i := range rows {
			if res[i] <= k {
				rw[i] = 1
			} else {
				rw[i] = k / res[i]
			}
		}
	}
	fit.coef = coef
	fit.Scale = scale
	fit.Av = [2][2]float64{{coef[0][0], coef[1][0]}, {coef[0][1], coef[1][1]}}
	fit.Ap = [2][2]float64{{coef[2][0], coef[3][0]}, {coef[2][1], coef[3][1]}}
	fit.D = [2]float64{coef[4][0], coef[4][1]}
	fit.ok = true
	// cluster statistics with the final robust weights (one-step bootstrap)
	idx := map[int]int{}
	for i, r := range rows {
		ci, ok := idx[r.c]
		if !ok {
			ci = len(fit.clusters)
			idx[r.c] = ci
			fit.clusters = append(fit.clusters, clusterStats{})
		}
		cs := &fit.clusters[ci]
		w := r.w * rw[i]
		for a := 0; a < 5; a++ {
			for b := 0; b < 5; b++ {
				cs.xtx[a][b] += w * r.x[a] * r.x[b]
			}
			cs.xty[a][0] += w * r.x[a] * r.y[0]
			cs.xty[a][1] += w * r.x[a] * r.y[1]
		}
		cs.n++
	}
	return fit
}

func residual(coef [5][2]float64, x [5]float64, y [2]float64) (float64, float64) {
	px, py := 0.0, 0.0
	for a := 0; a < 5; a++ {
		px += coef[a][0] * x[a]
		py += coef[a][1] * x[a]
	}
	return y[0] - px, y[1] - py
}

func solveRidge(xtx [5][5]float64, xty [5][2]float64) ([5][2]float64, bool) {
	var out [5][2]float64
	tr := 0.0
	for a := 0; a < 4; a++ {
		tr += xtx[a][a]
	}
	A := make([][]float64, 5)
	B := make([][]float64, 5)
	for a := 0; a < 5; a++ {
		A[a] = append([]float64(nil), xtx[a][:]...)
		if a < 4 {
			A[a][a] += 1e-6*tr + 1e-9
		} else {
			A[a][a] += 1e-9
		}
		B[a] = []float64{xty[a][0], xty[a][1]}
	}
	x, ok := solve(A, B)
	if !ok {
		return out, false
	}
	for a := 0; a < 5; a++ {
		out[a][0], out[a][1] = x[a][0], x[a][1]
	}
	return out, true
}

// Predict returns the modelled systematic error for a sample.
func (b *BiasFit) Predict(s *Sample) (float64, float64) {
	x, _ := biasRow(s)
	px, py := 0.0, 0.0
	for a := 0; a < 5; a++ {
		px += b.coef[a][0] * x[a]
		py += b.coef[a][1] * x[a]
	}
	return px, py
}

// AreaSet holds the three bias-based areas.
type AreaSet struct {
	Ideal     otd.Rect
	SameRatio otd.Rect
	Shifted   otd.Rect
	Lambda    float64 // isotropic natural scale (same ratio)
	Rotation  float64 // degrees change in the full fit
}

// Derive computes the ideal / same-ratio / shifted areas from a fit.
func (b *BiasFit) Derive(cur otd.Rect) AreaSet {
	return deriveAreas(b.L(), b.D, cur)
}

func deriveAreas(L [2][2]float64, d [2]float64, cur otd.Rect) AreaSet {
	th := cur.Rot * math.Pi / 180
	R := rot(th)
	S := [2][2]float64{{cur.W / 2, 0}, {0, cur.H / 2}}
	M := mul(mul(L, R), S)
	RS := mul(R, S)
	// same ratio: lambda = <RS, M>/<RS, RS>
	num := RS[0][0]*M[0][0] + RS[0][1]*M[0][1] + RS[1][0]*M[1][0] + RS[1][1]*M[1][1]
	den := RS[0][0]*RS[0][0] + RS[0][1]*RS[0][1] + RS[1][0]*RS[1][0] + RS[1][1]*RS[1][1]
	lambda := num / den
	centre := [2]float64{cur.X + d[0], cur.Y + d[1]}
	set := AreaSet{Lambda: lambda}
	set.Shifted = otd.Rect{W: cur.W, H: cur.H, X: centre[0], Y: centre[1], Rot: cur.Rot}
	set.SameRatio = otd.Rect{W: cur.W * lambda, H: cur.H * lambda, X: centre[0], Y: centre[1], Rot: cur.Rot}
	// full: best rotated rectangle
	obj := func(t float64) (float64, float64, float64) {
		Rt := rot(t)
		Q := mul(transpose(Rt), M)
		m1, m2 := Q[0][0], Q[1][1]
		F := mul(Rt, [2][2]float64{{m1, 0}, {0, m2}})
		e := 0.0
		for i := 0; i < 2; i++ {
			for j := 0; j < 2; j++ {
				e += (M[i][j] - F[i][j]) * (M[i][j] - F[i][j])
			}
		}
		return e, m1, m2
	}
	lo, hi := th-0.35, th+0.35
	gr := (math.Sqrt(5) - 1) / 2
	a, bb := hi-gr*(hi-lo), lo+gr*(hi-lo)
	for i := 0; i < 60; i++ {
		fa, _, _ := obj(a)
		fb, _, _ := obj(bb)
		if fa < fb {
			hi = bb
		} else {
			lo = a
		}
		a, bb = hi-gr*(hi-lo), lo+gr*(hi-lo)
	}
	tbest := (lo + hi) / 2
	_, m1, m2 := obj(tbest)
	set.Ideal = otd.Rect{W: 2 * math.Abs(m1), H: 2 * math.Abs(m2), X: centre[0], Y: centre[1], Rot: tbest * 180 / math.Pi}
	set.Rotation = set.Ideal.Rot - cur.Rot
	return set
}

func rot(t float64) [2][2]float64 {
	c, s := math.Cos(t), math.Sin(t)
	return [2][2]float64{{c, -s}, {s, c}}
}

func mul(a, b [2][2]float64) [2][2]float64 {
	return [2][2]float64{
		{a[0][0]*b[0][0] + a[0][1]*b[1][0], a[0][0]*b[0][1] + a[0][1]*b[1][1]},
		{a[1][0]*b[0][0] + a[1][1]*b[1][0], a[1][0]*b[0][1] + a[1][1]*b[1][1]},
	}
}

func transpose(a [2][2]float64) [2][2]float64 {
	return [2][2]float64{{a[0][0], a[1][0]}, {a[0][1], a[1][1]}}
}

// BiasBoot holds bootstrap spreads (95% half-widths) of derived values.
type BiasBoot struct {
	Reps                     int
	DX, DY                   float64 // offset
	Lambda                   float64
	IdealW, IdealH, Rotation float64
	JumpGain                 float64
	Samples                  []AreaSet
}

// Bootstrap resamples clusters (one-step, fixed robust weights).
func (b *BiasFit) Bootstrap(cur otd.Rect, reps int, seed int64) BiasBoot {
	out := BiasBoot{Reps: reps}
	if !b.ok || len(b.clusters) < 3 {
		return out
	}
	rng := rand.New(rand.NewSource(seed))
	var dxs, dys, lams, iws, ihs, rots, jg []float64
	for r := 0; r < reps; r++ {
		mult := resampleClusters(rng, len(b.clusters))
		var xtx [5][5]float64
		var xty [5][2]float64
		for ci, m := range mult {
			if m == 0 {
				continue
			}
			cs := &b.clusters[ci]
			fm := float64(m)
			for a := 0; a < 5; a++ {
				for c := 0; c < 5; c++ {
					xtx[a][c] += fm * cs.xtx[a][c]
				}
				xty[a][0] += fm * cs.xty[a][0]
				xty[a][1] += fm * cs.xty[a][1]
			}
		}
		coef, ok := solveRidge(xtx, xty)
		if !ok {
			continue
		}
		bf := BiasFit{coef: coef, ok: true}
		bf.Av = [2][2]float64{{coef[0][0], coef[1][0]}, {coef[0][1], coef[1][1]}}
		bf.Ap = [2][2]float64{{coef[2][0], coef[3][0]}, {coef[2][1], coef[3][1]}}
		bf.D = [2]float64{coef[4][0], coef[4][1]}
		set := bf.Derive(cur)
		out.Samples = append(out.Samples, set)
		dxs = append(dxs, bf.D[0])
		dys = append(dys, bf.D[1])
		lams = append(lams, set.Lambda)
		iws = append(iws, set.Ideal.W)
		ihs = append(ihs, set.Ideal.H)
		rots = append(rots, set.Rotation)
		jg = append(jg, (bf.Av[0][0]+bf.Av[1][1])/2)
	}
	hw := func(xs []float64) float64 {
		if len(xs) < 5 {
			return math.NaN()
		}
		return (quantile(xs, 0.975) - quantile(xs, 0.025)) / 2
	}
	out.DX, out.DY = hw(dxs), hw(dys)
	out.Lambda = hw(lams)
	out.IdealW, out.IdealH = hw(iws), hw(ihs)
	out.Rotation = hw(rots)
	out.JumpGain = hw(jg)
	return out
}

// JumpGain is the average gain on movement vectors (overshoot > 0).
func (b *BiasFit) JumpGain() float64 { return (b.Av[0][0] + b.Av[1][1]) / 2 }

// PositionGain is the average gain on absolute positions.
func (b *BiasFit) PositionGain() float64 { return (b.Ap[0][0] + b.Ap[1][1]) / 2 }
