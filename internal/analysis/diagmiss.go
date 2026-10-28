package analysis

import (
	"math"
	"sort"
)

// The misses expected by width need a model of how the hand scatters. It
// usually comes from all the replays of the profile; with a new grip or
// tablet there are none yet, and the diagnosis builds it from its own maps:
// every stretch tells how much the clicks scattered (NoiseSum) with which
// movements and speeds.

// NoiseFromSums fits the scatter model on the sums of the stretches: for
// each component, Σr² = n·A² + B²·ΣD² + C²·ΣV^(2p), with the speed exponent
// p chosen among those kept.
func NoiseFromSums(obs []CalibPoint) (NoiseModel, bool) {
	var ns []*NoiseSum
	for _, p := range obs {
		if p.NS != nil && p.NS.N >= 15 {
			ns = append(ns, p.NS)
		}
	}
	if len(ns) < 8 {
		return NoiseModel{}, false
	}
	best := math.Inf(1)
	var out NoiseModel
	for k := 0; k < nsP; k++ {
		ap, bp, cp, ep := fitSums(ns, k, func(s *NoiseSum) float64 { return s.Par })
		aq, bq, cq, eq := fitSums(ns, k, func(s *NoiseSum) float64 { return s.Perp })
		if e := ep + eq; e < best {
			best = e
			out = NoiseModel{APar: ap, BPar: bp, CPar: cp, APerp: aq, BPerp: bq, CPerp: cq, P: nsPow(k), ok: true}
		}
	}
	return out, best < math.Inf(1)
}

// fitSums is a least-squares fit of y = n·a² + ΣD²·b² + ΣV^(2p)·c² over the
// stretches (none of the three below zero), weighted as the sums' spread
// grows with their size. It returns a, b, c and the weighted error.
func fitSums(ns []*NoiseSum, k int, y func(*NoiseSum) float64) (a, b, c, e float64) {
	cols := func(s *NoiseSum) [3]float64 { return [3]float64{float64(s.N), s.D2, s.VP[k]} }
	use := [3]bool{true, true, true}
	var x [3]float64
	for iter := 0; iter < 4; iter++ {
		// weights: 1 / (n · σ⁴), σ² from the fit so far (or the data)
		var A [3][3]float64
		var B [3]float64
		for _, s := range ns {
			v := cols(s)
			pred := x[0]*v[0] + x[1]*v[1] + x[2]*v[2]
			if iter == 0 || pred <= 0 {
				pred = y(s)
			}
			sig2 := math.Max(pred/float64(s.N), 1e-6)
			w := 1 / (float64(s.N) * sig2 * sig2)
			for i := 0; i < 3; i++ {
				if !use[i] {
					continue
				}
				for j := 0; j < 3; j++ {
					if use[j] {
						A[i][j] += w * v[i] * v[j]
					}
				}
				B[i] += w * v[i] * y(s)
			}
		}
		for i := 0; i < 3; i++ {
			if !use[i] {
				A[i][i] = 1
				B[i] = 0
			}
			A[i][i] += 1e-12 * (A[i][i] + 1)
		}
		M := [][]float64{A[0][:], A[1][:], A[2][:]}
		sol, ok := solve(M, [][]float64{{B[0]}, {B[1]}, {B[2]}})
		if !ok {
			return 0, 0, 0, math.Inf(1)
		}
		neg := false
		for i := 0; i < 3; i++ {
			x[i] = sol[i][0]
			if use[i] && x[i] < 0 {
				// a negative square: that part of the scatter is not there
				use[i], x[i], neg = false, 0, true
			}
		}
		if neg {
			iter = -1
			if !use[0] && !use[1] && !use[2] {
				return 0, 0, 0, math.Inf(1)
			}
			continue
		}
	}
	for _, s := range ns {
		v := cols(s)
		pred := x[0]*v[0] + x[1]*v[1] + x[2]*v[2]
		sig2 := math.Max(pred/float64(s.N), 1e-6)
		d := y(s) - pred
		e += d * d / (float64(s.N) * sig2 * sig2)
	}
	return math.Sqrt(x[0]), math.Sqrt(x[1]), math.Sqrt(x[2]), e
}

