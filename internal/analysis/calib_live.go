package analysis

import (
	"math"
	"sort"
	"time"

	"areacoach/internal/otd"
)

// The diagnosis map by map: every map is played with one area, set between
// maps. Its replay is cut into stretches of about half a minute, each
// measured on its own.

// LiveSwitch is an area set by the diagnosis, and when.
type LiveSwitch struct {
	At   time.Time `json:"at"`
	Area otd.Rect  `json:"area"`
}

// AreaAt is the area the diagnosis had set at a time (base before the first
// change).
func AreaAt(base otd.Rect, sw []LiveSwitch, t time.Time) otd.Rect {
	a := base
	for _, s := range sw {
		if !s.At.After(t) {
			a = s.Area
		} else {
			break
		}
	}
	return a
}

// MapWindows measures a play made with one area (the one of its setup), in
// pieces of about AdaptMaxSec seconds. The end of the play is p.Time.
func MapWindows(p *PlayData) []CalibPoint {
	rate := p.rateOr1()
	var hs []Hit
	for _, h := range p.Hits {
		if h.HasClick() && h.Kind <= 1 {
			hs = append(hs, h)
		}
	}
	if len(hs) == 0 {
		return nil
	}
	last := float64(hs[len(hs)-1].Time) + float64(hs[len(hs)-1].CT)
	m := p.Setup.Mapper()
	parts := windowParts(hs, rate)
	var out []CalibPoint
	for k, part := range parts {
		cp, ok := windowPoint(part, m)
		if !ok || cp.SG <= 0 {
			continue
		}
		cp.Area, cp.Play = p.Setup.Area, p.ID
		a, b := part[0], part[len(part)-1]
		cp.From, cp.To = round(float64(a.Time)+float64(a.CT), 0), round(float64(b.Time)+float64(b.CT), 0)
		cp.Sec = round((cp.To-cp.From)/1000/rate, 2)
		cp.At = p.Time.Add(-time.Duration((last - cp.To) / rate * float64(time.Millisecond)))
		cp.Src = "replay"
		_ = k
		out = append(out, cp)
	}
	return out
}

// windowParts splits the circles of one area into pieces of about
// AdaptMaxSec seconds (none shorter than AdaptMinNotes circles).
func windowParts(hs []Hit, rate float64) [][]Hit {
	var out [][]Hit
	start := 0
	for i := range hs {
		dur := (float64(hs[i].Time) - float64(hs[start].Time)) / 1000 / rate
		if dur >= AdaptMaxSec*0.75 && i-start >= AdaptMinNotes && len(hs)-i >= AdaptMinNotes {
			out = append(out, hs[start:i])
			start = i
		}
	}
	return append(out, hs[start:])
}

