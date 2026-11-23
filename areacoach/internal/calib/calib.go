// Package calib remembers how you play at each width of the area, across
// days, and from that finds the band of widths that suit you: where you
// land neither short nor long, and where you miss the fewest notes,
// compared on notes alike. The live calibration moves freely inside the
// band; its edges are elastic.
package calib

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"areacoach/internal/aim"
)

// Click is one press, as the memory keeps it.
type Click struct {
	Width  float64 // width of the area it was played with (mm)
	Dist   float64 // distance of the press from the centre (radii)
	ErrX   float64 // press - centre (osu!pixels)
	ErrY   float64
	Radius float64 // osu!pixels
	MoveX  float64 // the movement to the note (osu!pixels); 0 when none
	MoveY  float64
	Gap    float64 // real ms for the movement; 0 when none
}

// FromAim makes a Click from a judged click, its move and the width used.
func FromAim(c aim.Click, m aim.Move, width float64) (Click, bool) {
	if !c.Pressed || c.Radius <= 0 || width <= 0 {
		return Click{}, false
	}
	out := Click{Width: width, Dist: c.Dist(), ErrX: c.Cursor.X - c.Target.X, ErrY: c.Cursor.Y - c.Target.Y, Radius: c.Radius}
	if m.OK {
		out.MoveX, out.MoveY, out.Gap = c.Target.X-m.From.X, c.Target.Y-m.From.Y, m.Gap
	}
	return out, true
}

// The numbers of the memory.
const (
	binMM     = 0.5  // widths are kept in steps of half a millimetre
	halfLife  = 10.0 // days after which a day counts half
	keepDays  = 60
	softTau   = 0.2 // how softly a miss is counted (see soft)
	classes   = 17
	jumpMin   = 2.0 // a jump, for long and short: at least two radii
	validN    = 60  // clicks around a width it needs to be judged
	wallN     = 150 // and to stop the area
	smoothMM  = 2.5 // widths this close help judge each other
	minPlays  = 2.5 // plays around a width it needs to be judged (one map can be just easy)
	bandFirst = 300 // clicks before there is a band
)

// soft counts a press as a miss softly: fully far out, half on the edge,
// a little when close to it; so that every press says something, not only
// the misses (they are few, and would need thousands of clicks).
func soft(x float64) float64 { return 1 / (1 + math.Exp(-(x-1)/softTau)) }

// class sorts the notes alike: by how far the jump is (in radii) and how
// much time there is for it. Misses are compared within a class.
func class(c Click) int {
	if c.Gap <= 0 || c.Radius <= 0 {
		return classes - 1
	}
	j := math.Hypot(c.MoveX, c.MoveY) / c.Radius
	jb := 3
	switch {
	case j < 1.5:
		jb = 0
	case j < 4:
		jb = 1
	case j < 8:
		jb = 2
	}
	gb := 3
	switch {
	case c.Gap < 120:
		gb = 0
	case c.Gap < 200:
		gb = 1
	case c.Gap < 350:
		gb = 2
	}
	return jb*4 + gb
}

// agg are the sums of the clicks of one width, one class, one day.
type agg struct {
	N    float64 `json:"n"`
	Q    float64 `json:"q"`  // soft misses
	Q2   float64 `json:"q2"` // their squares
	Miss float64 `json:"m"`  // misses
	KN   float64 `json:"kn"` // jumps
	EL   float64 `json:"el"` // Σ error along the jump × its length
	E2   float64 `json:"e2"` // Σ error along the jump squared
	L2   float64 `json:"l2"` // Σ length squared
}

func (g *agg) add(c Click) {
	q := soft(c.Dist)
	g.N++
	g.Q += q
	g.Q2 += q * q
	if c.Dist > 1 {
		g.Miss++
	}
	if l := math.Hypot(c.MoveX, c.MoveY); c.Gap > 0 && l >= jumpMin*c.Radius && c.Dist < 2.5 {
		e := (c.ErrX*c.MoveX + c.ErrY*c.MoveY) / l
		g.KN++
		g.EL += e * l
		g.E2 += e * e
		g.L2 += l * l
	}
}

