package analysis

import (
	"math"

	"areacoach/internal/otd"
)

// Turned aim. The hand moves the pen around a pivot (the wrist, or the
// elbow for arm aim): a movement meant as straight comes out a little turned,
// by how far around the pivot it happens compared with where the hand learned
// to aim straight. The diagnosis does not rotate the area (the rotation set
// in OpenTabletDriver stays as it is): it measures how much the movements
// come out turned with the area in use, and how that changes from one side
// of the tablet to the other, and from the two finds where on the tablet the
// same hand, with the same rotation, aims straight. The area slides sideways
// toward there, a few mm a map, measured again every time.

// RotSum sums up how the movements of a stretch were turned: for each click
// after a long enough movement, the angle between the movement made and the
// one needed (clockwise on the tablet, radians) against where on the tablet
// it happened (mm), weighted by the squared length of the movement (a long
// movement tells the angle better).
type RotSum struct {
	N int `json:"n"`
	// Σw, Σwx, Σwy, Σwxx, Σwxy, Σwyy, Σwψ, Σwxψ, Σwyψ, Σwψψ
	S [10]float64 `json:"s"`
}

func (r *RotSum) add(x, y, psi, w float64) {
	r.N++
	r.S[0] += w
	r.S[1] += w * x
	r.S[2] += w * y
	r.S[3] += w * x * x
	r.S[4] += w * x * y
	r.S[5] += w * y * y
	r.S[6] += w * psi
	r.S[7] += w * x * psi
	r.S[8] += w * y * psi
	r.S[9] += w * psi * psi
}

// rotMinMove is the shortest movement (mm) whose angle counts.
const rotMinMove = 6.0

// ArcFit is what the diagnosis found about turned aim.
type ArcFit struct {
	N      int     `json:"n"`      // stretches used
	Clicks int     `json:"clicks"` // movements used
	Mean   float64 `json:"mean"`   // turning with the area in use (degrees, + clockwise)
	MeanSE float64 `json:"meanSe"`
	// how the turning changes across the tablet (degrees per 10 mm, to the
	// right and downward), and how sure
	GX float64 `json:"gx"`
	GY float64 `json:"gy"`
	SE float64 `json:"se"`
	// the side of the pivot, seen from the area: 0 straight below it, +
	// the pivot to the left, - to the right (degrees)
	Dir   float64 `json:"dir"`
	DirSE float64 `json:"dirSe"`
	// the turning clearly changes from left to right: where it is zero can
	// be told
	Strong bool `json:"strong"`
	// how far sideways the aim would come out straight (mm, + to the right),
	// and how far the area slides now; why not when it does not
	Zero  float64 `json:"zero"`
	Shift float64 `json:"shift"`
	Note  string  `json:"note,omitempty"`
}

