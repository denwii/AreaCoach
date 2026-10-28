package engine

import (
	"errors"
	"fmt"
	"log"
	"math"
	"sort"
	"time"

	"areacoach/internal/analysis"
	"areacoach/internal/filter"
	"areacoach/internal/otd"
	"areacoach/internal/store"
)

// The filter, guided map by map. The program never touches the filter in
// OpenTabletDriver: it says which values of AbstractQbit's Radial Follow to
// put there for the next map, the player puts them, and the replay of the
// map says how they went. Like the area, every map tries values a little
// stronger or lighter than the best ones so far, and every map played says
// where the best ones are.
//
// What decides: the simulation of each setting on the movements recorded
// without a filter (how many clicks it would put in the circle or out of it,
// how much the cursor would still tremble on the circle, how late it would
// get there), corrected by what the game measures with each setting really
// played: the aim misses against those expected on the same notes without a
// filter, and how much the cursor really trembled. The game can only tell
// how the hand changes with a filter on; the simulation knows the filter
// itself well, so the game tilts the simulation rather than replacing it.

// filterOn tells whether a map was played with Radial Follow on (before 2.2
// also a key of the filter test).
func filterOn(key string) bool { return key != "" && key != "off" && key != "mixed" }

// The default shape and strength, before the simulation can say: some
// deadzone, at a strength most hands do well with.
var (
	coachDefShape = filter.Shape{Inner: 0.4, Coef: 0.5, Knee: 1, Leak: 0}
	coachDefOuter = 0.8
	// with as little latency as possible: lighter
	coachLowOuter = 0.35
)

// coach limits: the strongest filter tried may hold the cursor back this
// much on average (ms); the tries are this much stronger or lighter than the
// best (log of the radius) while learning, then this much.
const (
	coachMaxDelay  = 15.0
	coachLowDelay  = 3.0  // the same, with as little latency as possible
	coachLagCost   = 0.03 // and then each ms of delay counts as this share of aim misses
	coachTryLearn  = 0.35
	coachTrySettle = 0.2
	coachTrustStep = 0.5  // the best radius moves at most this much a map (log)
	coachTremGood  = 0.02 // wobble on the circle that counts as still (share of the radius)
	coachFeel      = 0.04 // how much a wobble of coachTremGood counts, as a share of aim misses
	coachOffEvery  = 4    // without enough maps played with no filter: one every this many
	coachOffNeed   = 1.5  // maps with no filter, counted as recent ones
)

/* ---------- what OpenTabletDriver has ---------- */

// filterSeenNow reads the Radial Follow of the live settings and records it
// when it changed. Callers hold ls.mu.
func (e *Engine) filterSeenNow(ls *adaptState, c *store.CalibSession) {
	if ls.settings == nil {
		return
	}
	env := e.Env()
	r, on, found, prof := otd.RadialIn(ls.settings, env.Setup.TabletName)
	if !found {
		return
	}
	seen := store.FilterSeen{At: time.Now(), On: on}
	if on {
		k := 1.0
		if !r.Tablet {
			// screen pixels: in millimetres of the area in use
			if a := c.Cur; a.Valid() && prof.Display.W > 0 {
				k = prof.Display.W / a.W
			} else if env.Setup.Area.W > 0 && env.Setup.Display.W > 0 {
				k = env.Setup.Display.W / env.Setup.Area.W
			}
		}
		seen.Tablet = r.Tablet
		seen.Params = filter.Params{Outer: round4(r.Outer / k), Inner: round4(r.Inner / k), Coef: r.Coef, Knee: r.Knee, Leak: r.Leak}
	}
	if n := len(c.FSeen); n > 0 {
		last := c.FSeen[n-1]
		if last.On == seen.On && (!on || (filter.SameParams(last.Params, seen.Params) && last.Tablet == seen.Tablet)) {
			return
		}
	}
	e.st.Update(func(s *store.State) {
		if s.Calib != nil && s.Calib.Started.Equal(c.Started) {
			s.Calib.FSeen = append(s.Calib.FSeen, seen)
			if n := len(s.Calib.FSeen); n > 300 {
				s.Calib.FSeen = s.Calib.FSeen[n-300:]
			}
		}
	})
	c.FSeen = append(c.FSeen, seen)
	if on {
		log.Printf("diagnosis: OpenTabletDriver now has Radial Follow %s", paramsTxt(seen.Params))
	} else {
		log.Printf("diagnosis: Radial Follow is now off in OpenTabletDriver")
	}
}

// filterSeenLag is how late a change of the filter may be seen.
const filterSeenLag = 5 * time.Second

