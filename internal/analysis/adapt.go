package analysis

import (
	"math"
	"sort"
	"time"

	"areacoach/internal/otd"
)

// The adaptive diagnosis: while the player plays, every stretch says how
// much longer or shorter than needed the jumps were and where the clicks
// landed. The area moves, a little at a time, to where both go to zero: the
// size the hand expects and the place where it plays. The model behind it
// learns from every stretch, and also how much the player's aim follows the
// area at all; as it gets surer, the changes get smaller.

// AdaptOpts are the rules of a diagnosis.
type AdaptOpts struct {
	Locked  bool    // forced proportions: the height follows the width
	Ratio   float64 // height / width of the screen part the area maps to
	TabletW float64
	TabletH float64
	// the area the player had before: where things stay when the
	// measurements say nothing
	Anchor otd.Rect
	// the place is kept still (the player chose where the wrist rests):
	// only the size adapts
	FixPos     bool
	PosX, PosY float64
	// the aim misses expected with each width (from the replays): the size
	// is the one with the fewest, near the one the hand is used to
	Miss *MissModel
	// without forced proportions: the shape (height / width) the area keeps
	// unless the jumps clearly ask for another (0: the shape it starts from)
	Shape float64
	// the width found so far: among widths practically as good, it stays
	// (0: the width in use)
	Keep float64
	// the place found so far (KeepPlace): toward where the hand is steadier
	// it moves a little at a time from there
	KeepPlace    bool
	KeepX, KeepY float64
	// the area set now
	Cur otd.Rect
	// the widths the player allows (0: no limit)
	MinW, MaxW float64
	// the size is kept as it is (the player chose it): only the place adapts
	FixSize      bool
	SizeW, SizeH float64
}

// AdaptDim is one quantity the diagnosis looks for.
type AdaptDim struct {
	Value float64 `json:"value"` // where it should be (mm)
	SD    float64 `json:"sd"`    // how unsure (mm)
	Slope float64 `json:"slope"` // how much the aim follows a change of the area (0: not at all, 1: fully)
	Bias  float64 `json:"bias"`  // what the last stretches measured: over/undershoot (share) or offset (mm)
}

// AdaptTrail is the state after one stretch.
type AdaptTrail struct {
	At     time.Time `json:"at"`
	Used   otd.Rect  `json:"used"`
	Target otd.Rect  `json:"target"`
	Src    string    `json:"src"`
}

// AdaptFit is what the diagnosis has found.
type AdaptFit struct {
	Target otd.Rect `json:"target"`
	W      AdaptDim `json:"w"`
	H      AdaptDim `json:"h"`
	X      AdaptDim `json:"x"`
	Y      AdaptDim `json:"y"`
	N      int      `json:"n"` // stretches used
	Sure   bool     `json:"sure"`
	// the width the hand is used to (no over/undershoot), when it is known
	// and the target is another one, with fewer aim misses expected over all
	// kinds of maps; the misses expected per 1000 notes with the target,
	// with it and with the width in use
	Natural  float64 `json:"natural,omitempty"`
	MissBest float64 `json:"missBest,omitempty"`
	MissNat  float64 `json:"missNat,omitempty"`
	MissCur  float64 `json:"missCur,omitempty"`
	CurW     float64 `json:"curW,omitempty"` // the width in use
	// the misses expected by width, and how the scatter measured during the
	// diagnosis compares with the model's
	MissCurve []MissPt `json:"missCurve,omitempty"`
	Prec      PrecCal  `json:"prec"`
	// how the aim is turned, and how that changes across the tablet
	Arc ArcFit `json:"arc"`
	// how close the area found is to a perfect one
	Score AdaptScore `json:"score"`
	// where the hand is steadier: how far the place moved for it (mm)
	Steady AdaptSteady `json:"steady"`
	// the widest area whose maps the hand can still play at their speed
	// (0: no limit known)
	Reach float64 `json:"reach,omitempty"`
	// when the best width is at a limit the player set (MinW, MaxW): the
	// best one without it, and the misses expected with it (0: not at a
	// limit, or not known)
	FreeW    float64 `json:"freeW,omitempty"`
	FreeMiss float64 `json:"freeMiss,omitempty"`
	// the bias line the misses expected were worked out with: jumps too
	// long by exp(BiasA - BiasB·ln w) - 1 with width w
	BiasA float64      `json:"biasA,omitempty"`
	BiasB float64      `json:"biasB,omitempty"`
	Trail []AdaptTrail `json:"trail,omitempty"`
}

