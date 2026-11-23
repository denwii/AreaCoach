package aim

import (
	"math"
	"math/rand"
	"sort"

	"areacoach/internal/geom"
	"areacoach/internal/otd"
)

// Sample is one press, ready for the fit.
type Sample struct {
	Pen    [2]float64    // where the pen was on the tablet (mm)
	Screen [2]float64    // where the centre of the circle was on the screen (px)
	ToPx   [2][2]float64 // tablet mm -> osu!pixels, with the area used then
	Err    [2]float64    // cursor - centre with the area used (osu!pixels)
	Radius float64       // circle radius (osu!pixels)
}

// Limits are what the area may be.
type Limits struct {
	Display          otd.Rect // the screen part OpenTabletDriver maps to (px)
	Rot              float64  // rotation, kept as it is (degrees)
	Ratio            float64  // height / width
	MinW, MaxW       float64  // width range (0: no limit)
	TabletW, TabletH float64  // tablet size (0: unknown)
}

// Stats describe the presses with one area.
type Stats struct {
	Clicks int     `json:"clicks"`
	Misses int     `json:"misses"` // presses outside the circle
	OffX   float64 `json:"offX"`   // average press - centre, in radii (+ right)
	OffY   float64 `json:"offY"`   // (+ down)
	Dist   float64 `json:"dist"`   // average distance from the centre, in radii
}

// Result is the area that fits the presses best.
type Result struct {
	Area   otd.Rect   `json:"area"`
	Before Stats      `json:"before"` // with the area (or areas) used
	After  Stats      `json:"after"`  // with Area, same hand movements
	SE     [3]float64 `json:"se"`     // uncertainty of width, X, Y (mm)
	AtMin  bool       `json:"atMin"`  // the width is at the bottom of the range
	AtMax  bool       `json:"atMax"`  // or at the top
}

// The score of an area: each press inside the circle counts one (a soft
// step, so that presses on the edge count about half and the search has a
// slope to follow), minus a small pull towards the centre of the circles
// (beta per radius squared, presses further than distCap radii away left
// out: they were not aimed at the circle). Hits count far more than the
// pull: moving a hundred presses from 0.6 to 0.4 radii away is worth one hit.
const (
	stepSoft = 0.1
	beta     = 0.05
	distCap  = 2.0
)

func soft(x float64) float64 { return 1 / (1 + math.Exp((x-1)/stepSoft)) }

func pull(x float64) float64 {
	x = math.Min(x, distCap)
	return beta * x * x
}

// problem holds what the fit needs, prepared once.
type problem struct {
	lim      Limits
	pen      [][2]float64
	v        [][2]float64 // circle centre on the tablet = c + W·v
	j        [][2][2]float64
	r        []float64
	lo, hi   float64
	tabletOK bool
}

func newProblem(s []Sample, lim Limits) *problem {
	p := &problem{lim: lim}
	th := lim.Rot * math.Pi / 180
	c, si := math.Cos(th), math.Sin(th)
	for _, x := range s {
		if x.Radius <= 0 {
			continue
		}
		gx := (x.Screen[0] - lim.Display.X) / lim.Display.W
		gy := (x.Screen[1] - lim.Display.Y) / lim.Display.H * lim.Ratio
		p.pen = append(p.pen, x.Pen)
		p.v = append(p.v, [2]float64{c*gx - si*gy, si*gx + c*gy})
		p.j = append(p.j, x.ToPx)
		p.r = append(p.r, x.Radius)
	}
	p.tabletOK = lim.TabletW > 0 && lim.TabletH > 0
	p.lo, p.hi = math.Max(lim.MinW, 5), math.Inf(1)
	if lim.MaxW > 0 {
		p.hi = lim.MaxW
	}
	if p.tabletOK {
		p.hi = math.Min(p.hi, geom.MaxWidth(lim.TabletW, lim.TabletH, lim.Ratio, lim.Rot))
	}
	if p.lo > p.hi {
		p.lo = p.hi
	}
	return p
}

func (p *problem) rect(q [3]float64) otd.Rect {
	return otd.Rect{W: q[0], H: q[0] * p.lim.Ratio, X: q[1], Y: q[2], Rot: p.lim.Rot}
}

// project puts an area inside the limits.
func (p *problem) project(q [3]float64) [3]float64 {
	q[0] = math.Max(p.lo, math.Min(p.hi, q[0]))
	if p.tabletOK {
		r := geom.ClampToTablet(p.rect(q), p.lim.TabletW, p.lim.TabletH)
		q[1], q[2] = r.X, r.Y
	}
	return q
}

// dist is how far press i is from the centre of its circle with the area q,
// in radii, keeping the circles as big on the tablet as they were.
func (p *problem) dist(i int, q [3]float64) (ex, ey, x float64) {
	dx := p.pen[i][0] - q[1] - q[0]*p.v[i][0]
	dy := p.pen[i][1] - q[2] - q[0]*p.v[i][1]
	j := &p.j[i]
	ex, ey = j[0][0]*dx+j[0][1]*dy, j[1][0]*dx+j[1][1]*dy
	return ex, ey, math.Hypot(ex, ey) / p.r[i]
}