// plays counts the plays (maps) that had clicks at a width.
type plays struct {
	N    float64 `json:"n"`
	Last string  `json:"l"` // the last one, not to count it twice
}

// memory is what is kept for one tablet and one shape of area.
type memory struct {
	Days  map[string]map[string]*agg   `json:"days"`  // day -> "bin:class" -> sums
	Plays map[string]map[string]*plays `json:"plays"` // day -> "bin" -> plays
	Seen  []string                     `json:"seen,omitempty"`
}

// Store keeps the memories (one per tablet and shape) in a file.
type Store struct {
	path  string
	mu    sync.Mutex
	mem   map[string]*memory
	dirty bool
	ver   int
}

// Key names the memory of a tablet and a shape of area (height / width).
func Key(tablet string, ratio float64) string {
	return fmt.Sprintf("%s|%.2f", tablet, ratio)
}

// Open reads the memories from path (none if it does not exist).
func Open(path string) *Store {
	s := &Store{path: path, mem: map[string]*memory{}}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &s.mem)
	}
	if s.mem == nil {
		s.mem = map[string]*memory{}
	}
	return s
}

// Save writes the memories if they changed.
func (s *Store) Save() error {
	s.mu.Lock()
	if !s.dirty {
		s.mu.Unlock()
		return nil
	}
	b, err := json.Marshal(s.mem)
	s.dirty = false
	s.mu.Unlock()
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Version changes every time something is added or forgotten.
func (s *Store) Version() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ver
}

func dayOf(t time.Time) string { return t.Format("2006-01-02") }

// Add remembers clicks played at time t during the play src (a map played
// once: the same name for all its clicks, another for the next play).
func (s *Store) Add(key string, t time.Time, src string, cs []Click) {
	if len(cs) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.mem[key]
	if m == nil {
		m = &memory{}
		s.mem[key] = m
	}
	if m.Days == nil {
		m.Days = map[string]map[string]*agg{}
	}
	if m.Plays == nil {
		m.Plays = map[string]map[string]*plays{}
	}
	day := m.Days[dayOf(t)]
	if day == nil {
		day = map[string]*agg{}
		m.Days[dayOf(t)] = day
	}
	pd := m.Plays[dayOf(t)]
	if pd == nil {
		pd = map[string]*plays{}
		m.Plays[dayOf(t)] = pd
	}
	for _, c := range cs {
		if c.Width <= 0 || c.Radius <= 0 || math.IsNaN(c.Dist) || c.Dist > 3 {
			continue // a press this far wasn't aimed at that circle
		}
		bk := strconv.Itoa(int(math.Floor(c.Width / binMM)))
		if p := pd[bk]; p == nil {
			pd[bk] = &plays{N: 1, Last: src}
		} else if p.Last != src {
			p.N++
			p.Last = src
		}
		k := bk + ":" + strconv.Itoa(class(c))
		g := day[k]
		if g == nil {
			g = &agg{}
			day[k] = g
		}
		g.add(c)
	}
	// days too old to count any more
	for d := range m.Days {
		if dt, err := time.Parse("2006-01-02", d); err == nil && t.Sub(dt) > keepDays*24*time.Hour {
			delete(m.Days, d)
			delete(m.Plays, d)
		}
	}
	s.dirty = true
	s.ver++
}

// Seen tells whether a replay was already counted, and notes it.
func (s *Store) Seen(key, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.mem[key]
	if m == nil {
		m = &memory{}
		s.mem[key] = m
	}
	for _, x := range m.Seen {
		if x == id {
			return true
		}
	}
	m.Seen = append(m.Seen, id)
	if len(m.Seen) > 300 {
		m.Seen = m.Seen[len(m.Seen)-300:]
	}
	s.dirty = true
	return false
}

// Forget drops the memory of a tablet and shape.
func (s *Store) Forget(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.mem, key)
	s.dirty = true
	s.ver++
}

