// Package calib remembers how you play at each width of the area, across
// days, and from that finds the band of widths that suit you: where you
// land neither short nor long, and where you press closest to the centres
// of the circles, compared on notes alike. The live calibration moves
// freely inside the band; its edges are elastic.
package calib

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
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
	// how much it counts (0-1): less in the first clicks after the area
	// changed, while the hand gets used to it (see AdaptWeight)
	Weight float64
}

// FromAim makes a Click from a judged click, its move and the width used.
func FromAim(c aim.Click, m aim.Move, width float64) (Click, bool) {
	if !c.Pressed || c.Radius <= 0 || width <= 0 {
		return Click{}, false
	}
	out := Click{Width: width, Dist: c.Dist(), ErrX: c.Cursor.X - c.Target.X, ErrY: c.Cursor.Y - c.Target.Y, Radius: c.Radius, Weight: 1}
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
	offCap    = 1.5 // a press counts at most this far from the centre (radii)
	classes   = 17
	jumpMin   = 2.0  // a jump, for long and short: at least two radii
	validN    = 60   // clicks around a width it needs to be judged
	wallN     = 150  // and to stop the area
	smoothMM  = 2.5  // widths this close help judge each other
	minPlays  = 2.5  // plays around a width it needs to be judged (one map can be just easy)
	ownSE     = 0.03 // a width this well measured is judged on its own
	probeN    = 600  // clicks a nearby width needs before it stops trying it
	bandFirst = 300  // clicks before there is a band
)

// off is how far a press counts from the centre of its circle, in radii:
// the distance, up to offCap (a press further away is a miss, and counts
// as one: misses on a hard map don't outweigh everything else).
func off(x float64) float64 { return math.Min(x, offCap) }

// class sorts the notes alike: by how far the jump is (in radii) and how
// much time there is for it. Presses are compared within a class.
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
	CN   float64 `json:"cn"` // clicks counted for the distance from the centre
	C    float64 `json:"c"`  // Σ distance from the centre (see off)
	C2   float64 `json:"c2"` // Σ its square
	Miss float64 `json:"m"`  // misses
	KN   float64 `json:"kn"` // jumps
	EL   float64 `json:"el"` // Σ error along the jump × its length
	E2   float64 `json:"e2"` // Σ error along the jump squared
	L2   float64 `json:"l2"` // Σ length squared
}

func (g *agg) add(c Click) {
	w := c.Weight
	v := off(c.Dist)
	g.N += w
	g.CN += w
	g.C += w * v
	g.C2 += w * v * v
	if c.Dist > 1 {
		g.Miss += w
	}
	if l := math.Hypot(c.MoveX, c.MoveY); c.Gap > 0 && l >= jumpMin*c.Radius && c.Dist < 2.5 {
		e := (c.ErrX*c.MoveX + c.ErrY*c.MoveY) / l
		g.KN += w
		g.EL += w * e * l
		g.E2 += w * e * e
		g.L2 += w * l * l
	}
}

// AdaptNeed is how many clicks the hand takes to get used to a change of
// the area of change mm (width, and position, added up).
func AdaptNeed(change float64) float64 {
	if change < 0.05 {
		return 0
	}
	return math.Max(15, math.Min(90, 30*change))
}

// AdaptWeight is how much the since-th click after a change counts, when it
// takes need clicks to get used to it: nothing in the first half, then more
// and more, fully from need on.
func AdaptWeight(since int, need float64) float64 {
	if need <= 0 {
		return 1
	}
	return math.Max(0, math.Min(1, (float64(since)-need/2)/(need/2)))
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
	// where the area goes best on the tablet (its centre), last found
	PosX float64 `json:"posX,omitempty"`
	PosY float64 `json:"posY,omitempty"`
	// a habit, once seen, stays until the clicks clearly say otherwise
	Habit bool `json:"habit,omitempty"`
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
	return fmt.Sprintf("%s|%.4f", tablet, ratio)
}

