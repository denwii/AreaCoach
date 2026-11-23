// Package live follows a map while it is played: the presses read from the
// keyboard and the cursor are lined up with the circles of the map named in
// the osu! window title.
package live

import (
	"math"
	"sort"

	"areacoach/internal/aim"
	"areacoach/internal/osu"
	"areacoach/internal/otd"
)

// Press is a press seen while playing.
type Press struct {
	At   float64  // ms on the program's clock
	X, Y float64  // cursor (osu!pixels)
	Area otd.Rect // the area in use at that moment
}

// Judged is a click of the map with the area it was played with, and how
// it was reached.
type Judged struct {
	aim.Click
	Area otd.Rect
	Move aim.Move
}

// variant is one way the map may be played: one of the files with that
// title, with or without Hard Rock (which flips the map and shrinks the
// circles; the other mods change nothing a press can see, but the speed).
type variant struct {
	objs []osu.HitObject
	d    osu.Difficulty
	hr   bool
}

type alignment struct {
	v    int
	t0   float64 // program clock at map time 0
	rate float64
	hits int
}

// Session is one map being played.
type Session struct {
	Title    string
	lazer    bool
	vars     []variant
	presses  []Press
	al       *alignment
	segStart int      // first press of the current alignment
	done     []Judged // clicks of earlier alignments (before a pause or a retry)
	lastTry  float64
}

// Status describes the session.
type Status struct {
	Title  string  `json:"title"`
	Synced bool    `json:"synced"`
	Rate   float64 `json:"rate"`
	HR     bool    `json:"hr"`
	Clicks int     `json:"clicks"`
	Hits   int     `json:"hits"`
}

// NewSession prepares the maps that have the title (osu!standard ones).
func NewSession(title string, maps []*osu.Beatmap, lazer bool) *Session {
	s := &Session{Title: title, lazer: lazer}
	for _, bm := range maps {
		if bm == nil || bm.Mode != 0 {
			continue
		}
		for _, mods := range []uint32{0, osu.ModHardRock} {
			objs, d := bm.Objects(mods)
			var heads []osu.HitObject
			for _, o := range objs {
				if o.Kind != osu.KindSpinner {
					heads = append(heads, o)
				}
			}
			if len(heads) > 0 {
				s.vars = append(s.vars, variant{objs: heads, d: d, hr: mods != 0})
			}
		}
	}
	return s
}

// Playable reports whether a map was found for the title.
func (s *Session) Playable() bool { return len(s.vars) > 0 }

// Add records a press (in time order).
func (s *Session) Add(p Press) { s.presses = append(s.presses, p) }

// the speeds osu! stable can play at; osu!lazer can play at any
var stableRates = []float64{1, 1.5, 0.75}

func lazerRates() []float64 {
	out := append([]float64{}, stableRates...)
	for r := 0.5; r <= 2.0001; r += 0.05 {
		if math.Abs(r-1) > 1e-6 && math.Abs(r-1.5) > 1e-6 && math.Abs(r-0.75) > 1e-6 {
			out = append(out, math.Round(r*100)/100)
		}
	}
	return out
}

// presses needed to line up a map
const (
	alignMin   = 10
	alignHits  = 8
	alignShare = 0.45
)

func (v *variant) mapTime(p Press, a *alignment) float64 { return (p.At - a.t0) * a.rate }

// judge judges presses with an alignment.
func (s *Session) judge(ps []Press, a *alignment) ([]aim.Click, []int) {
	v := &s.vars[a.v]
	in := make([]aim.Press, len(ps))
	for i, p := range ps {
		in[i] = aim.Press{T: v.mapTime(p, a), X: p.X, Y: p.Y}
	}
	cs := aim.Judge(v.objs, v.d, in)
	// which press each click used
	at := make([]int, len(cs))
	for i := range cs {
		at[i] = -1
		if !cs[i].Pressed {
			continue
		}
		k := sort.Search(len(in), func(k int) bool { return in[k].T >= cs[i].PressT })
		if k < len(in) && in[k].T == cs[i].PressT {
			at[i] = k
		}
	}
	return cs, at
}

// find lines the presses up with one of the variants: each press on a
// circle says when the map would have started; the start most presses agree
// on wins, if enough of them hit.
func (s *Session) find(ps []Press, rates []float64) *alignment {
	if len(ps) < alignMin {
		return nil
	}
	var best *alignment
	for vi := range s.vars {
		v := &s.vars[vi]
		for _, rate := range rates {
			const bin = 10.0
			votes := map[int]int{}
			sums := map[int]float64{}
			for _, p := range ps {
				for k := range v.objs {
					o := &v.objs[k]
					pos := o.StackedPos(v.d.StackOffset)
					if math.Hypot(p.X-pos.X, p.Y-pos.Y) > v.d.Radius {
						continue
					}
					t0 := p.At - o.Time/rate
					b := int(math.Floor(t0 / bin))
					votes[b]++
					sums[b] += t0
				}
			}
			topB, top := 0, 0
			for b := range votes {
				n := 0
				for k := b - 3; k <= b+3; k++ {
					n += votes[k]
				}
				if n > top || n == top && b < topB {
					topB, top = b, n
				}
			}
			if top < alignHits {
				continue
			}
			sum, n := 0.0, 0
			for k := topB - 3; k <= topB+3; k++ {
				sum += sums[k]
				n += votes[k]
			}
			a := &alignment{v: vi, t0: sum / float64(n), rate: rate}
			cs, _ := s.judge(ps, a)
			pressed := 0
			for _, c := range cs {
				if c.Pressed {
					pressed++
					if c.Hit {
						a.hits++
					}
				}
			}
			if a.hits < alignHits || float64(a.hits) < alignShare*float64(pressed) {
				continue
			}
			if best == nil || a.hits > best.hits {
				best = a
			}
		}
	}
	if best != nil {
		s.refine(ps, best)
	}
	return best
}