// AdaptSteady is the place moved toward where the hand is steadier: the
// scatter of the clicks changes with where the area is on the tablet (GX,
// GY: log of the variance per mm, right and down), and against moving away
// from where the clicks land on the circles it weighs how many aim misses
// each costs. Shift is how far it moved the place for it (mm).
type AdaptSteady struct {
	GX     float64 `json:"gx"`
	GXSE   float64 `json:"gxse"`
	GY     float64 `json:"gy"`
	GYSE   float64 `json:"gyse"`
	ShiftX float64 `json:"shiftX"`
	ShiftY float64 `json:"shiftY"`
	// the places measured
	XLo float64 `json:"xlo,omitempty"`
	XHi float64 `json:"xhi,omitempty"`
	YLo float64 `json:"ylo,omitempty"`
	YHi float64 `json:"yhi,omitempty"`
	N   int     `json:"n"`
}

// AdaptScore is how close the area found is to a perfect one: an area the
// diagnosis is sure of (it would not move it any more), with which the aim
// misses nothing and the movements between the circles are straight. Missing
// is human, so 100% is not reached; it grows as the maps played make the
// area surer and the hand gets used to it.
type AdaptScore struct {
	Pct    float64 `json:"pct"`    // 0-100: sure × clean
	Sure   float64 `json:"sure"`   // 0-100: how sure the diagnosis is of the area
	Clean  float64 `json:"clean"`  // 0-100: how clean the aim is with it
	Spread float64 `json:"spread"` // how unsure the best width still is (share of it)
	Place  float64 `json:"place"`  // how unsure its place still is (mm)
	Miss   float64 `json:"miss"`   // aim misses per 1000 notes expected with it
	// how far the paths between circles bend from the straight line (share
	// of the jump), over the last maps; 0: not known
	Lines float64 `json:"lines,omitempty"`
}

// one quantity of the model: the bias y measured with the area setting u
// is y = a - b·u, where a drifts slowly over time (the hand changes) and b,
// how much the bias follows the area, is not known: several values are
// followed at once and weighed by how well they explain the data
type adaptDim struct {
	u, y, se func(p CalibPoint) float64
	tau      float64 // spread between stretches beyond the measurement error
	q        float64 // drift of the target per second of play (variance)
	gap      float64 // extra drift across a break of more than 20 minutes (variance)
	sigma0   float64 // how unsure the target is at the start
	u0       float64 // where it is at the start (the area before)
	lambda   float64 // pull to u0 when the bias does not follow the area
	bMed     float64 // likely value of b
	bLogSD   float64
	shifty   bool // sudden changes are likely (the hand moved): follow them
	// the bias may also not follow the area at all: a habit (always a little
	// to the right, say) that moving the area would only chase. These values
	// of b near zero are followed too, with a prior weight of lowLL, and the
	// bias at the start may differ from zero by habit (SD)
	lowB  []float64
	lowLL float64
	habit float64
	// where the hand is at home, for the pull when the bias does not follow
	// the area (nil: u0): u0 if the diagnosis started playing about there
	// (homeBase: u0 is a place the hand used, not the whole tablet), else
	// where it did start playing (the first stretches homeOK accepts)
	homeOK   func(p CalibPoint) bool
	homeBase bool
}

// How many stretches say where the diagnosis started playing, and how far
// from the area before that is somewhere else (mm).
const (
	homeStretches = 8
	homeFar       = AdaptStepMax
)

type adaptHyp struct {
	b, a, P, ll float64
}

type dimState struct {
	d     *adaptDim
	hs    []adaptHyp
	tPrev time.Time
	bias  float64 // smoothed bias, for showing
	nb    float64
	// where the hand played the first stretches (sum of the settings, count)
	hSum, hN float64
}

// home is where the area is pulled to when the bias does not follow it:
// where the hand was playing when the diagnosis got going. That is the area
// from before, if the diagnosis played about there; if it soon played
// somewhere else (it started from the whole tablet, and the clicks sent it
// elsewhere), the place it played then. Never where the area went later:
// chasing a habit (clicks always a little to the right, wherever the area
// is) would move that along with the area, for ever.
func (s *dimState) home() float64 {
	d := s.d
	if d.homeOK == nil || s.hN == 0 {
		return d.u0
	}
	m := s.hSum / s.hN
	if d.homeBase && math.Abs(m-d.u0) <= homeFar {
		return d.u0
	}
	return m
}

