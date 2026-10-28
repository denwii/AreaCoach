package analysis

import "math"

// NoiseSum sums up how the clicks of a stretch scatter around where the
// player aimed (mm of tablet, along and across the movement), with what the
// noise model needs to predict that scatter: the movements (mm) and their
// speeds (m/s). Comparing the two, stretch by stretch, tells whether the
// player is steadier or shakier than the model says with each width.
type NoiseSum struct {
	N    int          `json:"n"`
	Par  float64      `json:"par"`  // Σ residual² along the movement (mm²)
	Perp float64      `json:"perp"` // across it
	D2   float64      `json:"d2"`   // Σ D²
	VP   [nsP]float64 `json:"vp"`   // Σ V^(2p), p = 1, 1.25, … 3.5
}

const nsP = 11

func nsPow(k int) float64 { return 1 + 0.25*float64(k) }

func (s *NoiseSum) add(par, perp, D, V float64) {
	s.N++
	s.Par += par * par
	s.Perp += perp * perp
	s.D2 += D * D
	for k := range s.VP {
		s.VP[k] += math.Pow(V, 2*nsPow(k))
	}
}

// vp is Σ V^(2p) for any p in [1, 3.5], between the sums kept.
func (s *NoiseSum) vp(p float64) float64 {
	x := clamp((p-1)/0.25, 0, nsP-1)
	k := int(math.Floor(x))
	if k >= nsP-1 {
		return s.VP[nsP-1]
	}
	f := x - float64(k)
	a, b := s.VP[k], s.VP[k+1]
	if a > 0 && b > 0 {
		return math.Exp((1-f)*math.Log(a) + f*math.Log(b))
	}
	return (1-f)*a + f*b
}

// predicted is the scatter (Σ of both components, mm²) the noise model
// expects for the clicks of the stretch.
func (s *NoiseSum) predicted(m NoiseModel) float64 {
	return float64(s.N)*(m.APar*m.APar+m.APerp*m.APerp) + (m.BPar*m.BPar+m.BPerp*m.BPerp)*s.D2 + (m.CPar*m.CPar+m.CPerp*m.CPerp)*s.vp(m.P)
}

// PrecCal is how the scatter measured during the diagnosis compares with the
// one the model of the replays predicts, as the width changes: the variance
// is the model's times (w / the width of the replays)^Gamma. A negative Gamma
// means the player gets shakier than expected on small areas (steadier on
// large ones); a positive one the opposite.
type PrecCal struct {
	Gamma float64 `json:"gamma"`
	SE    float64 `json:"se"`
	N     int     `json:"n"`  // stretches used
	Lo    float64 `json:"lo"` // widths they span
	Hi    float64 `json:"hi"`
	Ratio float64 `json:"ratio"` // measured scatter over the model's, on average
	// how fast the turning of the movements changes across the tablet
	// (radians per mm): across a wider area the arc turns them more
	Arc float64 `json:"arc"`
	// how the scatter changes with where the area is: log of the variance
	// per mm to the right (GX) and down (GY), with standard errors, around
	// X0, Y0, over the places measured (XLo-XHi, YLo-YHi)
	GX   float64 `json:"gx,omitempty"`
	GXSE float64 `json:"gxse,omitempty"`
	GY   float64 `json:"gy,omitempty"`
	GYSE float64 `json:"gyse,omitempty"`
	X0   float64 `json:"x0,omitempty"`
	Y0   float64 `json:"y0,omitempty"`
	XLo  float64 `json:"xlo,omitempty"`
	XHi  float64 `json:"xhi,omitempty"`
	YLo  float64 `json:"ylo,omitempty"`
	YHi  float64 `json:"yhi,omitempty"`
	// the clicks' variance per axis around where they were aimed (mm²)
	Sig2 float64 `json:"sig2,omitempty"`
}

// how much the trend is trusted before the stretches say otherwise (SD of
// Gamma), and how much stretches differ beyond chance (log variance)
const (
	precPriorSD = 0.6
	precTau2    = 0.06
	// the same for the place (log variance per mm) and the time (per hour)
	precPlaceSD = 0.03
	precTimeSD  = 0.2
)

// Calibrate compares the scatter of the stretches with the model's. Only
// stretches where the hand had got used to the area count: right after a
// big change, or with jumps still clearly off, the scatter says how new the
// area is, not how steady the hand can be with it.
//
// Besides the width, the scatter is followed by where the area is on the
// tablet (the hand may be steadier with it more to one side, where the
// wrist rests better) and by the time played (warming up, getting tired),
// so that neither is taken for the other.
func (m *MissModel) Calibrate(obs []CalibPoint) PrecCal {
	return m.calibrate(obs, false, 0, 0)
}

// CalibrateNear is Calibrate around the place x, y: the stretches played
// with the area there count most (within about steadyNear mm), so that how
// the steadiness changes with the place is the one around it, not a line
// through places far apart.
func (m *MissModel) CalibrateNear(obs []CalibPoint, x, y float64) PrecCal {
	return m.calibrate(obs, true, x, y)
}

// steadyNear is how far around a place the stretches count for how the
// steadiness changes there (mm, SD of the weight).
const steadyNear = 4.0