// windowPoint measures the aim of a group of notes. The window's own offset
// and its over/undershoot are taken out: right after a change they say how
// used the hand is to the area, not how precise it can be with it.
func windowPoint(hs []Hit, m Mapper) (CalibPoint, bool) {
	type note = winNote
	var ns, all []note
	var mx, my float64
	far := 0
	for _, h := range hs {
		R := float64(h.Radius)
		if R <= 0 {
			continue
		}
		rx, ry := float64(h.CX-h.TX)/R, float64(h.CY-h.TY)/R
		n := note{rx: rx, ry: ry, R: R, speed: math.NaN(), dt: math.NaN()}
		// the movement actually made: from where the cursor started
		switch {
		case h.HasOrigin():
			n.jx, n.jy = float64(h.TX-h.OX)/R, float64(h.TY-h.OY)/R
		case !math.IsNaN(float64(h.PX)):
			n.jx, n.jy = float64(h.TX-h.PX)/R, float64(h.TY-h.PY)/R
		}
		if h.Dt > 0 && (h.HasOrigin() || !math.IsNaN(float64(h.PX))) {
			n.dt = float64(h.Dt)
		}
		if !math.IsNaN(float64(h.PX)) && h.Dt > 0 {
			n.speed = math.Hypot(float64(h.TX-h.PX), float64(h.TY-h.PY)) / float64(h.Dt)
		}
		n.qx, n.qy = float64(h.TX)/R, float64(h.TY)/R
		n.tx, n.ty = m.Point(float64(h.TX), float64(h.TY))
		// every click says how long the movement was against the one needed,
		// even (above all) when it lands far from the circle
		all = append(all, n)
		if math.Hypot(rx, ry) > 3 {
			// far off: for the precision, mostly a hand not yet used to the
			// area
			far++
			continue
		}
		mx += n.qx
		my += n.qy
		ns = append(ns, n)
	}
	if len(ns)+far >= 12 && far > 2*len(ns) {
		// most clicks far from the circles: the area is clearly too hard
		// for now; it counts as such instead of disappearing
		n := float64(len(ns) + far)
		var sq float64
		for _, nt := range ns {
			e := math.Min(math.Hypot(nt.rx, nt.ry), 3)
			sq += e * e
		}
		sq += 9 * float64(far)
		e := round(math.Sqrt(sq/(2*n)), 4)
		cp := CalibPoint{Notes: len(ns) + far, Err: e, ErrX: e, ErrY: e, Miss: round(1000*(float64(far)+missIn(ns, func(nt note) float64 { return math.Hypot(nt.rx, nt.ry) }))/n, 2)}
		adaptMeasure(all, m, &cp)
		return cp, true
	}
	if len(ns) < 12 {
		return CalibPoint{}, false
	}
	mx /= float64(len(ns))
	my /= float64(len(ns))
	// r = a + g·jump + h·(position - mean), isotropic gains
	var A [4][4]float64
	var B [4]float64
	add := func(row [4]float64, y float64) {
		for a := 0; a < 4; a++ {
			for b := 0; b < 4; b++ {
				A[a][b] += row[a] * row[b]
			}
			B[a] += row[a] * y
		}
	}
	for i := range ns {
		n := &ns[i]
		n.qx -= mx
		n.qy -= my
		add([4]float64{1, 0, n.jx, n.qx}, n.rx)
		add([4]float64{0, 1, n.jy, n.qy}, n.ry)
	}
	for j := 2; j < 4; j++ {
		A[j][j] += 1e-3
	}
	M := [][]float64{A[0][:], A[1][:], A[2][:], A[3][:]}
	sol, ok := solve(M, [][]float64{{B[0]}, {B[1]}, {B[2]}, {B[3]}})
	if !ok {
		return CalibPoint{}, false
	}
	ax, ay, g, hq := sol[0][0], sol[1][0], sol[2][0], sol[3][0]
	var sq, sqx, sqy float64
	miss := 0
	var sp []float64
	var nsum NoiseSum
	var rot RotSum
	// where the clicks land part by part of the screen: a part always off
	// the same way (a corner the hand reaches badly) is a habit there, not
	// how steady the hand is, so the scatter is measured around each part
	noteCells(ns)
	var qn, qex, qey [9]float64
	for i := range ns {
		n := &ns[i]
		k := n.cell
		qn[k]++
		qex[k] += clamp(n.rx-ax-g*n.jx-hq*n.qx, -2.5, 2.5)
		qey[k] += clamp(n.ry-ay-g*n.jy-hq*n.qy, -2.5, 2.5)
	}
	for k := range qn {
		qex[k], qey[k] = qex[k]/(qn[k]+cellRidge), qey[k]/(qn[k]+cellRidge)
	}
	for i := range ns {
		n := &ns[i]
		ex := n.rx - ax - g*n.jx - hq*n.qx
		ey := n.ry - ay - g*n.jy - hq*n.qy
		// how much the movement came out turned, and where on the tablet
		if mvx, mvy := m.Vec(n.jx*n.R, n.jy*n.R); math.Hypot(mvx, mvy) >= rotMinMove {
			erx, ery := m.Vec(clamp(ex, -2.5, 2.5)*n.R, clamp(ey, -2.5, 2.5)*n.R)
			d2 := mvx*mvx + mvy*mvy
			psi := clamp((mvx*ery-mvy*erx)/d2, -0.35, 0.35)
			rot.add(n.tx-mvx/2, n.ty-mvy/2, psi, d2)
		}
		// the scatter on the tablet, along and across the movement made
		if finite(n.dt) {
			k := n.cell
			mx, my := m.Vec(n.jx*n.R, n.jy*n.R)
			px, py := m.Vec(clamp(clamp(ex, -2.5, 2.5)-qex[k], -2.5, 2.5)*n.R, clamp(clamp(ey, -2.5, 2.5)-qey[k], -2.5, 2.5)*n.R)
			D := math.Hypot(mx, my)
			par, perp := px, py
			if D >= 1.5 {
				ux, uy := mx/D, my/D
				par, perp = px*ux+py*uy, -px*uy+py*ux
			}
			nsum.add(par, perp, D, D/n.dt)
		}
		e := math.Min(math.Hypot(ex, ey), 2.5)
		sq += e * e
		ex, ey = math.Max(-2.5, math.Min(2.5, ex)), math.Max(-2.5, math.Min(2.5, ey))
		sqx += ex * ex
		sqy += ey * ey
		if math.Hypot(n.rx, n.ry) > 1 {
			miss++
		}
		if !math.IsNaN(n.speed) {
			sp = append(sp, n.speed)
		}
	}
	nn := float64(len(ns))
	cp := CalibPoint{Notes: len(ns), Gain: round(math.Abs(g), 4)}
	cp.Err = round(math.Sqrt(sq/(2*nn-4)), 4)
	cp.ErrX = round(math.Sqrt(sqx/(nn-2)), 4)
	cp.ErrY = round(math.Sqrt(sqy/(nn-2)), 4)
	cp.Miss = round(1000*float64(miss)/nn, 2)
	R := ns[0].R
	dx, dy := m.Vec(ax*R, ay*R)
	cp.DX, cp.DY = round(dx, 3), round(dy, 3)
	mean := 0.0
	for _, v := range sp {
		mean += v
	}
	if len(sp) > 0 {
		mean /= float64(len(sp))
	}
	cp.Speed = round(math.Log(mean+0.05), 4)
	if rot.N >= 12 {
		cp.Rot = &rot
	}
	if nsum.N >= 12 {
		// four numbers were fitted on the clicks: the scatter left is a
		// little smaller than the true one
		k := float64(2*len(ns)) / float64(2*len(ns)-4)
		nsum.Par, nsum.Perp = nsum.Par*k, nsum.Perp*k
		cp.NS = &nsum
	}
	adaptMeasure(all, m, &cp)
	return cp, true
}