var adaptGrid = func() []float64 {
	var g []float64
	for i := 0; i < 16; i++ {
		g = append(g, math.Exp(math.Log(0.05)+float64(i)*(math.Log(1.5)-math.Log(0.05))/15))
	}
	return g
}()

func newDimState(d *adaptDim) *dimState {
	s := &dimState{d: d}
	h2 := d.habit * d.habit
	for _, b := range adaptGrid {
		lp := math.Log(b/d.bMed) / d.bLogSD
		s.hs = append(s.hs, adaptHyp{b: b, a: b * d.u0, P: b*b*d.sigma0*d.sigma0 + h2, ll: -0.5 * lp * lp})
	}
	for _, b := range d.lowB {
		s.hs = append(s.hs, adaptHyp{b: b, a: b * d.u0, P: b*b*d.sigma0*d.sigma0 + h2, ll: d.lowLL})
	}
	return s
}

func (s *dimState) add(p CalibPoint) {
	y, se, u := s.d.y(p), s.d.se(p), s.d.u(p)
	if !finite(y) || !finite(u) || se <= 0 || !finite(se) {
		return
	}
	tau := s.d.tau
	if p.Src == "live" {
		tau *= 1.3
	}
	R := se*se + tau*tau
	var q float64
	if !s.tPrev.IsZero() {
		dt := p.At.Sub(s.tPrev).Seconds()
		if dt > 0 {
			q = s.d.q * math.Min(dt, 300)
			if dt > 1200 {
				q += s.d.gap
			}
		}
	}
	if p.At.After(s.tPrev) {
		s.tPrev = p.At
	}
	if s.d.homeOK != nil && s.hN < homeStretches && s.d.homeOK(p) {
		s.hSum += u
		s.hN++
	}
	for i := range s.hs {
		h := &s.hs[i]
		h.P += h.b * h.b * q
		v := y - (h.a - h.b*u)
		S := h.P + R
		z := v / math.Sqrt(S)
		Ri := R
		if math.Abs(z) > 3 {
			if s.d.shifty {
				// a clear jump: the hand moved, the old estimate counts less
				h.P += 0.5 * (v*v - S)
			} else {
				// probably a bad stretch: it counts less
				Ri = R * z * z / 9
			}
			S = h.P + Ri
		}
		h.ll += -0.5 * (math.Log(S) + v*v/S)
		K := h.P / S
		h.a += K * v
		h.P *= 1 - K
	}
	// the bias as measured lately, for showing
	k := 1 / math.Min(s.nb+1, 4)
	s.bias += k * (y - s.bias)
	s.nb++
}

// line is the bias the model expects with the area setting u: a - b·u, on
// average over the values of b followed.
func (s *dimState) line() (a, b float64) {
	mx := math.Inf(-1)
	for _, h := range s.hs {
		mx = math.Max(mx, h.ll)
	}
	var sw float64
	for _, h := range s.hs {
		w := math.Exp(h.ll - mx)
		sw += w
		a += w * h.a
		b += w * h.b
	}
	return a / sw, b / sw
}

// follow is how much the bias is expected to follow the area (the mean of
// b², over the values of b followed).
func (s *dimState) follow() float64 {
	mx := math.Inf(-1)
	for _, h := range s.hs {
		mx = math.Max(mx, h.ll)
	}
	var sw, sbb float64
	for _, h := range s.hs {
		w := math.Exp(h.ll - mx)
		sw += w
		sbb += w * h.b * h.b
	}
	return sbb / sw
}