func (m *MissModel) calibrate(obs []CalibPoint, near bool, nx, ny float64) PrecCal {
	var out PrecCal
	if !m.OK() {
		return out
	}
	nm := m.nf.Model
	type row struct{ lw, x, y, t, v, w float64 }
	var rows []row
	lo, hi := math.Inf(1), math.Inf(-1)
	xlo, xhi, ylo, yhi := math.Inf(1), math.Inf(-1), math.Inf(1), math.Inf(-1)
	var s2, s2n float64
	for _, p := range obs {
		ns := p.NS
		if ns == nil || ns.N < 15 || !p.Area.Valid() || p.SG <= 0 || math.Abs(p.G) > 0.08 {
			continue
		}
		pr := ns.predicted(nm)
		if !(pr > 0) {
			continue
		}
		r := (ns.Par + ns.Perp) / pr
		if !(r > 0) {
			continue
		}
		w := 1 / (2/float64(ns.N) + precTau2)
		if near {
			dx, dy := p.Area.X-nx, p.Area.Y-ny
			w *= math.Exp(-(dx*dx + dy*dy) / (2 * steadyNear * steadyNear))
			if w < 1e-3 {
				continue
			}
		}
		rows = append(rows, row{lw: math.Log(p.Area.W), x: p.Area.X, y: p.Area.Y, v: clamp(math.Log(r), -2, 2), w: w})
		rows[len(rows)-1].t = float64(p.At.UnixNano()) / 3.6e12
		lo, hi = math.Min(lo, p.Area.W), math.Max(hi, p.Area.W)
		xlo, xhi = math.Min(xlo, p.Area.X), math.Max(xhi, p.Area.X)
		ylo, yhi = math.Min(ylo, p.Area.Y), math.Max(yhi, p.Area.Y)
		s2 += (ns.Par + ns.Perp) / 2
		s2n += float64(ns.N)
	}
	out.N = len(rows)
	if out.N < 4 {
		return PrecCal{N: out.N}
	}
	var sw float64
	var mean [5]float64 // log width, x, y, hours, value
	for _, r := range rows {
		sw += r.w
		for k, v := range [5]float64{r.lw, r.x, r.y, r.t, r.v} {
			mean[k] += r.w * v
		}
	}
	for k := range mean {
		mean[k] /= sw
	}
	// the scatter (log, against the model's) as a line in the width, the
	// place and the time, each held toward no effect until the stretches
	// say otherwise
	prior := [4]float64{1 / (precPriorSD * precPriorSD), 1 / (precPlaceSD * precPlaceSD), 1 / (precPlaceSD * precPlaceSD), 1 / (precTimeSD * precTimeSD)}
	A := make([][]float64, 4)
	B := make([][]float64, 4)
	for i := range A {
		A[i] = make([]float64, 4)
		B[i] = make([]float64, 5)
		A[i][i] = prior[i]
		B[i][1+i] = 1
	}
	for _, r := range rows {
		f := [4]float64{r.lw - mean[0], r.x - mean[1], r.y - mean[2], r.t - mean[3]}
		for i := 0; i < 4; i++ {
			B[i][0] += r.w * f[i] * (r.v - mean[4])
			for j := 0; j < 4; j++ {
				A[i][j] += r.w * f[i] * f[j]
			}
		}
	}
	sol, ok := solve(A, B)
	if !ok {
		return PrecCal{N: out.N}
	}
	out.Gamma = round(sol[0][0], 4)
	out.SE = round(math.Sqrt(sol[0][1]), 4)
	out.GX, out.GXSE = round(sol[1][0], 5), round(math.Sqrt(sol[1][2]), 5)
	out.GY, out.GYSE = round(sol[2][0], 5), round(math.Sqrt(sol[2][3]), 5)
	out.X0, out.Y0 = round(mean[1], 2), round(mean[2], 2)
	out.XLo, out.XHi, out.YLo, out.YHi = round(xlo, 2), round(xhi, 2), round(ylo, 2), round(yhi, 2)
	out.Lo, out.Hi = round(lo, 2), round(hi, 2)
	out.Ratio = round(math.Exp(mean[4]), 3)
	if s2n > 0 {
		out.Sig2 = round(s2/s2n, 4)
	}
	return out
}

// Home is the width of the replays the model comes from.
func (m *MissModel) Home() float64 {
	if m == nil {
		return 0
	}
	return m.home
}

// varAt is the multiplier of the model's variance with width w.
func (m *MissModel) varAt(cal PrecCal, w float64) float64 {
	if cal.Gamma == 0 || !(m.home > 0) {
		return 1
	}
	// beyond the widths measured the trend is not carried further
	lw := math.Log(w)
	if cal.Lo > 0 && cal.Hi > 0 {
		lw = clamp(lw, math.Log(cal.Lo), math.Log(cal.Hi))
	}
	return math.Exp(cal.Gamma * (lw - math.Log(m.home)))
}

// arcSpread is how far from the middle of the area, as a share of its
// width, the circles of a map typically are sideways (the playfield covers
// about 0.6 of a 16:9 screen; spread evenly, its SD is 0.6/√12).
const arcSpread = 0.17