// arcFit pools the stretches measured near the area in use (where the arc
// is the same) into how much the movements are turned there, and how that
// changes across the tablet.
func arcFit(ps []CalibPoint, cur otd.Rect) ArcFit {
	var out ArcFit
	if !cur.Valid() {
		return out
	}
	type win struct{ k, w, x, y, psi float64 }
	var ws []win
	var cxx, cxy, cyy, cxp, cyp, cpp, dof float64
	for _, p := range ps {
		r := p.Rot
		if r == nil || r.N < 12 || r.S[0] <= 0 {
			continue
		}
		s := r.S
		sw := s[0]
		// stretches far from where the area is now say less about the arc here
		d := math.Hypot(p.Area.X-cur.X, p.Area.Y-cur.Y)
		k := math.Exp(-d * d / (2 * 10 * 10))
		if k < 0.05 {
			continue
		}
		// within each stretch: how the turning changes across the tablet
		cxx += k * (s[3] - s[1]*s[1]/sw)
		cxy += k * (s[4] - s[1]*s[2]/sw)
		cyy += k * (s[5] - s[2]*s[2]/sw)
		cxp += k * (s[7] - s[1]*s[6]/sw)
		cyp += k * (s[8] - s[2]*s[6]/sw)
		cpp += k * (s[9] - s[6]*s[6]/sw)
		dof += k * float64(r.N-3)
		ws = append(ws, win{k: k, w: sw, x: s[1] / sw, y: s[2] / sw, psi: s[6] / sw})
		out.N++
		out.Clicks += r.N
	}
	if out.N < 3 || dof < 30 {
		return out
	}
	det := cxx*cyy - cxy*cxy
	if det <= 0 {
		return out
	}
	gx := (cyy*cxp - cxy*cyp) / det
	gy := (cxx*cyp - cxy*cxp) / det
	s2 := math.Max(cpp-gx*cxp-gy*cyp, 0) / dof
	vxx, vyy, vxy := s2*cyy/det, s2*cxx/det, -s2*cxy/det
	// the turning at the centre of the area in use: every stretch, carried
	// there along the change across the tablet; how sure, from how much the
	// stretches differ (each one is a fresh look)
	var su, sm float64
	for _, w := range ws {
		u := w.k * w.w
		su += u
		sm += u * (w.psi + gx*(cur.X-w.x) + gy*(cur.Y-w.y))
	}
	psi := sm / su
	var sv float64
	for _, w := range ws {
		u := w.k * w.w
		d := w.psi + gx*(cur.X-w.x) + gy*(cur.Y-w.y) - psi
		sv += u * u * d * d
	}
	n := float64(len(ws))
	pse := math.Sqrt(sv*n/(n-1)) / su
	g := math.Hypot(gx, gy)
	se := math.Sqrt(math.Max(vxx+vyy, 0) / 2)
	sex := math.Sqrt(math.Max(vxx, 0))
	deg := 180 / math.Pi
	out.Mean, out.MeanSE = round(psi*deg, 2), round(pse*deg, 2)
	out.GX, out.GY, out.SE = round(gx*deg*10, 3), round(gy*deg*10, 3), round(se*deg*10, 3)
	if g > 0 {
		// with the pivot below the area, the turning grows to the right
		// (gx > 0); the direction of the change says where the pivot is
		dir := math.Atan2(gy, gx)
		dvar := (gy*gy*vxx + gx*gx*vyy - 2*gx*gy*vxy) / (g * g * g * g)
		out.Dir, out.DirSE = round(dir*deg, 1), round(math.Sqrt(math.Max(dvar, 0))*deg, 1)
	}
	out.Strong = math.Abs(gx) >= 3*sex && math.Abs(gx)*deg*10 >= 0.3
	if !out.Strong {
		out.Note = "the aim rotation doesn't change enough from one side of the area to the other to tell where it would come out straight"
		return out
	}
	// moving sideways by x changes the turning by gx·x
	out.Zero = round(-psi/gx, 2)
	if math.Abs(psi) < math.Max(2*pse, 0.4/deg) || math.Abs(out.Zero) < 1 {
		out.Note = "the aim is already straight with this area"
		return out
	}
	out.Shift = round(clamp(out.Zero, -arcStepMax, arcStepMax), 2)
	return out
}

// arcPlace is where the area goes sideways, from cur (width w): between
// where the aim comes out straight and where the clicks land centred (land,
// whose landing changes by b mm for each mm the area moves). Each costs what
// it does to the clicks: turned aim an error growing with the jump (about a
// quarter of the width), a hand used to another place an offset on every
// click. At most arcStepMax from cur.
func arcPlace(a *ArcFit, cur otd.Rect, land, b, w float64) float64 {
	zero := cur.X
	if a.Shift != 0 {
		zero = cur.X + a.Zero
	}
	g := math.Abs(a.GX) / 10 * math.Pi / 180 // radians per mm
	t := g * 0.25 * w
	t2, b2 := t*t, b*b
	x := zero
	if b2 > 0 {
		x = (b2*land + t2*zero) / (b2 + t2)
	}
	if a.Shift != 0 && math.Abs(x-zero) > 1.5 {
		side := "left"
		if land > zero {
			side = "right"
		}
		a.Note = "the hand is used to clicking further " + side + ": I move it towards where the aim comes out straight only as long as the clicks stay centred"
	}
	return clamp(x, cur.X-arcStepMax, cur.X+arcStepMax)
}

// How far the area slides for the arc: at most this much after one map, and
// this much from where it was before the diagnosis.
const (
	arcStepMax  = 6.0
	arcTotalMax = 25.0
)