// Width is what the memory says of one width (a millimetre wide).
type Width struct {
	W      float64 `json:"w"`      // from W to W+1 mm
	Clicks float64 `json:"clicks"` // counted (old days count less)
	Plays  float64 `json:"plays"`  // maps played at this width
	Miss   float64 `json:"miss"`   // misses compared with your average on notes alike (1 = average), from this width and its neighbours
	MissSE float64 `json:"missSE"`
	Own    float64 `json:"own"`  // from this width alone
	Raw    float64 `json:"raw"`  // share of the presses outside the circle
	Land   float64 `json:"land"` // along the jumps: + long, - short (share of the jump)
	LandSE float64 `json:"landSE"`
	Jumps  float64 `json:"jumps"`
	Band   bool    `json:"band"` // in the band
	Worse  bool    `json:"worse"`
}

// Report is what the memory says.
type Report struct {
	Widths  []Width `json:"widths"`
	Clicks  float64 `json:"clicks"`
	Days    int     `json:"days"`
	HasBand bool    `json:"hasBand"`
	BandLo  float64 `json:"bandLo"`
	BandHi  float64 `json:"bandHi"`
	Best    float64 `json:"best"` // the width that goes best (centre of its millimetre)
	LoWall  bool    `json:"loWall"`
	HiWall  bool    `json:"hiWall"`
	// where you go from landing long (below) to landing short (above)
	HasCross bool    `json:"hasCross"`
	Cross    float64 `json:"cross"`
	CrossRel float64 `json:"crossRel"` // how much it is trusted (0-0.7)
	// you land short (or long) by about the same share at every width:
	// it follows you, it is how you aim
	Habit   bool    `json:"habit"`
	HabitK  float64 `json:"habitK"`
	slope   float64 // misses against width (per mm), and its uncertainty
	slopeSE float64
}

// Analyze reads the memory of key as of now.
func (s *Store) Analyze(key string, now time.Time) Report {
	s.mu.Lock()
	defer s.mu.Unlock()
	var r Report
	m := s.mem[key]
	if m == nil {
		return r
	}
	type cell struct {
		bin, cls int
		w        float64
		g        *agg
	}
	var cells []cell
	days := 0
	today, _ := time.Parse("2006-01-02", dayOf(now))
	weight := func(d string) (float64, bool) {
		dt, err := time.Parse("2006-01-02", d)
		if err != nil {
			return 0, false
		}
		age := math.Round(today.Sub(dt).Hours() / 24) // whole days: today counts fully
		return math.Pow(0.5, math.Max(0, age)/halfLife), true
	}
	playsAt := map[int]float64{}
	for d, pd := range m.Plays {
		w, ok := weight(d)
		if !ok {
			continue
		}
		for k, p := range pd {
			if bin, err := strconv.Atoi(k); err == nil {
				playsAt[int(math.Floor(float64(bin)*binMM))] += w * p.N
			}
		}
	}
	for d, day := range m.Days {
		w, ok := weight(d)
		if !ok {
			continue
		}
		if len(day) > 0 {
			days++
		}
		for k, g := range day {
			a, b, ok := strings.Cut(k, ":")
			if !ok {
				continue
			}
			bin, e1 := strconv.Atoi(a)
			cls, e2 := strconv.Atoi(b)
			if e1 != nil || e2 != nil || cls < 0 || cls >= classes {
				continue
			}
			cells = append(cells, cell{bin, cls, w, g})
		}
	}
	r.Days = days
	// your average on each kind of note, at every width
	var cn, cq [classes]float64
	var tn, tq float64
	for _, c := range cells {
		cn[c.cls] += c.w * c.g.N
		cq[c.cls] += c.w * c.g.Q
		tn += c.w * c.g.N
		tq += c.w * c.g.Q
	}
	if tn == 0 {
		return r
	}
	r.Clicks = tn
	base := func(cls int) float64 {
		if cn[cls] < 20 {
			return math.Max(tq/tn, 1e-3)
		}
		return math.Max(cq[cls]/cn[cls], 1e-3)
	}
	// each millimetre of width
	type acc struct {
		n, q, exp, vq, miss  float64
		kn, el, e2, l2       float64
		hasWeight, weightSum float64
	}
	byMM := map[int]*acc{}
	for _, c := range cells {
		mm := int(math.Floor(float64(c.bin) * binMM))
		a := byMM[mm]
		if a == nil {
			a = &acc{}
			byMM[mm] = a
		}
		g, w := c.g, c.w
		a.n += w * g.N
		a.q += w * g.Q
		a.exp += w * g.N * base(c.cls)
		if g.N > 0 {
			a.vq += w * w * math.Max(0, g.Q2-g.Q*g.Q/g.N)
		}
		a.miss += w * g.Miss
		a.kn += w * g.KN
		a.el += w * g.EL
		a.e2 += w * g.E2
		a.l2 += w * g.L2
	}
	var mms []int
	for mm := range byMM {
		mms = append(mms, mm)
	}
	sort.Ints(mms)
	for _, mm := range mms {
		a := byMM[mm]
		wd := Width{W: float64(mm), Clicks: a.n, Jumps: a.kn, Plays: playsAt[mm]}
		if a.exp > 0 {
			wd.Own = a.q / a.exp
			wd.MissSE = math.Sqrt(a.vq) / a.exp
		}
		if a.n > 0 {
			wd.Raw = a.miss / a.n
		}
		if a.l2 > 0 {
			wd.Land = a.el / a.l2
			s2 := math.Max(0, a.e2-wd.Land*a.el) / math.Max(1, a.kn-1)
			wd.LandSE = math.Sqrt(s2 / a.l2)
		}
		r.Widths = append(r.Widths, wd)
	}
	r.band()
	r.landing()
	return r
}