// filterAt is the filter a map played between start and end was played
// with: "off", "on" (with its values), "mixed" (it changed meanwhile), or ""
// (not known), and where that is known from.
func (e *Engine) filterAt(c *store.CalibSession, start, end time.Time) (string, *filter.Params, string) {
	if len(c.FSeen) > 0 {
		// OpenTabletDriver is looked at every few seconds, so a change is seen
		// a little after it was made: one seen in the first seconds of the map
		// (whose start is known only roughly, too) was made before it
		from := start.Add(filterSeenLag)
		var cur *store.FilterSeen
		for i := range c.FSeen {
			s := &c.FSeen[i]
			switch {
			case !s.At.After(from):
				cur = s
			case s.At.Before(end):
				if cur != nil || i > 0 {
					return "mixed", nil, "otd"
				}
				// the first look came after it started: what it saw then
				cur = s
			}
		}
		if cur != nil {
			if !cur.On {
				return "off", nil, "otd"
			}
			p := cur.Params
			return "on", &p, "otd"
		}
	}
	// OpenTabletDriver could not be read: the values asked, if the player
	// said they were in before the map started
	if fc := c.FCoach; fc != nil && !fc.Confirmed.IsZero() && fc.Confirmed.After(fc.AskAt) && fc.Confirmed.Before(start) {
		if !fc.AskOn {
			return "off", nil, "ok"
		}
		p := fc.Ask
		return "on", &p, "ok"
	}
	// what the settings file had then
	st := e.st.State()
	id := store.EpochAt(st.Epochs, start)
	for _, x := range st.Epochs {
		if x.ID == id && x.Setup.Smoothing == "" {
			return "off", nil, "file"
		}
	}
	return "", nil, ""
}

// staleArea tells whether an area OpenTabletDriver got is one of the
// diagnosis's old ones, or the one from before it: the numbers its window
// still shows, sent back with Apply or Save (as when the filter is changed
// there).
func staleArea(a otd.Rect, c *store.CalibSession) bool {
	if sameArea(a, c.Base) || sameArea(a, c.Start) {
		return true
	}
	for _, s := range c.Switches {
		if sameArea(a, s.Area) {
			return true
		}
	}
	return false
}

/* ---------- the decision ---------- */

// coachIn is what the guided filter decides from.
type coachIn struct {
	fam      *filter.Family
	maps     []store.AdaptMap
	hold     bool
	dither   int
	prev     *filter.Params // the best one before this map (nil: none yet)
	areaMiss float64        // aim misses per 1000 notes expected with the area found (0: unknown)
	lowLag   bool           // as little latency as possible
	// the width of the area found (0: unknown): the aim misses expected are
	// trusted only on maps played about that wide (far from it, as at the
	// start of a diagnosis, they say more about the area than the filter)
	width float64
}

// coachNearW is how far (log of the width) from the area found a map may
// have been played to count for the filter; coachHalfLife is after how many
// maps one counts half.
const (
	coachNearW    = 0.15
	coachHalfLife = 10.0
)

// coachMapSD is how much the aim misses against those expected change from
// map to map beyond the counting (log): some maps suit the hand more than
// the model knows.
const coachMapSD = 0.3

// coachOut is what the guided filter decided.
type coachOut struct {
	Best   filter.Params `json:"best"`
	BestOn bool          `json:"bestOn"`
	Ask    filter.Params `json:"ask"`
	AskOn  bool          `json:"askOn"`
	AskTry float64       `json:"askTry"` // log: stronger (+) or lighter than the best
	AskRef bool          `json:"askRef"` // the next map without a filter, to compare
	// the best filter against none: the share of aim misses less (+), with
	// its uncertainty; known: there are maps with no filter to compare with
	Improve float64 `json:"improve"`
	ImpLo   float64 `json:"impLo"`
	ImpHi   float64 `json:"impHi"`
	Known   bool    `json:"known"`
	// how close to a perfect filter (0-100): how sure, and how clean the aim
	// is with it: few aim misses, a still cursor on the circle, on time
	Pct   float64 `json:"pct"`
	Sure  float64 `json:"sure"`
	Clean float64 `json:"clean"`
	Aim   float64 `json:"aim"`
	Still float64 `json:"still"`
	Time  float64 `json:"time"`
	// the wobble on the circle with the best and with no filter (share of
	// the radius), the extra lag and the clicks it puts out of the circle
	// (per 1000), from the simulation
	Trem    float64 `json:"trem"`
	TremOff float64 `json:"tremOff"`
	Delay   float64 `json:"delay"`
	Lost    float64 `json:"lost"`
	Gain    float64 `json:"gain"` // simulated: clicks put in the circle more than out, per 1000
	Tilt    float64 `json:"tilt"` // what the game says beyond the simulation: aim misses per log of radius
	TiltSE  float64 `json:"tiltSE"`
	PlaceSE float64 `json:"placeSE"`          // how unsure the best strength is (log of the radius)
	NOn     int     `json:"nOn"`              // maps measured with a known filter on
	NOff    int     `json:"nOff"`             // and with none
	Sim     bool    `json:"sim"`              // the simulation could be used
	LowLag  bool    `json:"lowLag"`           // as little latency as possible
	MaxLag  float64 `json:"maxLag"`           // the most delay it proposes (ms)
	SimSrc  string  `json:"simSrc,omitempty"` // on what: "diagnosis", "replay"
	Note    string  `json:"note,omitempty"`
}