// DiagMissModel is the model of the aim misses by width built from the maps
// of the diagnosis alone (plays: their judged circles, each with the setup
// of its area; obs: the stretches measured on them).
func DiagMissModel(plays []*PlayData, obs []CalibPoint, weights *[NumCats]float64) *MissModel {
	if len(plays) < 2 {
		return nil
	}
	nm, ok := NoiseFromSums(obs)
	if !ok {
		return nil
	}
	_, sessOf := BuildSessions(plays)
	all := BuildSamples(plays, sessOf)
	ptr := make([]*Sample, 0, len(all))
	var vs []float64
	for i := range all {
		s := &all[i]
		ptr = append(ptr, s)
		if s.HasMM && finite(s.Dmm) && finite(s.DtReal) && s.DtReal > 0 {
			vs = append(vs, s.Dmm/s.DtReal)
		}
	}
	if len(vs) < 200 {
		return nil
	}
	sort.Float64s(vs)
	nm.VMax = vs[int(0.97*float64(len(vs)-1))]
	// the mm per osu!pixel of an area 100 mm wide: the same whatever the
	// area of each play, the screen being the same
	su := plays[len(plays)-1].Setup
	const ref = 100.0
	toMM := su.Mapper().MMPerPx() / su.Area.W * ref
	ws := make([]float64, 0, len(plays))
	for _, p := range plays {
		ws = append(ws, p.Setup.Area.W)
	}
	sort.Float64s(ws)
	w := catShares(ptr)
	if weights != nil {
		w = *weights
	}
	return newMissModel(NoiseFit{Model: nm}, predPointsFrom(ptr, toMM, 2500, 3), w, ref, ws[len(ws)/2])
}

// MapAimMisses compares the aim misses of one play with those the model
// expects for its notes with its area and no filter: the notes counted, the
// aim misses among them and the misses expected. a, b are the bias line
// (jumps too long by exp(a - b·ln w) - 1), cal the steadiness with the
// width. The play needs an epoch, for the millimetres.
func (m *MissModel) MapAimMisses(p *PlayData, a, b float64, cal PrecCal) (n, miss int, exp float64) {
	if !m.OK() || p == nil || !p.Setup.Area.Valid() {
		return
	}
	_, sessOf := BuildSessions([]*PlayData{p})
	ss := BuildSamples([]*PlayData{p}, sessOf)
	w := p.Setup.Area.W
	g := math.Exp(a-b*math.Log(w)) - 1
	if !finite(g) {
		g = 0
	}
	k := math.Sqrt(m.varAt(cal, w))
	arc := cal.Arc * arcSpread * w
	for i := range ss {
		s := &ss[i]
		if s.Kind > 1 || !s.HasClick() || !s.HasMM || !finite(s.DmapMM) || !finite(s.DtReal) || s.DtReal <= 0 || s.Rmm <= 0 {
			continue
		}
		D := s.DmapMM
		sp, sq := m.nf.Model.Sigma(D, D/s.DtReal)
		sq = math.Hypot(k*sq, arc*D)
		exp += 1 - hitProbability(s.Rmm, k*sp, sq, g*D)
		n++
		if s.Result == uint8(RAimMiss) && math.Hypot(s.ENx, s.ENy) > 1 {
			miss++
		}
	}
	return
}

// ScatterRatio is how much the clicks of the stretches scattered against
// what the model expects for their movements (1: as expected; below: more
// gathered), and the clicks it is measured on.
func (m *MissModel) ScatterRatio(ws []CalibPoint) (float64, int) {
	if !m.OK() {
		return 0, 0
	}
	var got, want float64
	n := 0
	for _, w := range ws {
		if w.NS == nil || w.NS.N < 10 {
			continue
		}
		pr := w.NS.predicted(m.nf.Model)
		if !(pr > 0) {
			continue
		}
		got += w.NS.Par + w.NS.Perp
		want += pr
		n += w.NS.N
	}
	if want <= 0 {
		return 0, 0
	}
	return got / want, n
}