// smooth judges each width with its neighbours too: a straight line through
// the widths around it (the closer, the more they count), so that a few
// dozen clicks per millimetre are enough to see the misses grow.
func (r *Report) smooth() {
	type pt struct{ x, y, v float64 }
	var pts []pt
	for _, w := range r.Widths {
		if w.Clicks >= 15 && w.MissSE > 0 {
			pts = append(pts, pt{w.W + 0.5, w.Own, w.MissSE * w.MissSE})
		}
	}
	for i := range r.Widths {
		w := &r.Widths[i]
		w.Miss, w.MissSE = w.Own, 0
		if w.Clicks < 15 {
			continue
		}
		x0 := w.W + 0.5
		var s0, s1, s2 float64
		var us []float64
		for _, p := range pts {
			d := (p.x - x0) / smoothMM
			k := math.Max(0, 1-d*d)
			u := k / p.v
			us = append(us, u)
			s0 += u
			s1 += u * (p.x - x0)
			s2 += u * (p.x - x0) * (p.x - x0)
		}
		det := s0*s2 - s1*s1
		var est, vr float64
		if det > 1e-9*s0*s0 {
			// local line: the weight of each point in the value at x0
			for j, p := range pts {
				l := us[j] * (s2 - s1*(p.x-x0)) / det
				est += l * p.y
				vr += l * l * p.v
			}
		} else if s0 > 0 {
			for j, p := range pts {
				l := us[j] / s0
				est += l * p.y
				vr += l * l * p.v
			}
		} else {
			continue
		}
		w.Miss, w.MissSE = math.Max(0, est), math.Sqrt(vr)
	}
	// a width with plenty of clicks of its own needs no neighbours (they
	// would blur where the misses start to grow)
	for i := range r.Widths {
		w := &r.Widths[i]
		if w.Clicks >= 250 && w.Own > 0 {
			var own float64
			for _, p := range pts {
				if p.x == w.W+0.5 {
					own = math.Sqrt(p.v)
				}
			}
			if own > 0 {
				w.Miss, w.MissSE = w.Own, own
			}
		}
	}
}

// plays is how many plays there are within d mm of a width.
func (r *Report) plays(i int, d float64) float64 {
	n := 0.0
	for _, w := range r.Widths {
		if math.Abs(w.W-r.Widths[i].W) <= d {
			n += w.Plays
		}
	}
	return n
}