// KeyFor is the memory of a tablet and a shape: the one already kept for
// that tablet with the closest shape (within 0.003, so that rounding never
// splits a memory in two), or a new one.
func (s *Store) KeyFor(tablet string, ratio float64) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	best, bd := "", 0.003
	for k := range s.mem {
		t, r, ok := strings.Cut(k, "|")
		if !ok || t != tablet {
			continue
		}
		if v, err := strconv.ParseFloat(r, 64); err == nil && math.Abs(v-ratio) <= bd {
			best, bd = k, math.Abs(v-ratio)
		}
	}
	if best != "" {
		return best
	}
	return Key(tablet, ratio)
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
		if c.Width <= 0 || c.Radius <= 0 || math.IsNaN(c.Dist) || c.Dist > 3 || c.Weight <= 0 {
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

// SetPos notes where the area goes best on the tablet (its centre).
func (s *Store) SetPos(key string, x, y float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.mem[key]
	if m == nil {
		m = &memory{}
		s.mem[key] = m
	}
	m.PosX, m.PosY = x, y
	s.dirty = true
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
	// how far from the centre of the circles you press, compared with your
	// average on notes alike (1 = average, lower is better), from this width
	// and its neighbours
	Off   float64 `json:"off"`
	OffSE float64 `json:"offSE"`
	Own   float64 `json:"own"` // from this width alone
	OwnSE float64 `json:"ownSE"`
	Dist  float64 `json:"dist"` // average distance from the centre, in radii (not compared)
	Raw   float64 `json:"raw"`  // share of the presses outside the circle
	// along the jumps: + long, - short (share of the jump)
	Land   float64 `json:"land"`
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
	// the width where you press closest to the centres, and how sure that
	// is (0-1: it grows with the clicks around it and on both sides)
	HasBest     bool    `json:"hasBest"`
	Best        float64 `json:"best"`
	Reliability float64 `json:"reliability"`
	// where the area goes best on the tablet (its centre), if known
	HasPos bool    `json:"hasPos"`
	PosX   float64 `json:"posX"`
	PosY   float64 `json:"posY"`
	LoWall bool    `json:"loWall"`
	HiWall bool    `json:"hiWall"`
	// where you go from landing long (below) to landing short (above)
	HasCross bool    `json:"hasCross"`
	Cross    float64 `json:"cross"`
	CrossRel float64 `json:"crossRel"` // how much it is trusted (0-0.7)
	// you land short (or long) by about the same share at every width:
	// it follows you, it is how you aim
	Habit   bool    `json:"habit"`
	HabitK  float64 `json:"habitK"`
	slope   float64 // distance from the centre against width (per mm), and its uncertainty
	slopeSE float64
	// where the best could still be: the side (-1, +1) and how often it
	// came out more than a millimetre that way
	unsureSide  int
	unsureShare float64
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
	if m.PosX != 0 || m.PosY != 0 {
		r.HasPos, r.PosX, r.PosY = true, m.PosX, m.PosY
	}
	// your average distance from the centre on each kind of note, at every
	// width
	var cn, cc [classes]float64
	var tn, tc, all float64
	for _, c := range cells {
		all += c.w * c.g.N
		cn[c.cls] += c.w * c.g.CN
		cc[c.cls] += c.w * c.g.C
		tn += c.w * c.g.CN
		tc += c.w * c.g.C
	}
	if all == 0 {
		return r
	}
	r.Clicks = all
	base := func(cls int) float64 {
		if cn[cls] < 20 || tn == 0 {
			if tn == 0 {
				return 1
			}
			return math.Max(tc/tn, 1e-3)
		}
		return math.Max(cc[cls]/cn[cls], 1e-3)
	}
	// each millimetre of width
	type acc struct {
		n, cn, c, exp, vc, miss float64
		kn, el, e2, l2          float64
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
		a.cn += w * g.CN
		a.c += w * g.C
		a.exp += w * g.CN * base(c.cls)
		if g.CN > 0 {
			a.vc += w * w * math.Max(0, g.C2-g.C*g.C/g.CN)
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
		if a.exp > 0 && a.cn > 0 {
			wd.Own = a.c / a.exp
			wd.OffSE = math.Sqrt(a.vc) / a.exp
			wd.Dist = a.c / a.cn
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
	r.landing(m.Habit)
	if r.Habit != m.Habit {
		m.Habit = r.Habit
		s.dirty = true
	}
	return r
}

// smooth judges each width with its neighbours too: a straight line through
// the widths around it (the closer, the more they count), so that a few
// dozen clicks per millimetre are enough. A width measured well enough on
// its own is judged on its own (neighbours would blur where it starts to
// go worse).
func (r *Report) smooth() {
	type pt struct{ x, y, v float64 }
	var pts []pt
	for _, w := range r.Widths {
		if w.Clicks >= 15 && w.OffSE > 0 {
			pts = append(pts, pt{w.W + 0.5, w.Own, w.OffSE * w.OffSE})
		}
	}
	for i := range r.Widths {
		w := &r.Widths[i]
		own := w.OffSE
		w.OwnSE = own
		w.Off, w.OffSE = w.Own, 0
		if w.Clicks < 15 || own <= 0 {
			continue
		}
		if own <= ownSE {
			w.OffSE = own
			continue
		}
		x0 := w.W + 0.5
		var s0, s1, s2 float64
		us := make([]float64, len(pts))
		for j, p := range pts {
			d := (p.x - x0) / smoothMM
			u := math.Max(0, 1-d*d) / p.v
			us[j] = u
			s0 += u
			s1 += u * (p.x - x0)
			s2 += u * (p.x - x0) * (p.x - x0)
		}
		det := s0*s2 - s1*s1
		var est, vr float64
		for j, p := range pts {
			var l float64
			if det > 1e-9*s0*s0 {
				l = us[j] * (s2 - s1*(p.x-x0)) / det // local line
			} else {
				l = us[j] / s0
			}
			est += l * p.y
			vr += l * l * p.v
		}
		w.Off, w.OffSE = math.Max(0, est), math.Sqrt(vr)
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

// parabola fits off = a + b·x + c·x² to the points (weights 1/v), x
// measured from x0.
func parabola(xs, ys, vs []float64, x0 float64) (a, b, c float64, ok bool) {
	var m [3][4]float64
	for i := range xs {
		x, wt := xs[i]-x0, 1/vs[i]
		p := [3]float64{1, x, x * x}
		for r := 0; r < 3; r++ {
			for k := 0; k < 3; k++ {
				m[r][k] += wt * p[r] * p[k]
			}
			m[r][3] += wt * p[r] * ys[i]
		}
	}
	// Gauss-Jordan
	for col := 0; col < 3; col++ {
		piv := col
		for r := col + 1; r < 3; r++ {
			if math.Abs(m[r][col]) > math.Abs(m[piv][col]) {
				piv = r
			}
		}
		if math.Abs(m[piv][col]) < 1e-12 {
			return 0, 0, 0, false
		}
		m[col], m[piv] = m[piv], m[col]
		for r := 0; r < 3; r++ {
			if r == col {
				continue
			}
			f := m[r][col] / m[col][col]
			for k := col; k < 4; k++ {
				m[r][k] -= f * m[col][k]
			}
		}
	}
	return m[0][3] / m[0][0], m[1][3] / m[1][1], m[2][3] / m[2][2], true
}

// curveMin is where a parabola is lowest between lo and hi (x measured
// from x0), and its value there.
func curveMin(a, b, c, x0, lo, hi float64) (float64, float64) {
	f := func(x float64) float64 { d := x - x0; return a + b*d + c*d*d }
	x := lo
	if f(hi) < f(x) {
		x = hi
	}
	if c > 0 {
		if v := x0 - b/(2*c); v > lo && v < hi && f(v) < f(x) {
			x = v
		}
	}
	return x, f(x)
}

// band finds the width where you press closest to the centres, and the
// widths that go about as well, next to it.
func (r *Report) band() {
	r.smooth()
	judged := func(i int) bool {
		w := r.Widths[i]
		return w.Clicks >= 15 && w.OffSE > 0 && r.support(i, 1) >= validN && r.plays(i, 1) >= minPlays
	}
	var valid []int
	for i := range r.Widths {
		if judged(i) {
			valid = append(valid, i)
		}
	}
	// distance from the centre against width: which way it gets worse
	if len(valid) >= 2 {
		var sw, sx, sy, sxx, sxy float64
		for _, i := range valid {
			w := r.Widths[i]
			wt := 1 / (w.OffSE * w.OffSE)
			x := w.W + 0.5
			sw += wt
			sx += wt * x
			sy += wt * w.Off
			sxx += wt * x * x
			sxy += wt * x * w.Off
		}
		if d := sw*sxx - sx*sx; d > 0 {
			r.slope = (sw*sxy - sx*sy) / d
			r.slopeSE = math.Sqrt(sw / d)
		}
	}
	if len(valid) == 0 {
		r.reliability(nil)
		return
	}
	r.best(valid)
	defer r.reliability(valid)
	if r.Clicks < bandFirst || len(valid) < 2 {
		return
	}
	bi := -1
	for _, i := range valid {
		if bi < 0 || math.Abs(r.Widths[i].W+0.5-r.Best) < math.Abs(r.Widths[bi].W+0.5-r.Best) {
			bi = i
		}
	}
	b := r.Widths[bi]
	near := func(i int) bool {
		w := r.Widths[i]
		return w.Off <= b.Off+math.Max(0.04*b.Off, 2*math.Hypot(w.OffSE, b.OffSE))
	}
	// a width goes worse when it does with its neighbours and on its own
	// too (not only because the line through its neighbours says so)
	for _, i := range valid {
		w := r.Widths[i]
		ownWorse := w.Own-b.Off > math.Max(0.04*b.Off, math.Hypot(w.OwnSE, b.OffSE))
		if !near(i) && ownWorse && r.support(i, 1) >= wallN && r.plays(i, 1) >= 2*minPlays {
			r.Widths[i].Worse = true
		}
	}
	r.Widths[bi].Band = true
	r.HasBand = true
	// grow the band from the best width, over gaps of at most 2 mm without
	// data, up to a width that goes worse
	grow := func(dir int) (edge float64, wall bool) {
		edge = b.W
		if dir > 0 {
			edge = b.W + 1
		}
		last := b.W
		for i := bi + dir; i >= 0 && i < len(r.Widths); i += dir {
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

// window are the judged widths within 6 mm of the width x.
func (r *Report) window(valid []int, x float64) (xs, ys, vs []float64) {
	for _, i := range valid {
		w := r.Widths[i]
		if math.Abs(w.W+0.5-x) <= 6 {
			xs, ys, vs = append(xs, w.W+0.5), append(ys, w.Off), append(vs, w.OffSE*w.OffSE)
		}
	}
	return
}

// best finds the width where you press closest to the centres: the lowest
// point of a curve through the widths around the best measured one (not
// just the best millimetre, so that it moves smoothly as data comes).
func (r *Report) best(valid []int) {
	b0 := valid[0]
	for _, i := range valid {
		w, b := r.Widths[i], r.Widths[b0]
		if w.Off+w.OffSE < b.Off+b.OffSE {
			b0 = i
		}
	}
	x0 := r.Widths[b0].W + 0.5
	r.HasBest, r.Best = true, x0
	xs, ys, vs := r.window(valid, x0)
	if len(xs) < 3 {
		return
	}
	lo, hi := xs[0], xs[len(xs)-1]
	if hi-lo < 2 {
		return
	}
	if a, b, c, ok := parabola(xs, ys, vs, x0); ok {
		x, _ := curveMin(a, b, c, x0, lo, hi)
		r.Best = math.Round(x*10) / 10
	}
}

// reliability is how sure it is that the best width really is where you
// press closest to the centres, 0-1. The widths are drawn again many times
// within their uncertainty and the curve fitted again: each time, how much
// worse the best width is than the lowest point of that curve. Widths about
// as good count as good (it doesn't matter which of them is picked); one
// width with few clicks moves the curve only a little. It also needs widths
// measured on both sides of the best (otherwise the best could be past
// them) and plenty of clicks around it, so it grows as you play.
func (r *Report) reliability(valid []int) {
	if len(valid) == 0 {
		r.Reliability = 0.15 * math.Min(1, r.Clicks/3000)
		return
	}
	xs, ys, vs := r.window(valid, r.Best)
	rng := rand.New(rand.NewSource(7))
	const draws = 300
	regret, away, shift := 0.0, 0, 0.0
	dy := make([]float64, len(ys))
	for k := 0; k < draws; k++ {
		for i := range ys {
			dy[i] = ys[i] + rng.NormFloat64()*math.Sqrt(vs[i])
		}
		var xmin, fmin, fbest float64
		if len(xs) >= 3 && xs[len(xs)-1]-xs[0] >= 2 {
			a, b, c, ok := parabola(xs, dy, vs, r.Best)
			if !ok {
				continue
			}
			xmin, fmin = curveMin(a, b, c, r.Best, xs[0], xs[len(xs)-1])
			fbest = a
		} else {
			// too few widths for a curve: the widths themselves
			xmin, fmin = xs[0], dy[0]
			for i := range xs {
				if dy[i] < fmin {
					xmin, fmin = xs[i], dy[i]
				}
				if math.Abs(xs[i]-r.Best) < 0.6 {
					fbest = dy[i]
				}
			}
		}
		regret += math.Max(0, fbest-fmin) / math.Max(0.05, fmin)
		if math.Abs(xmin-r.Best) > 1 {
			away++
			shift += xmin - r.Best
		}
	}
	regret /= draws
	if away > 0 {
		r.unsureShare = float64(away) / draws
		r.unsureSide = 1
		if shift < 0 {
			r.unsureSide = -1
		}
	}
	prec := 1 / (1 + (regret/0.02)*(regret/0.02))
	// widths measured on both sides: of the best, or of the band (widths
	// about as good as the best count as the best); and the clicks there
	lo, hi := r.Best, r.Best
	if r.HasBand {
		lo, hi = math.Min(lo, r.BandLo), math.Max(hi, r.BandHi)
	}
	below, above := 0.0, 0.0
	near := 0.0
	for _, i := range valid {
		x := r.Widths[i].W + 0.5
		if x <= r.Best-0.5 && x >= lo-4 {
			below = 1
		}
		if x >= r.Best+0.5 && x <= hi+4 {
			above = 1
		}
	}
	for _, w := range r.Widths {
		x := w.W + 0.5
		if math.Abs(x-r.Best) <= 2 || r.HasBand && w.Band {
			near += w.Clicks
		}
	}
	cov := 0.4 + 0.3*below + 0.3*above
	amt := 1 - math.Exp(-near/2500)
	r.Reliability = prec * cov * amt
}

// landing finds where you go from long to short, or that you land the same
// at every width (then it is a habit, not the area). had: a habit was seen
// before; it stays until the clicks clearly say otherwise (so that a few
// noisy widths don't make it come and go).
func (r *Report) landing(had bool) {
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
		r.Habit = had
		return
	}
	d := sw*sxx - sx*sx
	if d <= 0 {
		r.Habit = had
		return
	}
	c := (sw*sxy - sx*sy) / d
	a := (sy - c*sx) / sw
	se := math.Sqrt(sw / d)
	span := hi - lo
	k, kse := pk/pw, math.Sqrt(1/pw)
	// long below, short above: the width where you land right
	if span >= 2 && c < 0 && -c > 2*se {
		if w0 := -a / c; w0 >= lo-4 && w0 <= hi+4 {
			r.HasCross, r.Cross = true, w0
			r.CrossRel = 0.7 * math.Min(1, span/8) * math.Min(1, r.Clicks/2000)
			return
		}
	}
	// about the same at every width tried: it follows you
	if had {
		gone := math.Abs(k) < 0.012 || (math.Abs(c)-2*se)*span > 0.6*math.Abs(k)
		r.Habit = !gone
	} else {
		r.Habit = n >= 3 && span >= math.Max(4, 0.05*hi) && math.Abs(k) >= 0.02 && math.Abs(k) > 3*kse && math.Abs(c)*span < 0.5*math.Abs(k)
	}
	if r.Habit {
		r.HabitK = k
	}
}

// measured tells whether there are enough clicks, from enough plays, within
// a millimetre of the width w.
func (r Report) measured(w float64) bool { return r.measuredN(w, probeN) }

func (r Report) measuredN(w, n float64) bool {
	c, p := 0.0, 0.0
	for _, x := range r.Widths {
		if math.Abs(x.W+0.5-w) <= 1 {
			c += x.Clicks
			p += x.Plays
		}
	}
	return c >= n && p >= 3
}

// Reliable is the reliability from which the best width can be kept: no
// more trying widths around it.
const Reliable = 0.75

// Plan is what the live calibration did last, for the next decision.
type Plan struct {
	Explore  bool    // it may play a stretch at a nearby width to measure it
	WasProbe bool    // the stretch just played was one of those
	LastSide int     // the side of the last one (-1 below, +1 above)
	Min, Max float64 // widths allowed (0: no limit)
}

// Decision is where the width goes next.
type Decision struct {
	Width  float64 // the width to go towards (or to, for a probe)
	Factor float64 // share of the usual step (finer with more data, half outwards)
	Probe  int     // -1 or +1: a stretch at a nearby width, to measure it (go straight there)
	Hold   bool    // nothing asks clearly for another width: it stays
	Why    string
}

const whyFollow = "following your last clicks"

// Next takes the width now (cur) and the width your last clicks ask for
// (fit, with its uncertainty fitSE), and says where to go next with what
// the memory knows.
func (r Report) Next(cur, fit, fitSE float64, p Plan) Decision {
	if d, ok := r.probe(cur, fit, p); ok {
		return d
	}
	d := Decision{Width: fit, Factor: math.Max(0.45, 1/math.Sqrt(1+r.Clicks/3000)), Why: whyFollow}
	if r.HasCross {
		d.Width = r.CrossRel*r.Cross + (1-r.CrossRel)*fit
		d.Why = fmt.Sprintf("towards %.1f mm, where you go from landing long to landing short", r.Cross)
	}
	// with a habit, what the clicks ask for that way is the habit itself:
	// not followed (how close to the centres you press can still move the
	// area, below)
	if r.Habit && (r.HabitK < 0 && d.Width < cur || r.HabitK > 0 && d.Width > cur) {
		d.Width, d.Why = cur, r.habitWords()
		if !r.HasBand {
			d.Hold = true
			return d
		}
	}
	if w := r.at(cur); w == nil || w.Clicks < validN {
		d.Factor *= 0.7
	}
	if r.HasBand {
		margin := math.Max(1.0, 0.015*cur)
		lo, hi := r.BandLo-margin, r.BandHi+margin
		if r.LoWall {
			lo = r.BandLo
		}
		if r.HiWall {
			hi = r.BandHi
		}
		if d.Width < lo || d.Width > hi {
			d.Width = math.Max(lo, math.Min(hi, d.Width))
			if !r.Habit {
				if (d.Width == r.BandLo && r.LoWall) || (d.Width == r.BandHi && r.HiWall) {
					d.Why = "kept inside your band: past it you press further from the centres"
				} else {
					d.Why = "only a little past your band, to see how it goes"
				}
			}
		}
		// going out of the band: half steps, slower still where it is seen
		// to get worse that way
		if (d.Width < cur && d.Width < r.BandLo) || (d.Width > cur && d.Width > r.BandHi) {
			d.Factor *= 0.5
			if dir := math.Copysign(1, d.Width-cur); r.slope*dir > 2*r.slopeSE {
				d.Factor *= 0.6
			}
		}
	}
	// your clicks alone don't ask clearly for another width: it stays (a
	// map is never perfect, and following its noise would never let you
	// settle)
	if d.Why == whyFollow && math.Abs(fit-cur) < math.Max(0.15, fitSE) {
		d.Width, d.Why = cur, "your clicks don't ask clearly for another width"
	}
	// the surer it is of the width where you press closest to the centres,
	// the more it goes there: when it is clearly better than the width now
	// (where all widths are about as good, it doesn't wander among them)
	if r.HasBest && r.HasBand && r.Best >= r.BandLo && r.Best <= r.BandHi && r.ClearlyBetter(cur) {
		k := r.Reliability
		if w := k*r.Best + (1-k)*d.Width; math.Abs(w-d.Width) >= 0.05 {
			d.Width = w
			if k >= 0.5 {
				d.Why = fmt.Sprintf("towards %.1f mm, where you press closest to the centres", r.Best)
			}
		}
	}
	if math.Abs(d.Width-cur) < 0.15 {
		d.Width, d.Hold = cur, true
		switch d.Why {
		case whyFollow, "your clicks don't ask clearly for another width":
			d.Why = "your clicks don't ask clearly for another width: it stays"
		}
	}
	return d
}

// probe decides whether to play the next stretch at a nearby width, to
// measure it: only while the best width isn't sure yet, and never twice in
// a row. First the side where the best could still be, else a side with
// little data (where your clicks point first).
func (r Report) probe(cur, fit float64, p Plan) (Decision, bool) {
	if !p.Explore || p.WasProbe || r.Reliability >= Reliable || r.Clicks < 150 {
		return Decision{}, false
	}
	allowed := func(w float64) bool {
		return !(p.Min > 0 && w < p.Min || p.Max > 0 && w > p.Max) && math.Abs(w-cur) >= 0.5
	}
	center := cur
	if r.HasBest {
		center = r.Best
	}
	dist := math.Max(1.5, 0.025*center)
	words := func(w float64, side int) string {
		if side < 0 {
			return fmt.Sprintf("trying %.1f mm (narrower) for a while, to measure it", w)
		}
		return fmt.Sprintf("trying %.1f mm (wider) for a while, to measure it", w)
	}
	wall := func(w float64, side int) bool {
		return side < 0 && r.LoWall && w < r.BandLo || side > 0 && r.HiWall && w > r.BandHi
	}
	try := func(side int, why string) (Decision, bool) {
		w := center + float64(side)*dist
		if wall(w, side) || !allowed(w) {
			return Decision{}, false
		}
		return Decision{Width: math.Round(w*100) / 100, Factor: 1, Probe: side, Why: words(w, side) + why}, true
	}
	// the order of the sides: where your clicks point first
	sides := []int{-1, 1}
	if fit > cur || fit == cur && p.LastSide < 0 {
		sides = []int{1, -1}
	}
	if p.LastSide != 0 && fit == cur {
		sides = []int{-p.LastSide, p.LastSide}
	}
	// first a side of the best with nothing measured (the best could be past it)
	for _, side := range sides {
		if !r.coveredSide(side) {
			if d, ok := try(side, ": nothing measured on that side yet"); ok {
				return d, true
			}
		}
	}
	// then the side where the best could still be, unless measured a lot already
	if r.unsureShare >= 0.2 && !r.measuredN(center+float64(r.unsureSide)*dist, 3*probeN) {
		if d, ok := try(r.unsureSide, fmt.Sprintf(": the best could be that way rather than %.1f mm", center)); ok {
			return d, true
		}
	}
	// then a side with little data
	for _, side := range sides {
		if r.measured(center + float64(side)*dist) {
			continue
		}
		if d, ok := try(side, ": there's little data on that side yet"); ok {
			return d, true
		}
	}
	return Decision{}, false
}

// coveredSide tells whether widths are measured on one side of the best
// (-1 below, +1 above), within 4 mm of it or of the band.
func (r Report) coveredSide(side int) bool {
	lo, hi := r.Best, r.Best
	if r.HasBand {
		lo, hi = math.Min(lo, r.BandLo), math.Max(hi, r.BandHi)
	}
	for i, w := range r.Widths {
		x := w.W + 0.5
		if w.Clicks < 15 || w.OffSE <= 0 || r.support(i, 1) < validN {
			continue
		}
		if side < 0 && x <= r.Best-0.5 && x >= lo-4 || side > 0 && x >= r.Best+0.5 && x <= hi+4 {
			return true
		}
	}
	return false
}

// ClearlyBetter tells whether at the best width you press clearly closer
// to the centres than at the width w (or w isn't measured).
func (r Report) ClearlyBetter(w float64) bool {
	b, c := r.at(r.Best), r.at(w)
	if b == nil {
		return false
	}
	if c == nil || c.Clicks < 15 || c.OffSE <= 0 {
		return true
	}
	return c.Off-b.Off > math.Max(0.03*b.Off, 1.5*math.Hypot(c.OffSE, b.OffSE))
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