type coachMap struct {
	u, y, v float64 // log outer radius; log aim misses over expected; its variance
	a, far  float64 // the simulation's log aim misses for its setting; how unlike the shapes it is
	trem    float64
	curN    int
	simTrem float64
}

// simLogMiss is the simulated change of the aim misses with a setting (log).
func simLogMiss(e filter.Eval, missRaw float64) float64 {
	return math.Log((math.Max(0, missRaw-e.Gain) + 5) / (missRaw + 5))
}

// coachNoSim is the guess without a simulation: about as good a little
// either side of the default strength def.
func coachNoSim(u, def float64) float64 {
	d := u - math.Log(def)
	return 0.3*d*d - 0.04
}

func feel(t float64) float64 { return coachFeel * math.Min(t/coachTremGood, 3) }

func coachDecide(in coachIn) coachOut {
	var out coachOut
	fam := in.fam
	maxDelay, lagCost, defOuter := coachMaxDelay, 0.0, coachDefOuter
	if in.lowLag {
		maxDelay, lagCost, defOuter = coachLowDelay, coachLagCost, coachLowOuter
	}
	out.LowLag, out.MaxLag = in.lowLag, maxDelay
	out.Sim = fam.OK()
	missRaw := 0.0
	if out.Sim {
		missRaw, out.SimSrc = fam.MissRaw, fam.Src
	}
	// the maps: with no filter (the reference), with a known one
	var offSum, offW float64
	var offTrem []float64
	var ms []coachMap
	recent := in.maps
	if len(recent) > 80 {
		recent = recent[len(recent)-80:]
	}
	var rateMiss, rateClicks float64
	// the later maps count more: the hand gets used to the area and to the
	// filter, and early on the area is still moving (a filter played then
	// would carry the blame of an area not yet found)
	played := 0
	for _, m := range recent {
		if m.Windows > 0 {
			played++
		}
	}
	age := played
	offEff := 0.0
	for _, m := range recent {
		if m.Windows == 0 {
			continue
		}
		age--
		fade := math.Pow(0.5, float64(age)/coachHalfLife)
		rateMiss += float64(m.AimMiss)
		rateClicks += float64(m.Clicks)
		if in.width > 0 && m.Area.Valid() && math.Abs(math.Log(m.Area.W/in.width)) > coachNearW {
			continue
		}
		switch {
		case m.Filter == "off":
			out.NOff++
			offEff += fade
			if m.Exp > 0 {
				y := math.Log((float64(m.AimMiss) + 0.5) / (m.Exp + 0.5))
				w := fade / (1/(float64(m.AimMiss)+0.5) + coachMapSD*coachMapSD)
				offSum += w * y
				offW += w
			}
			if m.CurN > 0 {
				offTrem = append(offTrem, m.Trem)
			}
		case m.Filter == "on" && m.FParams != nil && m.FParams.Outer > 0:
			out.NOn++
			if !(m.Exp > 0) {
				continue
			}
			p := *m.FParams
			cm := coachMap{u: math.Log(p.Outer), trem: m.Trem, curN: m.CurN}
			cm.y = math.Log((float64(m.AimMiss) + 0.5) / (m.Exp + 0.5))
			cm.v = 1/(float64(m.AimMiss)+0.5) + coachMapSD*coachMapSD
			if out.Sim {
				ev, far := fam.At(p)
				cm.a, cm.far, cm.simTrem = simLogMiss(ev, missRaw), far, ev.Trem
			} else {
				cm.a, cm.far = coachNoSim(cm.u, defOuter), 0.5
			}
			cm.v += (0.15 * cm.far) * (0.15 * cm.far)
			cm.v /= fade
			ms = append(ms, cm)
		}
	}
	// the reference: the aim misses against those expected with no filter
	// (as a count, held toward "as expected" with few of them)
	yOff, vOff := 0.0, 0.25
	if offW > 0 {
		out.Known = true
		yOff, vOff = offSum/offW, 1/offW
	}
	// what the game says beyond the simulation: a shift (d0, how much
	// better or worse any filter does than simulated), a tilt (d1, by
	// strength) and a bend (d2: the best strength is sharper or flatter than
	// simulated), with priors that keep to the simulation until the maps say
	// otherwise
	ubar, sw := 0.0, 0.0
	for _, m := range ms {
		ubar += m.u / m.v
		sw += 1 / m.v
	}
	if sw > 0 {
		ubar /= sw
	} else if in.prev != nil && in.prev.Outer > 0 {
		ubar = math.Log(in.prev.Outer)
	} else {
		ubar = math.Log(defOuter)
	}
	var A [3][3]float64
	var bv [3]float64
	A[0][0], A[1][1], A[2][2] = 1/(0.5*0.5), 1/(0.4*0.4), 1/(0.4*0.4)
	if !out.Known {
		// nothing to compare with: the level is free (the aim misses expected
		// may be off for this player), only the shape of the curve counts
		A[0][0] = 1e-6
	}
	for _, m := range ms {
		r := m.y - yOff - m.a
		x := m.u - ubar
		f := [3]float64{1, x, x * x}
		w := 1 / m.v
		for i := 0; i < 3; i++ {
			bv[i] += w * f[i] * r
			for j := 0; j < 3; j++ {
				A[i][j] += w * f[i] * f[j]
			}
		}
	}
	V := inv3(A)
	var d [3]float64
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			d[i] += V[i][j] * bv[j]
		}
	}
	if !out.Known {
		// with no map without a filter, the shift against none is not known
		d[0] = 0
		V[0][0], V[0][1], V[1][0], V[0][2], V[2][0] = 0.5*0.5, 0, 0, 0, 0
	}
	V[0][0] += vOff
	corr := func(u float64) float64 { x := u - ubar; return d[0] + d[1]*x + d[2]*x*x }
	corrVar := func(u float64) float64 {
		x := u - ubar
		f := [3]float64{1, x, x * x}
		v := 0.0
		for i := 0; i < 3; i++ {
			for j := 0; j < 3; j++ {
				v += f[i] * V[i][j] * f[j]
			}
		}
		return math.Max(0, v)
	}
	out.Tilt, out.TiltSE = round4(d[1]), round4(math.Sqrt(V[1][1]))
	// the wobble: the simulation's, scaled to what the replays measured
	kT := 1.0
	if out.Sim {
		var rs []float64
		for _, m := range ms {
			if m.simTrem > 0.002 && m.curN > 0 {
				rs = append(rs, m.trem/m.simTrem)
			}
		}
		if len(rs) > 0 {
			sort.Float64s(rs)
			kT = clampF(rs[len(rs)/2], 0.3, 3)
		}
	}
	tremOff := 0.0
	if len(offTrem) > 0 {
		sort.Float64s(offTrem)
		tremOff = offTrem[len(offTrem)/2]
	} else if out.Sim {
		tremOff = fam.TremRaw
	}
	out.TremOff = round4(tremOff)
	// every setting of the family: aim misses against none, and the feel
	type cell struct {
		i, k     int
		u, eff   float64
		obj      float64
		ev       filter.Eval
		ok       bool
		tremCell float64
	}
	outers, shapes := filter.FamilyOuters, filter.FamilyShapes
	if out.Sim {
		outers, shapes = fam.Outers, fam.Shapes
	}
	defK := 0
	for k, s := range shapes {
		if s == coachDefShape {
			defK = k
		}
	}
	grid := make([][]cell, len(outers))
	best := cell{obj: math.Inf(1)}
	uPrev := math.NaN()
	if in.prev != nil && in.prev.Outer > 0 && out.NOn > 0 {
		uPrev = math.Log(in.prev.Outer)
	}
	for i, o := range outers {
		grid[i] = make([]cell, len(shapes))
		u := math.Log(o)
		for k := range shapes {
			c := cell{i: i, k: k, u: u}
			if out.Sim {
				c.ev = fam.Evals[i][k]
				if c.ev.Delay > maxDelay {
					grid[i][k] = c
					continue
				}
				c.eff = simLogMiss(c.ev, missRaw) + corr(u)
				c.tremCell = kT * c.ev.Trem
			} else {
				if k != defK {
					grid[i][k] = c
					continue
				}
				c.eff = coachNoSim(u, defOuter) + corr(u)
				c.tremCell = 0
			}
			c.obj = c.eff + feel(c.tremCell) + lagCost*c.ev.Delay
			c.ok = true
			grid[i][k] = c
			if !math.IsNaN(uPrev) && math.Abs(u-uPrev) > coachTrustStep+1e-9 {
				continue
			}
			if c.obj < best.obj {
				best = c
			}
		}
	}
	if !best.ok {
		// nothing within reach (no simulation, odd values played): the default
		best = cell{i: 0, k: defK, u: math.Log(defOuter), ok: true}
		best.eff = coachNoSim(best.u, defOuter) + corr(best.u)
		best.obj = best.eff
	}
	// between the strengths of the grid: the bottom of the curve through the
	// best one and its neighbours; how sharp the curve is there, from a
	// parabola through the cells around it
	uBest, curv := best.u, 0.6
	if i := best.i; i > 0 && i+1 < len(outers) && grid[i-1][best.k].ok && grid[i+1][best.k].ok {
		l, m, r := grid[i-1][best.k], grid[i][best.k], grid[i+1][best.k]
		h := (r.u - l.u) / 2
		den := l.obj - 2*m.obj + r.obj
		if den > 1e-9 {
			uBest = clampF(m.u+h*(l.obj-r.obj)/(2*den), l.u, r.u)
		}
	}
	{
		var xs, ys []float64
		for j := best.i - 2; j <= best.i+2; j++ {
			if j >= 0 && j < len(outers) && grid[j][best.k].ok {
				xs = append(xs, grid[j][best.k].u-best.u)
				ys = append(ys, grid[j][best.k].obj)
			}
		}
		if c2, ok := quadCoef(xs, ys); ok {
			curv = c2
		}
	}
	curv = math.Max(curv, 0.15)
	if !math.IsNaN(uPrev) {
		uBest = clampF(uBest, uPrev-coachTrustStep, uPrev+coachTrustStep)
	}
	shape := coachDefShape
	if best.k < len(shapes) {
		shape = shapes[best.k]
	}
	bp := roundParams(shape.Params(math.Exp(uBest)))
	// the best one, where it really is
	var bev filter.Eval
	effBest := best.eff
	tremBest := best.tremCell
	if out.Sim {
		bev, _ = fam.At(bp)
		effBest = simLogMiss(bev, missRaw) + corr(uBest)
		tremBest = kT * bev.Trem
		out.Delay, out.Lost, out.Gain = round2(bev.Delay), round2(bev.Lost), round2(bev.Gain)
	}
	// measured with about these values: that wobble
	var near []float64
	for _, m := range recent {
		if m.Filter == "on" && m.FParams != nil && m.CurN > 0 && math.Abs(math.Log(math.Max(m.FParams.Outer, 1e-3))-uBest) < 0.15 {
			near = append(near, m.Trem)
		}
	}
	if len(near) >= 2 {
		sort.Float64s(near)
		tremBest = near[len(near)/2]
	}
	out.Trem = round4(tremBest)
	x := uBest - ubar
	effSE := math.Sqrt(corrVar(uBest))
	objOff := feel(tremOff)
	objBest := effBest + feel(tremBest) + lagCost*bev.Delay
	out.Improve = round4(1 - math.Exp(effBest))
	out.ImpLo, out.ImpHi = round4(1-math.Exp(effBest+effSE)), round4(1-math.Exp(effBest-effSE))
	out.Best, out.BestOn = bp, true
	gap := objOff - objBest // > 0: the filter is better
	if out.Known && out.NOn >= 3 && gap < 0 && -gap > 1.3*effSE {
		// clearly better with no filter
		out.BestOn = false
		out.Note = "with the values tried so far you do better without a filter"
	}
	// how unsure the best strength is: from how unsure the slope of the
	// correction is there
	gv := V[1][1] + 4*x*x*V[2][2] + 4*x*V[1][2]
	out.PlaceSE = round4(math.Min(1, math.Sqrt(math.Max(0, gv))/(2*curv)))
	// the next map
	// a map with no filter now and then, while the recent ones are too few
	needRef := offEff < coachOffNeed
	switch {
	case in.hold:
		out.Ask, out.AskOn = out.Best, out.BestOn
	case needRef && out.NOn > 0 && in.dither%coachOffEvery == coachOffEvery-1:
		out.AskOn, out.AskRef = false, true
	case !out.BestOn:
		// no filter is best: now and then the best filter again, to see
		// whether it is still so
		if in.dither%2 == 1 {
			out.Ask, out.AskOn = out.Best, true
		}
	default:
		amp := coachTryLearn
		if out.NOn >= 4 && out.PlaceSE <= 0.25 {
			amp = coachTrySettle
		}
		// stronger, lighter, the best itself, in turn
		sg := []float64{1, -1, 0}[in.dither%3]
		lo, hi := math.Log(outers[0]), math.Log(outers[len(outers)-1])
		if out.Sim {
			// not stronger than the lag allows
			for i := len(outers) - 1; i >= 0; i-- {
				if fam.Evals[i][best.k].Delay <= maxDelay {
					hi = math.Log(outers[i])
					break
				}
			}
		}
		u := clampF(uBest+sg*amp, lo, hi)
		if sg != 0 && math.Abs(u-uBest) < amp/2 {
			u = clampF(uBest-sg*amp, lo, hi)
		}
		out.Ask, out.AskOn = roundParams(shape.Params(math.Exp(u))), true
		if sg == 0 {
			out.Ask = out.Best
		}
		out.AskTry = round4(math.Log(out.Ask.Outer / out.Best.Outer))
	}
	// how close to a perfect filter
	// how many more aim misses a filter that far from the best would give
	// (log): near the best the curve is flat, so a little doubt costs little
	sureU := 1 / (1 + curv*out.PlaceSE*out.PlaceSE/0.015)
	sureN := 1 - math.Exp(-float64(out.NOn)/6)
	sureOff := 0.5
	if out.Known {
		sureOff = normCDF(math.Abs(gap) / math.Max(effSE, 1e-6))
	}
	sure := sureU * sureN * sureOff
	if !out.BestOn {
		sure = sureN * sureOff
	}
	m := in.areaMiss
	if !(m > 0) && rateClicks > 0 {
		m = 1000 * rateMiss / rateClicks
	}
	if out.BestOn {
		m *= math.Exp(effBest)
	}
	aim := math.Exp(-m / 150)
	still := math.Exp(-tremBest / 0.03)
	tm := 1.0
	if out.BestOn {
		tm = math.Exp(-out.Lost / 20)
	} else {
		still = math.Exp(-tremOff / 0.03)
	}
	clean := math.Pow(aim, 0.6) * math.Pow(still, 0.2) * math.Pow(tm, 0.2)
	out.Sure, out.Clean, out.Pct = round1(100*sure), round1(100*clean), round1(100*sure*clean)
	out.Aim, out.Still, out.Time = round1(100*aim), round1(100*still), round1(100*tm)
	return out
}