// target is where the bias would vanish, held back toward home when the
// bias does not seem to follow the area.
func (s *dimState) target() (u, sd, slope float64) {
	mx := math.Inf(-1)
	for _, h := range s.hs {
		mx = math.Max(mx, h.ll)
	}
	var sw, sab, sbb, sbm float64
	ws := make([]float64, len(s.hs))
	for i, h := range s.hs {
		ws[i] = math.Exp(h.ll - mx)
		sw += ws[i]
	}
	for i, h := range s.hs {
		w := ws[i] / sw
		sab += w * h.b * h.a
		sbb += w * h.b * h.b
		sbm += w * h.b
	}
	u0 := s.home()
	u = (sab + s.d.lambda*u0) / (sbb + s.d.lambda)
	var v float64
	for i, h := range s.hs {
		w := ws[i] / sw
		r := h.a - h.b*u
		v += w * (h.P + r*r)
	}
	sd = math.Sqrt(v / (sbb + s.d.lambda))
	return u, sd, sbm
}

// FitAdapt follows all the stretches in time order and returns where the
// area should be now.
func FitAdapt(pts []CalibPoint, o AdaptOpts, trail bool) AdaptFit {
	ratio := o.Ratio
	if ratio <= 0 {
		ratio = o.Anchor.H / o.Anchor.W
	}
	an := o.Anchor
	logW := func(p CalibPoint) float64 { return math.Log(p.Area.W) }
	logH := func(p CalibPoint) float64 { return math.Log(p.Area.H) }
	// the area before the diagnosis says where the hand is at home, unless
	// it was (nearly) the whole tablet: then it says nothing
	_, hi := AdaptRange(o)
	home := an.W < 0.9*hi
	sizeDim := func(u func(CalibPoint) float64, y, se func(CalibPoint) float64, u0 float64) *adaptDim {
		// the jumps may also be short or long by habit, whatever the size: a
		// hand that gets used to any area. Then no size fixes them, and the
		// size is chosen by the aim misses alone
		d := &adaptDim{u: u, y: y, se: se, tau: 0.02, q: 1e-4 / 600, gap: 0.03 * 0.03, sigma0: 0.35, u0: u0,
			lambda: 0.002, bMed: 0.75, bLogSD: 0.5, lowB: []float64{0.005, 0.02}, lowLL: -2.5, habit: 0.03}
		if !home {
			d.lambda, d.sigma0 = 0, 0.6
		}
		return d
	}
	// where the clicks land: when that does not change with the place of
	// the area (the hand gets used to wherever it is), the area stays where
	// the hand was at home when the diagnosis got going (see home). Not
	// always the area from before: the player may have moved since (another
	// desk, another grip), and if the diagnosis played elsewhere from the
	// start, pulling the area back there would move it for no reason the aim
	// shows. The whole tablet (where a diagnosis may start) says nothing
	// about where the hand is.
	notWhole := func(p CalibPoint) bool { return p.Area.W < 0.9*hi }
	centreDim := func(u, y, se func(CalibPoint) float64, u0 float64) *adaptDim {
		return &adaptDim{u: u, y: y, se: se, tau: 0.3, q: 0.64 / 600, gap: 2.5 * 2.5, sigma0: 10, u0: u0,
			lambda: 0.005, bMed: 0.5, bLogSD: 0.6, shifty: true,
			lowB: []float64{0.003, 0.01, 0.025}, lowLL: -1.2, habit: 0.5, homeOK: notWhole, homeBase: home}
	}
	var dw, dh *adaptDim
	if o.Locked {
		dw = sizeDim(logW, func(p CalibPoint) float64 { return p.G }, func(p CalibPoint) float64 { return p.SG }, math.Log(an.W))
	} else {
		dw = sizeDim(logW, func(p CalibPoint) float64 { return p.GX }, func(p CalibPoint) float64 { return p.SGX }, math.Log(an.W))
		// the shape: how much shorter or longer the vertical jumps are than
		// the horizontal ones. A difference that stays whatever the shape
		// (vertical jumps a little short by habit, say) looks the same as a
		// hand used to another shape; making the area narrower for it makes
		// the vertical aim twitchier without fixing anything. So the shape
		// keeps to the one it started from and follows the jumps only in part,
		// and only as far as the difference changes with the shape.
		dh = &adaptDim{u: func(p CalibPoint) float64 { return logH(p) - logW(p) },
			y:   func(p CalibPoint) float64 { return p.GY - p.GX },
			se:  func(p CalibPoint) float64 { return math.Hypot(p.SGX, p.SGY) },
			tau: 0.03, q: 1e-4 / 600, gap: 0.03 * 0.03, sigma0: 0.2, u0: math.Log(ShapePrior(o)),
			lambda: adaptShapeHold, bMed: 0.6, bLogSD: 0.6}
	}
	dx := centreDim(func(p CalibPoint) float64 { return p.Area.X }, func(p CalibPoint) float64 { return p.DX }, func(p CalibPoint) float64 { return p.SDX }, an.X)
	dy := centreDim(func(p CalibPoint) float64 { return p.Area.Y }, func(p CalibPoint) float64 { return p.DY }, func(p CalibPoint) float64 { return p.SDY }, an.Y)
	sw, sx, sy := newDimState(dw), newDimState(dx), newDimState(dy)
	var sh *dimState
	if dh != nil {
		sh = newDimState(dh)
	}
	ps := make([]CalibPoint, 0, len(pts))
	for _, p := range pts {
		if p.SG > 0 && p.Area.Valid() {
			ps = append(ps, p)
		}
	}
	sort.SliceStable(ps, func(i, j int) bool { return ps[i].At.Before(ps[j].At) })
	out := AdaptFit{N: len(ps)}
	curA := o.Cur
	if !curA.Valid() && len(ps) > 0 {
		curA = ps[len(ps)-1].Area
	}
	out.Arc = arcFit(ps, curA)
	// how different the widths measured were
	wLo, wHi := math.Inf(1), math.Inf(-1)
	for _, p := range ps {
		if p.Area.Valid() {
			wLo, wHi = math.Min(wLo, p.Area.W), math.Max(wHi, p.Area.W)
		}
	}
	wSpan := 0.0
	if wHi > wLo {
		wSpan = math.Log(wHi / wLo)
	}
	build := func(final bool) otd.Rect {
		uw, sdw, bw := sw.target()
		ux, sdx, bx := sx.target()
		uy, sdy, by := sy.target()
		w := math.Exp(uw)
		// the width with the fewest aim misses expected over all the kinds of
		// maps played: what the jumps measured says how far off they are with
		// each width (only as much as that follows the width: a habit no width
		// fixes is not chased), the replays how the hand scatters with the
		// movements each width needs, the stretches of the diagnosis how
		// steady the hand really was with the widths tried
		k := 1.0
		out.Natural, out.MissBest, out.MissNat, out.MissCur, out.CurW, out.MissCurve = 0, 0, 0, 0, 0, nil
		// how unsure the best width is, from what decides it (log)
		spread := sdw
		var ma, mb float64
		model := false
		if final && o.Miss.OK() && sw.nb >= 3 {
			lo, hi := AdaptRange(o)
			a, b := sw.line()
			ma, mb, model = a, b, true
			out.BiasA, out.BiasB = round(a, 5), round(b, 5)
			cur := w
			if curA.Valid() {
				cur = curA.W
			}
			keep := cur
			if o.Keep > 0 {
				keep = o.Keep
			}
			out.Prec = o.Miss.Calibrate(ps)
			if out.Arc.Strong {
				// the arc turns movements more the further they reach across
				// the area: a wider area pays for it
				out.Prec.Arc = math.Hypot(out.Arc.GX, out.Arc.GY) / 10 * math.Pi / 180
			}
			// where there is something to go by: near the width with no
			// over/undershoot (or the one in use, when the jumps do not say),
			// the widths the hand was measured with, the one of the replays
			c := cur
			if b >= 0.15 {
				if nat := math.Exp(a / b); nat >= lo && nat <= hi {
					c = nat
				}
			}
			tlo, thi := 0.8*c, 1.25*c
			if out.Prec.N >= 4 {
				tlo, thi = math.Min(tlo, 0.9*out.Prec.Lo), math.Max(thi, 1.1*out.Prec.Hi)
			}
			if hw := o.Miss.Home(); hw > 0 {
				tlo, thi = math.Min(tlo, 0.9*hw), math.Max(thi, 1.1*hw)
			}
			tlo, thi = math.Max(tlo, lo), math.Min(thi, hi)
			out.Reach = round(o.Miss.Reach(out.Prec), 2)
			if plan, ok := o.Miss.BestFor(a, b, out.Prec, keep, tlo, thi); ok {
				k = plan.W / w
				out.MissBest, out.CurW, out.MissCurve = plan.Miss, round(cur, 3), plan.Curve
				// the trend of the steadiness with the width is known only so
				// well: the best width with it a little steeper or flatter
				g := 0.12
				if out.Prec.N >= 4 && out.Prec.SE > 0 {
					cl, ch := out.Prec, out.Prec
					cl.Gamma -= out.Prec.SE
					ch.Gamma += out.Prec.SE
					p1, ok1 := o.Miss.BestFor(a, b, cl, 0, tlo, thi)
					p2, ok2 := o.Miss.BestFor(a, b, ch, 0, tlo, thi)
					if ok1 && ok2 {
						g = math.Abs(math.Log(curveMin(p2.Curve)/curveMin(p1.Curve))) / 2
					}
				}
				spread = math.Hypot(g, 0.5*sdw)
				out.MissCur = o.Miss.MissAt(cur, a, b, out.Prec)
				if (o.MinW > 0 || o.MaxW > 0) && (plan.W <= lo*1.005 || plan.W >= hi*0.995) {
					// pressed against a limit the player set: where it would be
					// without it
					of := o
					of.MinW, of.MaxW = 0, 0
					flo, fhi := AdaptRange(of)
					clo, chi := 0.8*c, 1.25*c
					if out.Prec.N >= 4 {
						clo, chi = math.Min(clo, 0.9*out.Prec.Lo), math.Max(chi, 1.1*out.Prec.Hi)
					}
					if plan.W <= lo*1.005 {
						clo = math.Min(clo, 0.85*lo)
					} else {
						chi = math.Max(chi, 1.15*hi)
					}
					clo, chi = math.Max(clo, flo), math.Min(chi, fhi)
					if fp, ok := o.Miss.BestFor(a, b, out.Prec, 0, clo, chi); ok && math.Abs(math.Log(fp.W/plan.W)) > 0.01 {
						out.FreeW, out.FreeMiss = round(fp.W, 2), round(fp.Miss, 2)
					}
				}
				// the width with no over/undershoot, when the bias follows the
				// width enough to say
				if b >= 0.15 {
					if nat := math.Exp(a / b); nat >= lo && nat <= hi && math.Abs(nat/plan.W-1) > 0.01 {
						out.Natural = round(nat, 3)
						out.MissNat = o.Miss.MissAt(nat, a, b, out.Prec)
					}
				}
			}
		}
		if final && out.MissBest == 0 && sw.nb >= 6 && curA.Valid() && wSpan >= math.Log(1.15) {
			// without the misses expected by width yet: when the jumps do not
			// change with the size, over sizes different enough to tell, the
			// size in use stays for now
			if _, b := sw.line(); b < 0.12 {
				w = curA.W
			}
		}
		w *= k
		if o.FixSize && o.SizeW > 0 {
			w, spread = o.SizeW, 0
		}
		h := w * ratio
		out.W = AdaptDim{Value: round(w, 3), SD: round(w*sdw, 3), Slope: round(bw, 3), Bias: round(sw.bias, 4)}
		if sh != nil {
			us, sds, bs := sh.target()
			h = w * math.Exp(us)
			if o.FixSize && o.SizeW > 0 && o.SizeH > 0 {
				h = o.SizeH
			}
			// the bias shown is the vertical one
			out.H = AdaptDim{Value: round(h, 3), SD: round(h*math.Hypot(sdw, sds), 3), Slope: round(bs, 3), Bias: round(sw.bias+sh.bias, 4)}
		}
		switch {
		case o.FixPos:
			ux, uy, sdx, sdy = o.PosX, o.PosY, 0, 0
		case final && out.Arc.Strong && curA.Valid():
			// sideways, the area goes toward where straight movements come
			// out straight, as far as the hand lands where it should there
			ux = arcPlace(&out.Arc, curA, ux, bx, w)
			if an.Valid() {
				ux = clamp(ux, an.X-arcTotalMax, an.X+arcTotalMax)
			}
			out.Arc.Shift = round(ux-curA.X, 2)
		}
		if final && !o.FixPos && out.Prec.N >= 8 && out.Prec.Sig2 > 0 {
			// toward where the hand is steadier. The scatter's share of aim
			// misses against that of the offset from the circles: with
			// clicks scattered by σ around where they aim, the misses grow
			// with the variance as R²/2σ² per log, and with an offset d as
			// R²/2σ⁴ per mm²: moving by Δ costs (b·Δ)² of offset where the
			// clicks follow the area by b, nothing where they do not (a
			// habit: the wrist moves with the area), so the steadier side
			// wins more the less the place changes where the clicks land.
			// Never beyond the places measured, but for a little.
			pc := out.Prec
			if o.KeepPlace {
				// around the place found so far
				if pn := o.Miss.CalibrateNear(ps, o.KeepX, o.KeepY); pn.N >= 8 && pn.Sig2 > 0 {
					pc = pn
				}
			}
			steer := func(u, g, gse float64, st *dimState, lo, hi float64) (float64, float64) {
				// only as much of the trend as it is sure of: where the clicks
				// do not follow the area, even a small trend would move it a
				// lot
				if gse > 0 {
					z2 := (g / gse) * (g / gse)
					g *= z2 / (1 + z2)
				}
				den := 2 * (st.follow() + st.d.lambda)
				d := -pc.Sig2 * g / den
				nu := clamp(u+d, math.Min(u, lo-steadyBeyond), math.Max(u, hi+steadyBeyond))
				return nu, nu - u
			}
			var shx, shy float64
			ux, shx = steer(ux, pc.GX, pc.GXSE, sx, pc.XLo, pc.XHi)
			uy, shy = steer(uy, pc.GY, pc.GYSE, sy, pc.YLo, pc.YHi)
			out.Steady = AdaptSteady{GX: pc.GX, GXSE: pc.GXSE, GY: pc.GY, GYSE: pc.GYSE, ShiftX: round(shx, 2), ShiftY: round(shy, 2),
				XLo: pc.XLo, XHi: pc.XHi, YLo: pc.YLo, YHi: pc.YHi, N: pc.N}
		}
		if final && !o.FixPos && o.KeepPlace && curA.Valid() && curA.W < 0.9*hi {
			// where the clicks do not follow the area (the wrist goes where
			// the area goes), moving it does not put them on the circles: the
			// place found moves only a little a map from where it was, so as
			// not to chase a habit, and the hand is not made to move for
			// nothing. Where they do follow it (the hand stays where it is on
			// the tablet), as far as a map allows
			lim := func(st *dimState) float64 {
				f := clamp((st.follow()-0.02)/(0.09-0.02), 0, 1)
				return steadyStep + f*(AdaptStepMax-steadyStep)
			}
			lx, ly := lim(sx), lim(sy)
			ux = clamp(ux, o.KeepX-lx, o.KeepX+lx)
			uy = clamp(uy, o.KeepY-ly, o.KeepY+ly)
		}
		if final && !o.FixPos && curA.Valid() && curA.W < 0.9*hi {
			// a few millimetres a map: the hand follows a big move better in
			// steps, and each step is measured before the next (from the whole
			// tablet, where a diagnosis may start, it goes at once)
			ux = clamp(ux, curA.X-AdaptStepMax, curA.X+AdaptStepMax)
			uy = clamp(uy, curA.Y-AdaptStepMax, curA.Y+AdaptStepMax)
		}
		out.X = AdaptDim{Value: round(ux, 3), SD: round(sdx, 3), Slope: round(bx, 3), Bias: round(sx.bias, 3)}
		out.Y = AdaptDim{Value: round(uy, 3), SD: round(sdy, 3), Slope: round(by, 3), Bias: round(sy.bias, 3)}
		r := AdaptShape(otd.Rect{W: w, H: h, X: ux, Y: uy, Rot: an.Rot}, o)
		if o.Locked {
			out.H = AdaptDim{Value: r.H, SD: round(out.W.SD*ratio, 3), Slope: out.W.Slope, Bias: out.W.Bias}
		}
		if final {
			// the place counts only as far as it changes where the clicks land
			place := 0.0
			if !o.FixPos {
				place = math.Hypot(sdx*math.Min(1, bx/0.3), sdy*math.Min(1, by/0.3))
			}
			miss := math.NaN()
			if model {
				miss = o.Miss.MissAt(r.W, ma, mb, out.Prec)
			}
			out.Score = adaptScore(ps, spread, place/math.Max(r.W, 1), place, miss)
		}
		return r
	}
	for _, p := range ps {
		sw.add(p)
		if sh != nil {
			sh.add(p)
		}
		sx.add(p)
		sy.add(p)
		if trail {
			out.Trail = append(out.Trail, AdaptTrail{At: p.At, Used: p.Area, Target: build(false), Src: p.Src})
		}
	}
	out.Target = build(true)
	// where the area is counts only if it changes the aim
	placeOK := func(d AdaptDim) bool { return d.SD <= 2 || d.Slope < 0.1 }
	out.Sure = out.N >= 20 && out.W.SD <= 1.5 && placeOK(out.X) && placeOK(out.Y) && (o.Locked || out.H.SD <= 1.5)
	return out
}

