package analysis

import (
	"math"
	"math/rand"
	"sort"
)

// CatMetrics are observed aim statistics for one category.
type CatMetrics struct {
	Objects   int     `json:"objects"`
	Aimed     int     `json:"aimed"`
	AimMiss   int     `json:"aimMiss"`
	MissPer1k float64 `json:"missPer1k"`
	NormErr   float64 `json:"normErr"` // robust RMS of |error|/radius
	NormErrLo float64 `json:"normErrLo"`
	NormErrHi float64 `json:"normErrHi"`
	Inside    float64 `json:"inside"` // share of presses inside the circle
	Overshoot float64 `json:"overshoot"`
	Settle    float64 `json:"settle"`
}

func robustRMS(xs []float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	s := 0.0
	n := 0
	for _, x := range xs {
		x = math.Min(x, 3)
		s += x * x
		n++
	}
	return math.Sqrt(s / float64(n) / 2)
}

// catMetrics computes observed metrics for samples (any epoch).
func catMetrics(samples []*Sample, rng *rand.Rand) [NumCats + 1]CatMetrics {
	var out [NumCats + 1]CatMetrics
	var errs [NumCats + 1][]float64
	var inside [NumCats + 1]int
	var over [NumCats + 1][2]int
	var settle [NumCats + 1][]float64
	for _, s := range samples {
		for _, c := range []int{int(s.Cat), NumCats} {
			m := &out[c]
			m.Objects++
			if s.Result == RAimMiss {
				m.AimMiss++
			}
			if isAimHit(s) {
				m.Aimed++
				e := math.Hypot(s.ENx, s.ENy)
				errs[c] = append(errs[c], e)
				if e <= 1 {
					inside[c]++
				}
				if finite(s.OverN) {
					over[c][1]++
					if s.OverN > 0.6 {
						over[c][0]++
					}
				}
				if finite(s.SettleN) {
					settle[c] = append(settle[c], s.SettleN)
				}
			}
		}
	}
	for c := range out {
		m := &out[c]
		if m.Objects > 0 {
			m.MissPer1k = 1000 * float64(m.AimMiss) / float64(m.Objects)
		}
		m.NormErr = robustRMS(errs[c])
		m.NormErrLo, m.NormErrHi = m.NormErr, m.NormErr
		if n := len(errs[c]); n >= 30 && rng != nil {
			// analytic interval on the mean square, with a design effect of 3
			// because notes of the same play are not independent
			s1, s2 := 0.0, 0.0
			for _, x := range errs[c] {
				q := math.Min(x, 3)
				q *= q
				s1 += q
				s2 += q * q
			}
			mean2 := s1 / float64(n)
			v := s2/float64(n) - mean2*mean2
			se := math.Sqrt(math.Max(v, 0) / (float64(n) / 3))
			m.NormErrLo = math.Sqrt(math.Max(mean2-1.96*se, 0) / 2)
			m.NormErrHi = math.Sqrt((mean2 + 1.96*se) / 2)
		}
		if m.Aimed > 0 {
			m.Inside = float64(inside[c]) / float64(m.Aimed)
		}
		if over[c][1] > 0 {
			m.Overshoot = float64(over[c][0]) / float64(over[c][1])
		} else {
			m.Overshoot = math.NaN()
		}
		if len(settle[c]) > 4000 {
			step := len(settle[c]) / 4000
			sub := make([]float64, 0, 4001)
			for i := 0; i < len(settle[c]); i += step {
				sub = append(sub, settle[c][i])
			}
			m.Settle = median(sub)
		} else {
			m.Settle = median(settle[c])
		}
	}
	return out
}

// FieldCell is the mean landing error in one region of the tablet.
type FieldCell struct {
	X  float64 `json:"x"`
	Y  float64 `json:"y"`
	EX float64 `json:"ex"`
	EY float64 `json:"ey"`
	N  int     `json:"n"`
}