func (p *problem) score(q [3]float64, idx []int) float64 {
	s := 0.0
	if idx == nil {
		for i := range p.pen {
			_, _, x := p.dist(i, q)
			s += soft(x) - pull(x)
		}
		return s
	}
	for _, i := range idx {
		_, _, x := p.dist(i, q)
		s += soft(x) - pull(x)
	}
	return s
}

// centre is the best place of an area of width w, by least squares on the
// presses that were aimed at their circle (reweighted a few times).
func (p *problem) centre(w float64, from [3]float64) [3]float64 {
	q := from
	q[0] = w
	for it := 0; it < 4; it++ {
		var a [2][2]float64
		var b [2]float64
		for i := range p.pen {
			_, _, x := p.dist(i, q)
			if x >= distCap {
				continue
			}
			t := 1 - (x/distCap)*(x/distCap)
			wt := t * t / (p.r[i] * p.r[i])
			j := &p.j[i]
			// JᵀJ
			m00 := j[0][0]*j[0][0] + j[1][0]*j[1][0]
			m01 := j[0][0]*j[0][1] + j[1][0]*j[1][1]
			m11 := j[0][1]*j[0][1] + j[1][1]*j[1][1]
			tx := p.pen[i][0] - w*p.v[i][0]
			ty := p.pen[i][1] - w*p.v[i][1]
			a[0][0] += wt * m00
			a[0][1] += wt * m01
			a[1][1] += wt * m11
			b[0] += wt * (m00*tx + m01*ty)
			b[1] += wt * (m01*tx + m11*ty)
		}
		det := a[0][0]*a[1][1] - a[0][1]*a[0][1]
		if math.Abs(det) < 1e-12 {
			break
		}
		q[1] = (a[1][1]*b[0] - a[0][1]*b[1]) / det
		q[2] = (a[0][0]*b[1] - a[0][1]*b[0]) / det
	}
	return p.project(q)
}

// climb improves an area from q with Nelder-Mead.
func (p *problem) climb(q [3]float64, idx []int) ([3]float64, float64) {
	f := func(x [3]float64) float64 { return -p.score(p.project(x), idx) }
	steps := [3]float64{0.6, 0.4, 0.4}
	var pts [4][3]float64
	var val [4]float64
	for k := range pts {
		pts[k] = q
		if k > 0 {
			pts[k][k-1] += steps[k-1]
		}
		val[k] = f(pts[k])
	}
	for it := 0; it < 400; it++ {
		// order
		ord := [4]int{0, 1, 2, 3}
		sort.Slice(ord[:], func(a, b int) bool { return val[ord[a]] < val[ord[b]] })
		var np [4][3]float64
		var nv [4]float64
		for k, o := range ord {
			np[k], nv[k] = pts[o], val[o]
		}
		pts, val = np, nv
		size := 0.0
		for k := 1; k < 4; k++ {
			for d := 0; d < 3; d++ {
				size = math.Max(size, math.Abs(pts[k][d]-pts[0][d]))
			}
		}
		if size < 0.002 {
			break
		}
		var c [3]float64
		for k := 0; k < 3; k++ {
			for d := 0; d < 3; d++ {
				c[d] += pts[k][d] / 3
			}
		}
		at := func(t float64) [3]float64 {
			var x [3]float64
			for d := 0; d < 3; d++ {
				x[d] = c[d] + t*(pts[3][d]-c[d])
			}
			return x
		}
		xr := at(-1)
		fr := f(xr)
		switch {
		case fr < val[0]:
			xe := at(-2)
			if fe := f(xe); fe < fr {
				pts[3], val[3] = xe, fe
			} else {
				pts[3], val[3] = xr, fr
			}
		case fr < val[2]:
			pts[3], val[3] = xr, fr
		default:
			xc := at(0.5)
			if fr < val[3] {
				xc = at(-0.5)
			}
			if fc := f(xc); fc < math.Min(fr, val[3]) {
				pts[3], val[3] = xc, fc
			} else {
				for k := 1; k < 4; k++ {
					for d := 0; d < 3; d++ {
						pts[k][d] = pts[0][d] + 0.5*(pts[k][d]-pts[0][d])
					}
					val[k] = f(pts[k])
				}
			}
		}
	}
	best := 0
	for k := 1; k < 4; k++ {
		if val[k] < val[best] {
			best = k
		}
	}
	return p.project(pts[best]), -val[best]
}