// support is how many clicks there are within d mm of a width.
func (r *Report) support(i int, d float64) float64 {
	n := 0.0
	for _, w := range r.Widths {
		if math.Abs(w.W-r.Widths[i].W) <= d {
			n += w.Clicks
		}
	}
	return n
}

// band finds the widths that go about as well as the best one, next to it.
func (r *Report) band() {
	r.smooth()
	judged := func(i int) bool {
		w := r.Widths[i]
		return w.Clicks >= 15 && w.MissSE > 0 && r.support(i, 1) >= validN && r.plays(i, 1) >= minPlays
	}
	var valid []int
	for i := range r.Widths {
		if judged(i) {
			valid = append(valid, i)
		}
	}
	// misses against width: which way it gets worse
	if len(valid) >= 2 {
		var sw, sx, sy, sxx, sxy float64
		for _, i := range valid {
			w := r.Widths[i]
			wt := 1 / (w.MissSE * w.MissSE)
			x := w.W + 0.5
			sw += wt
			sx += wt * x
			sy += wt * w.Miss
			sxx += wt * x * x
			sxy += wt * x * w.Miss
		}
		if d := sw*sxx - sx*sx; d > 0 {
			r.slope = (sw*sxy - sx*sy) / d
			r.slopeSE = math.Sqrt(sw / d)
		}
	}
	if r.Clicks < bandFirst || len(valid) < 2 {
		return
	}
	best := valid[0]
	for _, i := range valid {
		w, b := r.Widths[i], r.Widths[best]
		if w.Miss+w.MissSE < b.Miss+b.MissSE {
			best = i
		}
	}
	b := r.Widths[best]
	near := func(i int) bool {
		w := r.Widths[i]
		return w.Miss <= b.Miss+math.Max(0.06, 2*math.Hypot(w.MissSE, b.MissSE))
	}
	for _, i := range valid {
		if !near(i) && r.support(i, 1) >= wallN && r.plays(i, 1) >= 2*minPlays {
			r.Widths[i].Worse = true
		}
	}
	r.Widths[best].Band = true
	r.HasBand, r.Best = true, b.W+0.5
	// grow the band from the best width, over gaps of at most 2 mm without
	// data, up to a width that goes worse
	grow := func(dir int) (edge float64, wall bool) {
		edge = b.W
		if dir > 0 {
			edge = b.W + 1
		}
		last := b.W
		for i := best + dir; i >= 0 && i < len(r.Widths); i += dir {
			w := r.Widths[i]
			if math.Abs(w.W-last) > 3 {
				break
			}
			if !judged(i) {
				continue // too little to say: neither in nor out
			}
			if !near(i) {
				return edge, w.Worse
			}
			r.Widths[i].Band = true
			last = w.W
			if dir > 0 {
				edge = w.W + 1
			} else {
				edge = w.W
			}
		}
		return edge, false
	}
	r.BandLo, r.LoWall = grow(-1)
	r.BandHi, r.HiWall = grow(+1)
}

// landing finds where you go from long to short, or that you land the same
// at every width (then it is a habit, not the area).
func (r *Report) landing() {
	var sw, sx, sy, sxx, sxy, n float64
	lo, hi := math.Inf(1), math.Inf(-1)
	var pk, pw float64
	for _, w := range r.Widths {
		if w.Jumps < 40 || w.LandSE <= 0 {
			continue
		}
		wt := 1 / (w.LandSE * w.LandSE)
		x := w.W + 0.5
		sw += wt
		sx += wt * x
		sy += wt * w.Land
		sxx += wt * x * x
		sxy += wt * x * w.Land
		pk += wt * w.Land
		pw += wt
		lo, hi = math.Min(lo, x), math.Max(hi, x)
		n++
	}
	if n < 2 {
		return
	}
	d := sw*sxx - sx*sx
	if d <= 0 {
		return
	}
	c := (sw*sxy - sx*sy) / d
	a := (sy - c*sx) / sw
	se := math.Sqrt(sw / d)
	span := hi - lo
	// long below, short above: the width where you land right
	if span >= 2 && c < 0 && -c > 2*se {
		if w0 := -a / c; w0 >= lo-4 && w0 <= hi+4 {
			r.HasCross, r.Cross = true, w0
			r.CrossRel = 0.7 * math.Min(1, span/8) * math.Min(1, r.Clicks/2000)
			return
		}
	}
	// about the same at every width tried: it follows you
	k, kse := pk/pw, math.Sqrt(1/pw)
	if n >= 3 && span >= math.Max(4, 0.05*hi) && math.Abs(k) >= 0.02 && math.Abs(k) > 3*kse && math.Abs(c)*span < 0.5*math.Abs(k) {
		r.Habit, r.HabitK = true, k
	}
}

