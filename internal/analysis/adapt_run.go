package analysis

import (
	"math"
	"time"

	"areacoach/internal/otd"
)

// AdaptRun is the logic of the diagnosis map by map, apart from talking to
// the driver: every map is played with one area; when its replay comes, what
// it measured is added and the area of the next map decided.
type AdaptRun struct {
	Opts AdaptOpts
	Cur  otd.Rect // the area set now
	Obs  []CalibPoint
	Fit  AdaptFit
	// a map with no aim miss leaves the area as it is
	HoldClean bool
	// maps played a little wider or narrower than the best width so far
	// (in turn, by how unsure it still is)
	Dither int
	// the side the last decision could not try, and why: "min" / "max" (the
	// widths the player allows, Limit mm) or "reach" (wider the hand could
	// not play the maps at their speed); "" when nothing was in the way
	Blocked string
	Limit   float64
	// how far the last decision moved the place from the best one, to see
	// where the hand is steadier (mm, right and down)
	PlaceTry [2]float64
}

// AdaptDecision is a map measured and the area it led to.
type AdaptDecision struct {
	At   time.Time `json:"at"`
	Kind string    `json:"kind"` // map: after a map; filter: the guided filter; otd: OpenTabletDriver
	Map  string    `json:"map,omitempty"`
	From otd.Rect  `json:"from"`
	To   otd.Rect  `json:"to"`
	Held bool      `json:"held,omitempty"` // no aim miss: the area was left as it was
	// the next map is played this much wider (+) or narrower than the best
	// width so far (log), to keep learning
	Try  float64 `json:"try,omitempty"`
	Note string  `json:"note,omitempty"`
}

// AimQuality counts the clicked circles, the aim misses and the clicks at
// the edge (beyond 80% of the radius). An aim miss is a circle or slider
// head missed after pressing outside it: not the misses of notelock nor
// those with no press at all.
func AimQuality(hs []Hit) (clicks, miss, edge int) {
	for i := range hs {
		h := &hs[i]
		if h.Kind > 1 || !h.HasClick() || h.Radius <= 0 {
			continue
		}
		clicks++
		d := math.Hypot(float64(h.CX-h.TX), float64(h.CY-h.TY)) / float64(h.Radius)
		switch {
		case aimMissed(h):
			miss++
		case h.Result != RAimMiss && d > 0.8:
			edge++
		}
	}
	return
}

// aimMissed tells whether a judged circle is an aim miss: a circle or the
// head of a slider (missing it breaks the combo too) missed after pressing
// outside it. The replay has the cursor exactly where osu! judged it.
func aimMissed(h *Hit) bool {
	if h.Kind > 1 || h.Result != RAimMiss || !h.HasClick() || h.Radius <= 0 {
		return false
	}
	return math.Hypot(float64(h.CX-h.TX), float64(h.CY-h.TY))/float64(h.Radius) > 1
}

// NewAdaptRun starts or continues a diagnosis with the area cur set.
func NewAdaptRun(o AdaptOpts, cur otd.Rect, obs []CalibPoint) *AdaptRun {
	r := &AdaptRun{Opts: o, Cur: cur, Obs: obs}
	r.refit()
	return r
}

func (r *AdaptRun) refit() {
	o := r.Opts
	o.Keep = r.Keep()
	o.Cur = r.Cur
	if t := r.Fit.Target; t.Valid() {
		o.KeepPlace, o.KeepX, o.KeepY = true, t.X, t.Y
	}
	r.Fit = FitAdapt(r.Obs, o, false)
}

// Keep is the width found so far, which stays among widths practically as
// good (not the width of a map played with another one on purpose).
func (r *AdaptRun) Keep() float64 {
	if r.Fit.Target.Valid() {
		return r.Fit.Target.W
	}
	return 0
}

// SetOpts changes the rules of the run (such as keeping the place still).
func (r *AdaptRun) SetOpts(o AdaptOpts) {
	r.Opts = o
	r.refit()
}

// Set records the area set now.
func (r *AdaptRun) Set(a otd.Rect) { r.Cur = a }

// AddMap adds what a map measured.
func (r *AdaptRun) AddMap(ws []CalibPoint) {
	for _, w := range ws {
		if w.SG <= 0 {
			continue
		}
		w.Index = len(r.Obs)
		r.Obs = append(r.Obs, w)
	}
	r.refit()
}

// Rest is the best area found so far.
func (r *AdaptRun) Rest() otd.Rect { return AdaptNext(r.Cur, r.Fit.Target, r.Opts) }

// Every map tries a width a little different from the best one so far:
// wider and narrower in turn, by how unsure the best one still is (share),
// within these bounds. All the maps go into the next decision, so the
// differences shrink as the diagnosis gets surer, without ever stopping.
// Until it is known how the steadiness of the hand changes with the width
// (its trend known within AdaptTrySE), at least AdaptTryLearn: smaller
// differences hardly show it.
const (
	AdaptTryMin   = 0.02
	AdaptTryMax   = 0.08
	AdaptTrySE    = 0.4
	AdaptTryLearn = 0.08
)