func normCDF(z float64) float64 { return 0.5 * math.Erfc(-z/math.Sqrt2) }

// inv3 inverts a 3×3 matrix (symmetric, positive definite here).
func inv3(a [3][3]float64) [3][3]float64 {
	var r [3][3]float64
	det := a[0][0]*(a[1][1]*a[2][2]-a[1][2]*a[2][1]) - a[0][1]*(a[1][0]*a[2][2]-a[1][2]*a[2][0]) + a[0][2]*(a[1][0]*a[2][1]-a[1][1]*a[2][0])
	if math.Abs(det) < 1e-300 {
		return r
	}
	r[0][0] = (a[1][1]*a[2][2] - a[1][2]*a[2][1]) / det
	r[0][1] = (a[0][2]*a[2][1] - a[0][1]*a[2][2]) / det
	r[0][2] = (a[0][1]*a[1][2] - a[0][2]*a[1][1]) / det
	r[1][0] = (a[1][2]*a[2][0] - a[1][0]*a[2][2]) / det
	r[1][1] = (a[0][0]*a[2][2] - a[0][2]*a[2][0]) / det
	r[1][2] = (a[0][2]*a[1][0] - a[0][0]*a[1][2]) / det
	r[2][0] = (a[1][0]*a[2][1] - a[1][1]*a[2][0]) / det
	r[2][1] = (a[0][1]*a[2][0] - a[0][0]*a[2][1]) / det
	r[2][2] = (a[0][0]*a[1][1] - a[0][1]*a[1][0]) / det
	return r
}