// Decision is where the width goes on the next change, and how fast.
type Decision struct {
	Width  float64 // the width to go towards
	Factor float64 // share of the usual step (finer with more data, half outwards)
	Why    string
}

// Decide takes the width your last clicks ask for (fit) and the width now
// (cur), and says where to go with what the memory knows.
func (r Report) Decide(cur, fit float64) Decision {
	d := Decision{Width: fit, Factor: math.Max(0.45, 1/math.Sqrt(1+r.Clicks/3000)), Why: "following your last clicks"}
	if r.HasCross {
		d.Width = r.CrossRel*r.Cross + (1-r.CrossRel)*fit
		d.Why = fmt.Sprintf("towards %.1f mm, where you go from landing long to landing short", r.Cross)
	}
	// a habit isn't chased into widths not measured yet
	if r.Habit && (r.HabitK < 0 && d.Width < cur || r.HabitK > 0 && d.Width > cur) && !r.HasBand {
		d.Width = cur
		d.Why = r.habitWords()
		return d
	}
	if w := r.at(cur); w == nil || w.Clicks < validN {
		d.Factor *= 0.4
		if d.Why == "following your last clicks" {
			d.Why = "a width it doesn't know yet: smaller steps, to measure it"
		}
	}
	if !r.HasBand {
		return d
	}
	margin := math.Max(1.0, 0.015*cur)
	lo, hi := r.BandLo-margin, r.BandHi+margin
	if r.LoWall {
		lo = r.BandLo
	}
	if r.HiWall {
		hi = r.BandHi
	}
	// a habit isn't chased past the band
	if r.Habit && r.HabitK < 0 && d.Width < cur {
		lo = math.Max(lo, r.BandLo)
		d.Why = r.habitWords()
	}
	if r.Habit && r.HabitK > 0 && d.Width > cur {
		hi = math.Min(hi, r.BandHi)
		d.Why = r.habitWords()
	}
	if lo > hi {
		lo, hi = hi, lo
	}
	if d.Width < lo || d.Width > hi {
		d.Width = math.Max(lo, math.Min(hi, d.Width))
		if !r.Habit {
			if (d.Width == r.BandLo && r.LoWall) || (d.Width == r.BandHi && r.HiWall) {
				d.Why = "kept inside your band: past it you miss more"
			} else {
				d.Why = "only a little past your band, to see how it goes"
			}
		}
	}
	// going out of the band: half steps, slower still where the misses are
	// seen to grow that way
	out := (d.Width < cur && d.Width < r.BandLo) || (d.Width > cur && d.Width > r.BandHi)
	if out {
		d.Factor *= 0.5
		dir := math.Copysign(1, d.Width-cur)
		if r.slope*dir > 2*r.slopeSE {
			d.Factor *= 0.6
		}
	}
	return d
}

func (r Report) habitWords() string {
	if r.HabitK < 0 {
		return fmt.Sprintf("you land %.0f%% short at every width: that's how you aim, not the area, so it doesn't shrink for it", -100*r.HabitK)
	}
	return fmt.Sprintf("you land %.0f%% long at every width: that's how you aim, not the area, so it doesn't grow for it", 100*r.HabitK)
}

func (r Report) at(w float64) *Width {
	for i := range r.Widths {
		if w >= r.Widths[i].W && w < r.Widths[i].W+1 {
			return &r.Widths[i]
		}
	}
	return nil
}