// Next decides the area of the next map, after one ended: clean tells
// whether it had no aim miss. It returns the area and how much wider (+)
// or narrower than the best width so far it is (log).
func (r *AdaptRun) Next(clean bool) (otd.Rect, float64) {
	r.Blocked, r.Limit, r.PlaceTry = "", 0, [2]float64{}
	if r.HoldClean && clean {
		return r.Cur, 0
	}
	t := r.Rest()
	if r.Opts.FixSize {
		p := r.placeTry(t)
		r.Dither++
		return p, 0
	}
	amp := clamp(r.Fit.Score.Spread, AdaptTryMin, AdaptTryMax)
	if p := r.Fit.Prec; r.Opts.Miss.OK() && (p.N < 8 || p.SE > AdaptTrySE) {
		amp = math.Max(amp, AdaptTryLearn)
	}
	sg := 1.0
	if r.Dither%2 == 1 {
		sg = -1
	}
	lo, hi := AdaptRange(r.Opts)
	hiWhy := "max"
	if lim := r.Fit.Reach; lim > 0 && lim < hi {
		// never wider than the hand can play the maps at their speed
		hi, hiWhy = math.Max(lim, lo), "reach"
	}
	want := t.W * math.Exp(sg*amp)
	w := clamp(want, lo, hi)
	if math.Abs(math.Log(w/t.W)) < amp/2 {
		// that side is closed by a limit (the widths allowed, or the speed
		// the hand can play the maps at): this time the best width itself,
		// the next one the other side
		w = t.W
		if want < lo {
			r.Blocked, r.Limit = "min", lo
		} else {
			r.Blocked, r.Limit = hiWhy, hi
		}
	}
	k := w / t.W
	p := AdaptShape(otd.Rect{W: t.W * k, H: t.H * k, X: t.X, Y: t.Y, Rot: t.Rot}, r.Opts)
	p = r.placeTry(p)
	r.Dither++
	return p, round(math.Log(p.W/t.W), 4)
}

// The place is tried a little off the best one too, to see where on the
// tablet the hand is steadier: right, left, down, up in turn, two maps each
// (so that each side is played both wider and narrower), by AdaptPlaceTry
// mm while it is not known, then by AdaptPlaceTrySure.
const (
	AdaptPlaceTry     = 2.0
	AdaptPlaceTrySure = 1.0
	// known well enough: the steadiness by place within this (log variance
	// per mm)
	adaptPlaceKnown = 0.012
)

// placeTry moves an area a little off the best place, in turn.
func (r *AdaptRun) placeTry(a otd.Rect) otd.Rect {
	if r.Opts.FixPos {
		return a
	}
	pc := r.Fit.Prec
	phase := (r.Dither / 2) % 4
	se := pc.GXSE
	if phase >= 2 {
		se = pc.GYSE
	}
	amp := AdaptPlaceTry
	if pc.N >= 8 && se > 0 && se <= adaptPlaceKnown {
		amp = AdaptPlaceTrySure
	}
	d := [4][2]float64{{amp, 0}, {-amp, 0}, {0, amp}, {0, -amp}}[phase]
	p := AdaptShape(otd.Rect{W: a.W, H: a.H, X: a.X + d[0], Y: a.Y + d[1], Rot: a.Rot}, r.Opts)
	r.PlaceTry = [2]float64{round(p.X-a.X, 2), round(p.Y-a.Y, 2)}
	return p
}

// AreaReaches tells whether the areas set reach the tablet: across the
// stretches measured since from, the over/undershoot has to change when the
// size changes a lot (right after a change the hand still moves as before).
// known is false until the size has changed enough to tell.
func AreaReaches(obs []CalibPoint, from time.Time) (known, reaches bool) {
	var sw, sx, sy, sxx, sxy float64
	lo, hi := math.Inf(1), math.Inf(-1)
	n := 0
	for _, p := range obs {
		if p.Src == "play" || p.SG <= 0 || p.SG > 0.15 || !p.Area.Valid() || p.At.Before(from) {
			continue
		}
		x, w := math.Log(p.Area.W), 1/(p.SG*p.SG)
		sw += w
		sx += w * x
		sy += w * p.G
		sxx += w * x * x
		sxy += w * x * p.G
		lo, hi = math.Min(lo, x), math.Max(hi, x)
		n++
	}
	if n < 6 || hi-lo < 0.6 {
		return false, true
	}
	den := sw*sxx - sx*sx
	if den <= 0 {
		return false, true
	}
	slope := (sw*sxy - sx*sy) / den
	se := math.Sqrt(sw / den)
	// with the smallest areas the jumps are still far off: what a smaller
	// area should have fixed (jumps a little short whatever the area are a
	// habit of the hand, not a sign of anything wrong)
	var bw, bg float64
	cut := lo + (hi-lo)/3
	for _, p := range obs {
		if p.Src == "play" || p.SG <= 0 || p.SG > 0.15 || !p.Area.Valid() || p.At.Before(from) || math.Log(p.Area.W) > cut {
			continue
		}
		w := 1 / (p.SG * p.SG)
		bw += w
		bg += w * p.G
	}
	if bw == 0 || math.Abs(bg/bw) < 0.08 {
		return true, true
	}
	// a smaller area makes the same movement of the pen longer on screen:
	// the measures must lean that way, clearly
	if slope > -0.05 && slope-2*se > -0.25 {
		return true, false
	}
	return true, true
}