// quadCoef is the x² coefficient of the parabola through the points (least
// squares).
func quadCoef(xs, ys []float64) (float64, bool) {
	if len(xs) < 3 {
		return 0, false
	}
	var A [3][3]float64
	var b [3]float64
	for i := range xs {
		f := [3]float64{1, xs[i], xs[i] * xs[i]}
		for r := 0; r < 3; r++ {
			b[r] += f[r] * ys[i]
			for c := 0; c < 3; c++ {
				A[r][c] += f[r] * f[c]
			}
		}
	}
	V := inv3(A)
	c2 := V[2][0]*b[0] + V[2][1]*b[1] + V[2][2]*b[2]
	return c2, !math.IsNaN(c2)
}

// roundParams rounds a setting to what is typed in OpenTabletDriver.
func roundParams(p filter.Params) filter.Params {
	r := func(v float64) float64 { return math.Round(v*100) / 100 }
	return filter.Params{Outer: r(p.Outer), Inner: r(math.Min(p.Inner, p.Outer)), Coef: r(p.Coef), Knee: r(p.Knee), Leak: r(p.Leak)}
}

func paramsTxt(p filter.Params) string {
	return fmt.Sprintf("outer %.2f, inner %.2f, coef %.2f, knee %.2f, leak %.2f", p.Outer, p.Inner, p.Coef, p.Knee, p.Leak)
}