// AdaptStepMax is how far the place of the area moves at most from one map
// to the next (mm).
const AdaptStepMax = 6.0

// steadyBeyond is how far beyond the places measured the area may go toward
// where the hand is steadier (mm), steadyStep how far a map from the place
// found before.
const (
	steadyBeyond = 2.0
	steadyStep   = 1.5
)

// adaptShapeHold is how much the shape keeps to the one it started from,
// against the jumps: with the usual following of the area, about half of a
// difference between the vertical and the horizontal jumps goes into the
// shape; a difference that does not follow the shape, none.
const adaptShapeHold = 0.1

// ShapePrior is the shape (height / width) an area without forced
// proportions keeps unless the jumps ask for another.
func ShapePrior(o AdaptOpts) float64 {
	if o.Shape > 0 {
		return o.Shape
	}
	s := AdaptStart(o)
	return s.H / s.W
}

// AdaptRange is the smallest and largest width the diagnosis uses: what
// fits the tablet, within the widths the player allows.
func AdaptRange(o AdaptOpts) (lo, hi float64) {
	ratio := o.Ratio
	if ratio <= 0 {
		ratio = o.Anchor.H / o.Anchor.W
	}
	hi = 160
	if o.TabletW > 0 && o.TabletH > 0 {
		hi = o.TabletW
		if o.Locked {
			hi = math.Min(o.TabletW, o.TabletH/ratio)
		}
	}
	lo = math.Max(30, hi*0.25)
	if o.MinW > 0 {
		lo = math.Min(math.Max(lo, o.MinW), hi)
	}
	if o.MaxW > 0 {
		hi = math.Max(math.Min(hi, o.MaxW), lo)
	}
	return lo, hi
}