// refine sets the start (and, for osu!lazer, the speed) from the hits.
func (s *Session) refine(ps []Press, a *alignment) {
	cs, at := s.judge(ps, a)
	var xs, ys []float64
	for i, c := range cs {
		if c.Hit && at[i] >= 0 {
			xs = append(xs, c.T)
			ys = append(ys, ps[at[i]].At)
		}
	}
	if len(xs) < alignHits {
		return
	}
	if s.lazer && len(xs) >= 20 && xs[len(xs)-1]-xs[0] > 5000 {
		// at = t0 + T / rate: a line through the hits
		var mx, my float64
		for i := range xs {
			mx += xs[i]
			my += ys[i]
		}
		mx /= float64(len(xs))
		my /= float64(len(xs))
		var sxy, sxx float64
		for i := range xs {
			sxy += (xs[i] - mx) * (ys[i] - my)
			sxx += (xs[i] - mx) * (xs[i] - mx)
		}
		if sxx > 0 && sxy > 0 {
			r := sxx / sxy
			for _, std := range stableRates {
				if math.Abs(r/std-1) < 0.005 {
					r = std
				}
			}
			if math.Abs(r/a.rate-1) < 0.03 {
				a.rate = r
			}
		}
	}
	d := make([]float64, len(xs))
	for i := range xs {
		d[i] = ys[i] - xs[i]/a.rate
	}
	sort.Float64s(d)
	a.t0 = d[len(d)/2]
}

// Update lines the presses up when needed: at the start, and again when
// they stop matching (a pause, a retry). now is the program clock.
func (s *Session) Update(now float64) {
	if !s.Playable() {
		return
	}
	staleFrom := len(s.presses)
	if s.al != nil {
		bad, at := s.stale(now)
		if !bad {
			return
		}
		staleFrom = at
	}
	if now-s.lastTry < 1500 {
		return
	}
	s.lastTry = now
	// the presses of the last 12 seconds
	from := len(s.presses)
	for from > s.segStart && now-s.presses[from-1].At < 12000 {
		from--
	}
	recent := s.presses[from:]
	if len(recent) < alignMin {
		return
	}
	rates := stableRates
	a := s.find(recent, rates)
	if a == nil && s.lazer {
		a = s.find(recent, lazerRates())
	}
	if a == nil {
		return
	}
	if s.al != nil && a.v == s.al.v && a.rate == s.al.rate && math.Abs(a.t0-s.al.t0) < 40 {
		return // the same as before: the presses are still being sorted out
	}
	// the new alignment starts at its first hit
	cs, at := s.judge(recent, a)
	start := from
	for i, c := range cs {
		if c.Hit && at[i] >= 0 {
			start = from + at[i]
			break
		}
	}
	if s.al != nil {
		// what was played before, without the presses that no longer matched
		s.done = append(s.done, s.final(math.Inf(1), min(start, staleFrom))...)
	}
	s.al, s.segStart = a, start
}

// stale: the last presses no longer hit the circles of the alignment; at
// is the first of those presses.
func (s *Session) stale(now float64) (bool, int) {
	ps := s.presses[s.segStart:]
	var recent []Press
	at := len(s.presses)
	for i := len(ps) - 1; i >= 0 && len(recent) < 12; i-- {
		if now-ps[i].At > 10000 {
			break
		}
		recent = append(recent, ps[i])
		at = s.segStart + i
	}
	if len(recent) < 12 {
		return false, at
	}
	for i, j := 0, len(recent)-1; i < j; i, j = i+1, j-1 {
		recent[i], recent[j] = recent[j], recent[i]
	}
	cs, _ := s.judge(recent, s.al)
	hits := 0
	for _, c := range cs {
		if c.Hit {
			hits++
		}
	}
	return hits < 3, at
}

// final are the clicks of the current alignment whose hit window is over
// by now (before: the program clock; +Inf for all), from its first hit on,
// pressed by presses before the press number upto.
func (s *Session) final(before float64, upto int) []Judged {
	if s.al == nil || upto <= s.segStart {
		return nil
	}
	ps := s.presses[s.segStart:min(upto, len(s.presses))]
	v := &s.vars[s.al.v]
	cs, at := s.judge(ps, s.al)
	limit := math.Inf(1)
	if !math.IsInf(before, 1) {
		limit = v.mapTime(Press{At: before}, s.al) - v.d.Window50 - 50
	}
	first := math.Inf(1)
	for _, c := range cs {
		if c.Hit {
			first = c.T
			break
		}
	}
	moves := aim.Moves(v.objs, v.d, s.al.rate, cs)
	var out []Judged
	for i, c := range cs {
		if c.T < first || c.T > limit || !c.Pressed || at[i] < 0 {
			continue
		}
		out = append(out, Judged{Click: c, Area: ps[at[i]].Area, Move: moves[i]})
	}
	return out
}

// Clicks are all the clicks played so far whose result is settled.
func (s *Session) Clicks(now float64) []Judged {
	out := append([]Judged{}, s.done...)
	return append(out, s.final(now, len(s.presses))...)
}

// Status describes the session now.
func (s *Session) Status(now float64) Status {
	st := Status{Title: s.Title}
	if s.al != nil {
		bad, _ := s.stale(now)
		st.Synced = !bad
		st.Rate, st.HR = s.al.rate, s.vars[s.al.v].hr
	}
	for _, c := range s.Clicks(now) {
		st.Clicks++
		if c.Hit {
			st.Hits++
		}
	}
	return st
}