// winNote is one click of a window, in radii: the error, the movement
// needed to reach the circle, its position from the window's mean.
type winNote struct {
	rx, ry, jx, jy, qx, qy, R, speed, dt, tx, ty float64
	cell                                         int // the part of the screen (see noteCells)
}

// noteCells puts every note in one of nine parts of the screen, by the
// thirds of where the circles of the window are.
func noteCells(ns []winNote) {
	if len(ns) == 0 {
		return
	}
	xs := make([]float64, len(ns))
	ys := make([]float64, len(ns))
	for i, n := range ns {
		xs[i], ys[i] = n.qx, n.qy
	}
	sort.Float64s(xs)
	sort.Float64s(ys)
	k := len(ns)
	x1, x2 := xs[k/3], xs[2*k/3]
	y1, y2 := ys[k/3], ys[2*k/3]
	third := func(v, a, b float64) int {
		switch {
		case v < a:
			return 0
		case v < b:
			return 1
		}
		return 2
	}
	for i := range ns {
		ns[i].cell = 3*third(ns[i].qy, y1, y2) + third(ns[i].qx, x1, x2)
	}
}

// cellRidge is how many clicks of a part of the screen it takes for half of
// their own offset to count as a habit of that part.
const cellRidge = 6.0

// fitIn shrinks an area, keeping its shape, until it fits the tablet.
func fitIn(r otd.Rect, tabletW, tabletH float64) otd.Rect {
	if tabletW > 0 && tabletH > 0 {
		k := math.Min(1, math.Min(tabletW/r.W, tabletH/r.H))
		r.W, r.H = r.W*k, r.H*k
		r = ClampToTablet(r, tabletW, tabletH)
	}
	return r
}

func missIn[T any](xs []T, dist func(T) float64) float64 {
	n := 0.0
	for _, x := range xs {
		if dist(x) > 1 {
			n++
		}
	}
	return n
}