// search finds the best area: the widths across the range, each in its best
// place, then the best few refined.
func (p *problem) search(start [3]float64, idx []int) [3]float64 {
	lo, hi := p.lo, p.hi
	if math.IsInf(hi, 1) {
		hi = math.Max(lo, start[0]*1.6)
	}
	if lo < start[0]*0.5 && hi > start[0]*0.5 {
		lo = math.Max(lo, start[0]*0.5)
	}
	if hi < lo {
		hi = lo
	}
	type cand struct {
		q [3]float64
		s float64
	}
	var cs []cand
	from := p.project(start)
	cs = append(cs, cand{from, p.score(from, idx)})
	c0 := p.centre(from[0], from)
	cs = append(cs, cand{c0, p.score(c0, idx)})
	n := int(math.Ceil((hi-lo)/0.5)) + 1
	if n > 400 {
		n = 400
	}
	for k := 0; k < n; k++ {
		w := lo
		if n > 1 {
			w = lo + (hi-lo)*float64(k)/float64(n-1)
		}
		q := p.centre(w, from)
		cs = append(cs, cand{q, p.score(q, idx)})
	}
	sort.SliceStable(cs, func(a, b int) bool { return cs[a].s > cs[b].s })
	best, bs := cs[0].q, math.Inf(-1)
	for k := 0; k < len(cs) && k < 4; k++ {
		q, s := p.climb(cs[k].q, idx)
		if s > bs+1e-9 {
			best, bs = q, s
		}
	}
	return best
}

func (p *problem) stats(q [3]float64) Stats {
	var st Stats
	n := 0
	for i := range p.pen {
		ex, ey, x := p.dist(i, q)
		st.Clicks++
		if x > 1 {
			st.Misses++
		}
		if x < distCap {
			st.OffX += ex / p.r[i]
			st.OffY += ey / p.r[i]
			st.Dist += x
			n++
		}
	}
	if n > 0 {
		st.OffX /= float64(n)
		st.OffY /= float64(n)
		st.Dist /= float64(n)
	}
	return st
}

// StatsOf describes the presses as they were.
func StatsOf(s []Sample) Stats {
	var st Stats
	n := 0
	for _, x := range s {
		if x.Radius <= 0 {
			continue
		}
		d := math.Hypot(x.Err[0], x.Err[1]) / x.Radius
		st.Clicks++
		if d > 1 {
			st.Misses++
		}
		if d < distCap {
			st.OffX += x.Err[0] / x.Radius
			st.OffY += x.Err[1] / x.Radius
			st.Dist += d
			n++
		}
	}
	if n > 0 {
		st.OffX /= float64(n)
		st.OffY /= float64(n)
		st.Dist /= float64(n)
	}
	return st
}

// MinSamples is how many presses a fit needs.
const MinSamples = 20

// Fit finds the area, within the limits, with which the same pen movements
// would have put the most presses inside their circles (and, among areas
// as good, the presses closest to the centres). start is the area in use:
// the search begins there. The width is rounded to 0.01 mm, the position
// to 0.01 mm. uncertainty adds how much the answer would move with other
// presses like these (a little slower).
func Fit(s []Sample, lim Limits, start otd.Rect, uncertainty bool) (Result, bool) {
	if lim.Ratio <= 0 || !lim.Display.Valid() {
		return Result{}, false
	}
	p := newProblem(s, lim)
	if len(p.pen) < MinSamples {
		return Result{}, false
	}
	q0 := [3]float64{start.W, start.X, start.Y}
	best := p.search(q0, nil)
	best = p.project([3]float64{math.Round(best[0]*100) / 100, math.Round(best[1]*100) / 100, math.Round(best[2]*100) / 100})
	// misses come first: never more of them than with the area in use
	if from := p.project(q0); p.stats(from).Misses < p.stats(best).Misses {
		best = from
	}
	res := Result{Area: p.rect(best), Before: StatsOf(s), After: p.stats(best)}
	res.Area.H = math.Round(res.Area.H*1000) / 1000
	res.AtMin = best[0] <= p.lo+0.005 && p.lo > 5
	res.AtMax = best[0] >= p.hi-0.005 && !math.IsInf(p.hi, 1)
	if uncertainty {
		res.SE = p.spread(best)
	}
	return res, true
}

// spread is the bootstrap standard deviation of the answer: the fit done
// again on presses drawn from these, the same draws every time.
func (p *problem) spread(best [3]float64) [3]float64 {
	const rounds = 24
	rng := rand.New(rand.NewSource(1))
	n := len(p.pen)
	idx := make([]int, n)
	var sum, sum2 [3]float64
	for r := 0; r < rounds; r++ {
		for i := range idx {
			idx[i] = rng.Intn(n)
		}
		q, _ := p.climb(best, idx)
		for d := 0; d < 3; d++ {
			sum[d] += q[d]
			sum2[d] += q[d] * q[d]
		}
	}
	var se [3]float64
	for d := 0; d < 3; d++ {
		m := sum[d] / rounds
		se[d] = math.Sqrt(math.Max(0, sum2[d]/rounds-m*m))
	}
	return se
}