func lagTxt(low bool) string {
	if low {
		return "latency as low as possible"
	}
	return "some latency is fine"
}

func askTxt(on bool, p filter.Params) string {
	if !on {
		return "Radial Follow off"
	}
	return "Radial Follow " + paramsTxt(p)
}

/* ---------- the steps ---------- */

// coachInput gathers what the decision needs.
func (e *Engine) coachInput(c *store.CalibSession, areaMiss, width float64) coachIn {
	in := coachIn{fam: c.FFam, maps: c.Maps, areaMiss: areaMiss, width: width}
	if fc := c.FCoach; fc != nil {
		in.hold, in.dither, in.lowLag = fc.Hold, fc.Dither, fc.LowLag
		if fc.Best.Outer > 0 {
			p := fc.Best
			in.prev = &p
		}
	}
	return in
}

// coachStart starts guiding the filter.
func (e *Engine) coachStart(c *store.CalibSession, lowLag bool) error {
	if c == nil {
		return errors.New("start the diagnosis first")
	}
	if fc := c.FCoach; fc != nil && fc.Active {
		return nil
	}
	am, aw := e.coachArea(c)
	in := e.coachInput(c, am, aw)
	// the first map with the best values, then the tries (Dither is the
	// turn of the next decision)
	in.dither = 2
	in.prev = nil
	in.lowLag = lowLag
	d := coachDecide(in)
	now := time.Now()
	fc := &store.FilterCoach{Active: true, Started: now, LowLag: lowLag, Ask: d.Ask, AskOn: d.AskOn, AskAt: now, AskTry: d.AskTry, AskRef: d.AskRef, Best: d.Best, BestOn: d.BestOn, Dither: 3}
	if old := c.FCoach; old != nil {
		fc.Hold, fc.Hist = old.Hold, old.Hist
	}
	e.st.Update(func(s *store.State) {
		if s.Calib != nil {
			s.Calib.FCoach = fc
		}
	})
	e.adaptEvent(analysis.AdaptDecision{At: now, Kind: "filter", Note: "guided filter: for the next map " + askTxt(d.AskOn, d.Ask)})
	log.Printf("diagnosis: guided filter started (%s); for the next map %s", lagTxt(lowLag), askTxt(d.AskOn, d.Ask))
	return nil
}