// MissPt is the predicted aim misses (per 1000 notes) with one width.
type MissPt struct {
	W    float64 `json:"w"`
	Miss float64 `json:"miss"`
}

// MissPlan is the width with the fewest predicted aim misses.
type MissPlan struct {
	W          float64  // the best width
	Miss       float64  // aim misses per 1000 notes with it
	MissAnchor float64  // with the width in use
	Curve      []MissPt // by width, for showing
}

type planKey [7]int32

// BestFor returns the width between lo and hi with the fewest predicted aim
// misses over all the kinds of maps the player plays, for a player whose
// jumps with width w are too long by exp(a - b·ln w) - 1 (what the
// diagnosis measured: b says how much that follows the width; with b near
// zero it is a habit no width fixes) and whose scatter compares with the
// model's as cal says. A small area needs a steadier hand where precision
// counts (small circles), a large one faster movements on big jumps (and
// beyond the speeds the player ever reached, it is not used at all). Among
// widths practically as good, the one in use (anchor) stays.
func (m *MissModel) BestFor(a, b float64, cal PrecCal, anchor, lo, hi float64) (MissPlan, bool) {
	if !m.OK() || !(hi > 0) {
		return MissPlan{}, false
	}
	lo = math.Max(lo, 20)
	// beyond the speeds the replays ever reached the maps could not be
	// played, unless the hand was measured there during the diagnosis
	if lim := math.Max(m.maxW, cal.Hi); lim < hi {
		hi = math.Max(lim, lo)
	}
	key := planKey{int32(math.Round(a * 400)), int32(math.Round(b * 200)), int32(math.Round(cal.Gamma * 100)), int32(math.Round(anchor * 2)), int32(math.Round(lo)), int32(math.Round(hi)), int32(math.Round(cal.Arc * 1e5))}
	m.mu.Lock()
	if p, ok := m.plans[key]; ok {
		m.mu.Unlock()
		return p, p.W > 0
	}
	m.mu.Unlock()
	step := 1.0
	if hi-lo < 40 {
		step = 0.5
	}
	var ws, vals []float64
	plan := MissPlan{}
	for x := math.Ceil(lo/step) * step; x <= hi+1e-9; x += step {
		v := m.missFor(x, a, b, cal)
		if !finite(v) {
			continue
		}
		ws = append(ws, x)
		vals = append(vals, 1-v/1000)
		plan.Curve = append(plan.Curve, MissPt{W: round(x, 2), Miss: round(v, 2)})
	}
	if len(ws) >= 3 {
		an := clamp(anchor, lo, hi)
		// the width found so far stays only against differences too small
		// to show up in the game (3% of the misses)
		plan.W = bestScaleTol(ws, vals, lo, hi, an, 0.03)
		plan.Miss = round(m.missFor(plan.W, a, b, cal), 2)
		plan.MissAnchor = round(m.missFor(an, a, b, cal), 2)
		if !finite(plan.Miss) {
			plan = MissPlan{}
		}
	} else {
		plan = MissPlan{}
	}
	// a coarser curve is enough for showing
	if n := len(plan.Curve); n > 70 {
		var c []MissPt
		for i := 0; i < n; i += (n + 59) / 60 {
			c = append(c, plan.Curve[i])
		}
		plan.Curve = c
	}
	m.mu.Lock()
	if m.plans == nil || len(m.plans) > 400 {
		m.plans = map[planKey]MissPlan{}
	}
	m.plans[key] = plan
	m.mu.Unlock()
	return plan, plan.W > 0
}

// missFor is the predicted aim misses per 1000 notes with width w (see
// BestFor).
func (m *MissModel) missFor(w, a, b float64, cal PrecCal) float64 {
	g := math.Exp(a-b*math.Log(w)) - 1
	k := math.Sqrt(m.varAt(cal, w))
	s := w / m.ref
	// the arc: across the playfield (about 0.17 of the width either side of
	// the middle) the movements come out turned by different angles
	arc := cal.Arc * arcSpread * w
	var sum [NumCats]float64
	var n [NumCats]int
	for _, p := range m.pts {
		D := s * p.D
		V := s * p.V
		sp, sq := m.nf.Model.Sigma(D, V)
		sq = math.Hypot(k*sq, arc*D)
		pr := hitProbability(s*p.R, k*sp, sq, g*D)
		sum[p.cat] += pr
		n[p.cat]++
	}
	t, wt := 0.0, 0.0
	for c := 0; c < NumCats; c++ {
		if m.weights[c] > 0 && n[c] > 0 {
			t += m.weights[c] * sum[c] / float64(n[c])
			wt += m.weights[c]
		}
	}
	if wt == 0 {
		return math.NaN()
	}
	return (1 - t/wt) * 1000
}

// MissAt is the predicted aim misses per 1000 notes with width w (see
// BestFor).
func (m *MissModel) MissAt(w, a, b float64, cal PrecCal) float64 {
	if !m.OK() {
		return math.NaN()
	}
	return round(m.missFor(w, a, b, cal), 2)
}