func errorField(samples []*Sample, cell float64) []FieldCell {
	type acc struct {
		sx, sy []float64
	}
	grid := map[[2]int]*acc{}
	for _, s := range samples {
		if !s.HasMM || !isAimHit(s) {
			continue
		}
		k := [2]int{int(math.Floor(s.Tx / cell)), int(math.Floor(s.Ty / cell))}
		a, ok := grid[k]
		if !ok {
			a = &acc{}
			grid[k] = a
		}
		a.sx = append(a.sx, s.Cx-s.Tx)
		a.sy = append(a.sy, s.Cy-s.Ty)
	}
	var out []FieldCell
	for k, a := range grid {
		if len(a.sx) < 25 {
			continue
		}
		out = append(out, FieldCell{X: (float64(k[0]) + 0.5) * cell, Y: (float64(k[1]) + 0.5) * cell,
			EX: trimmedMean(a.sx), EY: trimmedMean(a.sy), N: len(a.sx)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Y != out[j].Y {
			return out[i].Y < out[j].Y
		}
		return out[i].X < out[j].X
	})
	return out
}

func trimmedMean(xs []float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	c := append([]float64(nil), xs...)
	sort.Float64s(c)
	k := len(c) / 10
	c = c[k : len(c)-k]
	return mean(c)
}

// DistPoint is the mean error along the movement for a distance bin.
type DistPoint struct {
	D    float64 `json:"d"`
	Mean float64 `json:"mean"`
	Lo   float64 `json:"lo"`
	Hi   float64 `json:"hi"`
	Pct  float64 `json:"pct"` // mean / D in %
	N    int     `json:"n"`
}

func alongMovement(s *Sample) (float64, float64, float64, bool) {
	if !s.HasMM || !isAimHit(s) || !s.HasOrigin() {
		return 0, 0, 0, false
	}
	mx, my := s.Tx-s.Ox, s.Ty-s.Oy
	D := math.Hypot(mx, my)
	if D < 3 {
		return 0, 0, 0, false
	}
	ux, uy := mx/D, my/D
	ex, ey := s.Cx-s.Tx, s.Cy-s.Ty
	return D, ex*ux + ey*uy, -ex*uy + ey*ux, true
}

func distanceCurve(samples []*Sample, offset [2]float64) []DistPoint {
	edges := []float64{3, 8, 13, 18, 24, 30, 37, 45, 55, 70, 100}
	bins := make([][]float64, len(edges)-1)
	ds := make([][]float64, len(edges)-1)
	for _, s := range samples {
		D, par, _, ok := alongMovement(s)
		if !ok {
			continue
		}
		// remove the constant offset so the curve shows the gain only
		mx, my := (s.Tx-s.Ox)/D, (s.Ty-s.Oy)/D
		par -= offset[0]*mx + offset[1]*my
		for b := 0; b < len(edges)-1; b++ {
			if D >= edges[b] && D < edges[b+1] {
				bins[b] = append(bins[b], par)
				ds[b] = append(ds[b], D)
				break
			}
		}
	}
	var out []DistPoint
	for b := range bins {
		if len(bins[b]) < 30 {
			continue
		}
		m := trimmedMean(bins[b])
		sd := 1.4826 * median(absDev(bins[b], median(bins[b])))
		se := sd / math.Sqrt(float64(len(bins[b])))
		d := mean(ds[b])
		out = append(out, DistPoint{D: d, Mean: m, Lo: m - 1.96*se, Hi: m + 1.96*se, Pct: 100 * m / d, N: len(bins[b])})
	}
	return out
}

func absDev(xs []float64, m float64) []float64 {
	out := make([]float64, len(xs))
	for i, x := range xs {
		out[i] = math.Abs(x - m)
	}
	return out
}

// DirPoint is the mean gain for a movement direction sector.
type DirPoint struct {
	Angle float64 `json:"angle"` // degrees, 0 = right, 90 = down (tablet)
	Gain  float64 `json:"gain"`  // % overshoot
	Side  float64 `json:"side"`  // mean sideways error mm
	N     int     `json:"n"`
}

func directionGains(samples []*Sample, offset [2]float64) []DirPoint {
	const sectors = 12
	var par, side [sectors][]float64
	for _, s := range samples {
		D, p, q, ok := alongMovement(s)
		if !ok || D < 12 {
			continue
		}
		mx, my := (s.Tx-s.Ox)/D, (s.Ty-s.Oy)/D
		p -= offset[0]*mx + offset[1]*my
		q -= -offset[0]*my + offset[1]*mx
		a := math.Atan2(my, mx)
		if a < 0 {
			a += 2 * math.Pi
		}
		k := int(math.Floor(a/(2*math.Pi)*sectors+0.5)) % sectors
		par[k] = append(par[k], 100*p/D)
		side[k] = append(side[k], q)
	}
	var out []DirPoint
	for k := 0; k < sectors; k++ {
		if len(par[k]) < 20 {
			continue
		}
		out = append(out, DirPoint{Angle: float64(k) * 360 / sectors, Gain: trimmedMean(par[k]), Side: trimmedMean(side[k]), N: len(par[k])})
	}
	return out
}

// DriftPoint is the mean offset at a stage of the play.
type DriftPoint struct {
	Stage string  `json:"stage"`
	EX    float64 `json:"ex"`
	EY    float64 `json:"ey"`
	N     int     `json:"n"`
}

func driftWithinPlay(samples []*Sample) []DriftPoint {
	names := []string{"Start", "Middle", "End"}
	var xs, ys [3][]float64
	for _, s := range samples {
		if !s.HasMM || !isAimHit(s) {
			continue
		}
		k := int(s.Frac * 3)
		if k > 2 {
			k = 2
		}
		xs[k] = append(xs[k], s.Cx-s.Tx)
		ys[k] = append(ys[k], s.Cy-s.Ty)
	}
	var out []DriftPoint
	for k := 0; k < 3; k++ {
		if len(xs[k]) < 30 {
			continue
		}
		out = append(out, DriftPoint{Stage: names[k], EX: trimmedMean(xs[k]), EY: trimmedMean(ys[k]), N: len(xs[k])})
	}
	return out
}

// ScatterPoint is a press relative to its circle, in radii, with the
// movement direction rotated to point right.
type ScatterPoint struct {
	X   float64 `json:"x"`
	Y   float64 `json:"y"`
	Hit bool    `json:"hit"`
}

func scatterPoints(samples []*Sample, max int, rng *rand.Rand) []ScatterPoint {
	var out []ScatterPoint
	for _, s := range samples {
		if !s.HasClick() {
			continue
		}
		e := math.Hypot(s.ENx, s.ENy)
		if e > 2.5 {
			continue
		}
		x, y := s.ENx, s.ENy
		if s.HasOrigin() && s.HasMM {
			mx, my := s.Tx-s.Ox, s.Ty-s.Oy
			D := math.Hypot(mx, my)
			if D >= 3 {
				// rotate the error (in screen orientation) so the movement points right
				ex, ey := (s.Cx-s.Tx)/s.Rmm, (s.Cy-s.Ty)/s.Rmm
				ux, uy := mx/D, my/D
				x, y = ex*ux+ey*uy, -ex*uy+ey*ux
			} else {
				continue
			}
		} else {
			continue
		}
		out = append(out, ScatterPoint{X: round(x, 3), Y: round(y, 3), Hit: e <= 1})
	}
	if len(out) > max {
		rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
		out = out[:max]
	}
	return out
}