// coachArea is the aim misses per 1000 notes expected with the area found,
// and its width.
func (e *Engine) coachArea(c *store.CalibSession) (float64, float64) {
	ls := &e.ad
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if ls.run != nil && ls.runKey.Equal(c.Started) {
		return ls.run.Fit.Score.Miss, ls.run.Fit.Target.W
	}
	return 0, 0
}

// coachNext decides the values of the next map, after last. Callers hold
// ls.mu.
func (e *Engine) coachNext(c *store.CalibSession, last *store.AdaptMap, batch []store.AdaptMap, areaMiss, width float64) {
	fc := c.FCoach
	if fc == nil || !fc.Active {
		return
	}
	in := e.coachInput(c, areaMiss, width)
	// the maps just measured are not yet among the session's maps
	in.maps = append(append([]store.AdaptMap(nil), c.Maps...), batch...)
	d := coachDecide(in)
	now := time.Now()
	e.st.Update(func(s *store.State) {
		f := s.Calib.FCoach
		if f == nil {
			return
		}
		f.Ask, f.AskOn, f.AskAt, f.AskTry, f.AskRef = d.Ask, d.AskOn, now, d.AskTry, d.AskRef
		f.Best, f.BestOn = d.Best, d.BestOn
		f.Dither++
		f.Hist = append(f.Hist, store.FilterStep{At: now, Pct: d.Pct})
		if n := len(f.Hist); n > 200 {
			f.Hist = f.Hist[n-200:]
		}
	})
	pct := d.Pct
	last.FPct = &pct
	note := "filter for the next map: " + askTxt(d.AskOn, d.Ask)
	switch {
	case d.AskRef:
		note += " (a map without a filter, to compare)"
	case d.AskTry > 0:
		note += " (a little stronger than the best)"
	case d.AskTry < 0:
		note += " (a little lighter than the best)"
	}
	e.adaptEvent(analysis.AdaptDecision{At: now, Kind: "filter", Map: last.Title, Note: note})
	played := "filter unknown"
	switch last.Filter {
	case "off":
		played = "without a filter"
	case "on":
		if last.FParams != nil {
			played = "with " + paramsTxt(*last.FParams)
		}
	case "mixed":
		played = "filter changed during the map"
	}
	log.Printf("diagnosis: filter: %q played %s: %d aim misses (expected %.1f), shake %.1f%% of the radius, last-moment clicks %.0f%%; best %s (%+.0f%% aim misses against no filter, %s), filter perfection %.1f%% (certainty %.0f%%, clean %.0f%%); %s",
		last.Title, played, last.AimMiss, last.Exp, 100*last.Trem, 100*last.Late, askTxt(d.BestOn, d.Best), -100*d.Improve,
		map[bool]string{true: "compared with maps without a filter", false: "no map without a filter to compare with yet"}[d.Known],
		d.Pct, d.Sure, d.Clean, note)
}