// AdaptStart is the area the diagnosis starts from: the whole tablet (with
// forced proportions, the largest area with the screen's shape), centred
// where the player's area was as far as it fits.
func AdaptStart(o AdaptOpts) otd.Rect {
	_, hi := AdaptRange(o)
	r := otd.Rect{W: hi, H: hi * o.Ratio, X: o.Anchor.X, Y: o.Anchor.Y, Rot: o.Anchor.Rot}
	if !o.Locked && o.TabletH > 0 {
		r.H = o.TabletH
	}
	return AdaptShape(r, o)
}

// AdaptShape keeps an area within the rules: the proportions, sensible
// sizes and shapes, inside the tablet.
func AdaptShape(r otd.Rect, o AdaptOpts) otd.Rect {
	ratio := o.Ratio
	if ratio <= 0 {
		ratio = o.Anchor.H / o.Anchor.W
	}
	lo, hi := AdaptRange(o)
	r.W = clamp(r.W, lo, hi)
	if o.Locked {
		r.H = r.W * ratio
	} else {
		r.H = r.W * clamp(r.H/r.W, ratio*0.7, ratio*1.45)
		if o.TabletH > 0 && r.H > o.TabletH {
			r.H = o.TabletH
		}
	}
	r = fitIn(r, o.TabletW, o.TabletH)
	return otd.Rect{W: round(r.W, 3), H: round(r.H, 3), X: round(r.X, 3), Y: round(r.Y, 3), Rot: r.Rot}
}

// AdaptNext is the area of the next map: the target, within the rules.
func AdaptNext(cur, target otd.Rect, o AdaptOpts) otd.Rect { return AdaptShape(target, o) }

// AdaptMove is how far the edges of the area move with a change (mm).
func AdaptMove(a, b otd.Rect) float64 {
	return math.Max(math.Abs(b.W-a.W)/2+math.Abs(b.X-a.X), math.Abs(b.H-a.H)/2+math.Abs(b.Y-a.Y))
}

// The stretches a map is measured in: about this long, never with fewer
// circles than this.
const (
	AdaptMinNotes = 40
	AdaptMaxSec   = 45.0
)