// coachAction handles the buttons of the guided filter.
func (e *Engine) coachAction(c *store.CalibSession, action string) error {
	switch action {
	case "fc-start", "fc-start-low":
		return e.coachStart(c, action == "fc-start-low")
	case "fc-stop":
		e.st.Update(func(s *store.State) {
			if s.Calib != nil && s.Calib.FCoach != nil {
				s.Calib.FCoach.Active = false
			}
		})
		log.Printf("diagnosis: guided filter stopped")
	case "fc-hold", "fc-tries", "fc-lowlag", "fc-normal":
		if c.FCoach == nil {
			return errors.New("the guided filter isn't running")
		}
		lagSwitch := action == "fc-lowlag" || action == "fc-normal"
		e.st.Update(func(s *store.State) {
			if s.Calib != nil && s.Calib.FCoach != nil {
				if lagSwitch {
					s.Calib.FCoach.LowLag = action == "fc-lowlag"
				} else {
					s.Calib.FCoach.Hold = action == "fc-hold"
				}
			}
		})
		// the next values at once: this is the player at the page
		c2 := e.st.State().Calib
		am, aw := e.coachArea(c2)
		in := e.coachInput(c2, am, aw)
		if lagSwitch {
			// another rule: the best may be far from the one before
			in.prev = nil
		}
		d := coachDecide(in)
		now := time.Now()
		e.st.Update(func(s *store.State) {
			if f := s.Calib.FCoach; f != nil {
				f.Ask, f.AskOn, f.AskTry, f.AskRef = d.Ask, d.AskOn, d.AskTry, d.AskRef
				f.Best, f.BestOn = d.Best, d.BestOn
				if !filter.SameParams(f.Ask, c.FCoach.Ask) || f.AskOn != c.FCoach.AskOn {
					f.AskAt = now
				}
			}
		})
		what := map[string]string{"fc-hold": "keeping the best", "fc-tries": "trying different values", "fc-lowlag": lagTxt(true), "fc-normal": lagTxt(false)}[action]
		log.Printf("diagnosis: guided filter: %s; for the next map %s", what, askTxt(d.AskOn, d.Ask))
	case "fc-confirm":
		if c.FCoach == nil {
			return errors.New("the guided filter isn't running")
		}
		now := time.Now()
		e.st.Update(func(s *store.State) {
			if s.Calib != nil && s.Calib.FCoach != nil {
				s.Calib.FCoach.Confirmed = now
			}
		})
		log.Printf("diagnosis: filter values set (told by the page): %s", askTxt(c.FCoach.AskOn, c.FCoach.Ask))
	default:
		return errors.New("unknown action")
	}
	return nil
}

/* ---------- for the page ---------- */

// CoachStatus is the guided filter for the page.
type CoachStatus struct {
	coachOut
	Active    bool               `json:"active"`
	Hold      bool               `json:"hold"`
	Started   time.Time          `json:"started"`
	AskAt     time.Time          `json:"askAt"`
	Confirmed bool               `json:"confirmed"` // the player said the values asked are in
	Hist      []store.FilterStep `json:"hist,omitempty"`
	// what OpenTabletDriver has now: readable, on, the values (mm), the
	// Tablet coordinates version; the values asked in pixels for the screen
	// version, when that is the one there
	NowKnown  bool           `json:"nowKnown"`
	NowOn     bool           `json:"nowOn"`
	Now       filter.Params  `json:"now"`
	NowTablet bool           `json:"nowTablet"`
	AskPx     *filter.Params `json:"askPx,omitempty"`
	IsSet     bool           `json:"isSet"` // OpenTabletDriver has the values asked
}

func (e *Engine) coachStatus(c *store.CalibSession, areaMiss, width float64) *CoachStatus {
	fc := c.FCoach
	if fc == nil {
		return nil
	}
	in := e.coachInput(c, areaMiss, width)
	d := coachDecide(in)
	// what the next map is asked: as decided after the last map
	d.Ask, d.AskOn, d.AskTry, d.AskRef = fc.Ask, fc.AskOn, fc.AskTry, fc.AskRef
	out := &CoachStatus{coachOut: d, Active: fc.Active, Hold: fc.Hold, Started: fc.Started, AskAt: fc.AskAt, Hist: fc.Hist}
	out.Confirmed = !fc.Confirmed.IsZero() && fc.Confirmed.After(fc.AskAt)
	if n := len(c.FSeen); n > 0 {
		s := c.FSeen[n-1]
		out.NowKnown, out.NowOn, out.Now, out.NowTablet = true, s.On, s.Params, s.Tablet
		if s.On && !s.Tablet {
			env := e.Env()
			if a := c.Cur; a.Valid() && env.Setup.Display.W > 0 {
				k := env.Setup.Display.W / a.W
				px := fc.Ask
				px.Outer, px.Inner = math.Round(px.Outer*k*10)/10, math.Round(px.Inner*k*10)/10
				out.AskPx = &px
			}
		}
		if fc.AskOn {
			out.IsSet = s.On && filter.SameParams(s.Params, fc.Ask)
			if !s.Tablet && out.AskPx != nil {
				out.IsSet = s.On && math.Abs(s.Params.Outer-fc.Ask.Outer) < 0.02+0.02*fc.Ask.Outer && math.Abs(s.Params.Inner-fc.Ask.Inner) < 0.02+0.02*fc.Ask.Inner &&
					math.Abs(s.Params.Coef-fc.Ask.Coef) < 0.006
			}
		} else {
			out.IsSet = !s.On
		}
	}
	return out
}

/* ---------- small maths ---------- */

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// filterPrior is how many aim misses the guess "as usual" is worth.
const filterPrior = 2.0

// trigamma is the second derivative of the log gamma function (the variance
// of the log of a gamma variable).
func trigamma(x float64) float64 {
	r := 0.0
	for x < 6 {
		r += 1 / (x * x)
		x++
	}
	f := 1 / (x * x)
	return r + 1/x + f/2 + f/x*(1.0/6-f*(1.0/30-f/42))
}
