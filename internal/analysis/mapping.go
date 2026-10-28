// Package analysis turns replays into aim measurements on the tablet and
// derives area recommendations, consistency models and coaching.
package analysis

import (
	"math"

	"areacoach/internal/otd"
)

// Setup describes everything needed to map osu!pixels to tablet millimetres.
type Setup struct {
	TabletName string   `json:"tabletName"`
	TabletW    float64  `json:"tabletW"`
	TabletH    float64  `json:"tabletH"`
	Area       otd.Rect `json:"area"`
	Display    otd.Rect `json:"display"`
	GameW      float64  `json:"gameW"`
	GameH      float64  `json:"gameH"`
	WinX       float64  `json:"winX"`
	WinY       float64  `json:"winY"`
	StretchX   float64  `json:"stretchX"`
	StretchY   float64  `json:"stretchY"`
	// enabled smoothing filters of OpenTabletDriver ("" if none); a change
	// starts a new period, so their effect can be measured
	Smoothing    string `json:"smoothing,omitempty"`
	SmoothingSet bool   `json:"smoothingSet,omitempty"`
}

// Valid reports whether the setup can be used for mm conversions.
func (s Setup) Valid() bool {
	return s.Area.Valid() && s.Display.Valid() && s.GameH > 100 && s.GameW > 100
}

// Mapper is the affine map osu!px -> tablet mm: p_mm = A·p + B.
type Mapper struct {
	A  [2][2]float64
	B  [2]float64
	ok bool
}

// Mapper builds the transform from the setup.
func (s Setup) Mapper() Mapper {
	if !s.Valid() {
		return Mapper{}
	}
	sx, sy := s.StretchX, s.StretchY
	if sx <= 0 {
		sx = 1
	}
	if sy <= 0 {
		sy = 1
	}
	k := s.GameH / 480
	pfX0 := (s.GameW - 512*k) / 2
	pfY0 := s.GameH/2 - 184*k
	// osu -> display: d = W + (pf0 + p*k) * stretch
	dax, dbx := k*sx, s.WinX+pfX0*sx
	day, dby := k*sy, s.WinY+pfY0*sy
	// display -> local (mm, unrotated)
	fx := s.Area.W / s.Display.W
	fy := s.Area.H / s.Display.H
	lax, lbx := dax*fx, (dbx-s.Display.X)*fx
	lay, lby := day*fy, (dby-s.Display.Y)*fy
	th := s.Area.Rot * math.Pi / 180
	c, si := math.Cos(th), math.Sin(th)
	var m Mapper
	m.A = [2][2]float64{{c * lax, -si * lay}, {si * lax, c * lay}}
	m.B = [2]float64{s.Area.X + c*lbx - si*lby, s.Area.Y + si*lbx + c*lby}
	m.ok = true
	return m
}

// OK reports whether the mapper is usable.
func (m Mapper) OK() bool { return m.ok }

// Point maps an osu!pixel position to tablet mm.
func (m Mapper) Point(x, y float64) (float64, float64) {
	return m.A[0][0]*x + m.A[0][1]*y + m.B[0], m.A[1][0]*x + m.A[1][1]*y + m.B[1]
}

// Vec maps an osu!pixel displacement to tablet mm.
func (m Mapper) Vec(x, y float64) (float64, float64) {
	return m.A[0][0]*x + m.A[0][1]*y, m.A[1][0]*x + m.A[1][1]*y
}

// MMPerPx is the average scale (mm per osu!pixel).
func (m Mapper) MMPerPx() float64 {
	det := m.A[0][0]*m.A[1][1] - m.A[0][1]*m.A[1][0]
	return math.Sqrt(math.Abs(det))
}

// PlayfieldCorners returns the playfield corners on the tablet (mm).
func (m Mapper) PlayfieldCorners() [4][2]float64 {
	var out [4][2]float64
	pts := [4][2]float64{{0, 0}, {512, 0}, {512, 384}, {0, 384}}
	for i, p := range pts {
		out[i][0], out[i][1] = m.Point(p[0], p[1])
	}
	return out
}

// AreaCorners returns the corners of an OTD area on the tablet.
func AreaCorners(r otd.Rect) [4][2]float64 {
	th := r.Rot * math.Pi / 180
	c, s := math.Cos(th), math.Sin(th)
	hw, hh := r.W/2, r.H/2
	loc := [4][2]float64{{-hw, -hh}, {hw, -hh}, {hw, hh}, {-hw, hh}}
	var out [4][2]float64
	for i, p := range loc {
		out[i][0] = r.X + c*p[0] - s*p[1]
		out[i][1] = r.Y + s*p[0] + c*p[1]
	}
	return out
}

// ScreenVec is a movement on the tablet (mm, along the tablet's axes) as
// seen on the screen, for an area rotated by rot degrees.
func ScreenVec(dx, dy, rot float64) (float64, float64) {
	t := rot * math.Pi / 180
	c, s := math.Cos(t), math.Sin(t)
	return c*dx + s*dy, -s*dx + c*dy
}

// tabletSpan is how much of the tablet an area w×h turned by rot degrees
// takes, along the tablet's x and y.
func tabletSpan(w, h, rot float64) (float64, float64) {
	t := rot * math.Pi / 180
	c, s := math.Abs(math.Cos(t)), math.Abs(math.Sin(t))
	return w*c + h*s, w*s + h*c
}

// MaxWidth is the widest area turned by rot degrees, with height ratio ×
// width, that fits a tablet tw × th (ratio 0: the height left aside).
func MaxWidth(tw, th, ratio, rot float64) float64 {
	t := rot * math.Pi / 180
	c, s := math.Abs(math.Cos(t)), math.Abs(math.Sin(t))
	w := math.Inf(1)
	if d := c + ratio*s; d > 1e-9 {
		w = math.Min(w, tw/d)
	}
	if d := s + ratio*c; d > 1e-9 {
		w = math.Min(w, th/d)
	}
	return w
}

// maxHeight is the tallest area of width w turned by rot degrees that fits
// a tablet tw × th.
func maxHeight(tw, th, w, rot float64) float64 {
	t := rot * math.Pi / 180
	c, s := math.Abs(math.Cos(t)), math.Abs(math.Sin(t))
	h := math.Inf(1)
	if s > 1e-9 {
		h = math.Min(h, (tw-w*c)/s)
	}
	if c > 1e-9 {
		h = math.Min(h, (th-w*s)/c)
	}
	return h
}

// FitsTablet reports whether an area lies within the tablet bounds.
func FitsTablet(r otd.Rect, tw, th float64) bool {
	if tw <= 0 || th <= 0 {
		return true
	}
	for _, c := range AreaCorners(r) {
		if c[0] < -0.01 || c[1] < -0.01 || c[0] > tw+0.01 || c[1] > th+0.01 {
			return false
		}
	}
	return true
}

// ClampToTablet shifts an area so that it fits inside the tablet when possible.
func ClampToTablet(r otd.Rect, tw, th float64) otd.Rect {
	if tw <= 0 || th <= 0 {
		return r
	}
	cs := AreaCorners(r)
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, c := range cs {
		minX = math.Min(minX, c[0])
		maxX = math.Max(maxX, c[0])
		minY = math.Min(minY, c[1])
		maxY = math.Max(maxY, c[1])
	}
	if maxX-minX <= tw {
		if minX < 0 {
			r.X -= minX
		} else if maxX > tw {
			r.X -= maxX - tw
		}
	}
	if maxY-minY <= th {
		if minY < 0 {
			r.Y -= minY
		} else if maxY > th {
			r.Y -= maxY - th
		}
	}
	return r
}

// ScreenToOsu maps a cursor position on screen (pixels) to osu!pixels: the
// inverse of the way osu! draws the playfield in its window.
func (s Setup) ScreenToOsu(x, y float64) (float64, float64) {
	sx, sy := s.StretchX, s.StretchY
	if sx <= 0 {
		sx = 1
	}
	if sy <= 0 {
		sy = 1
	}
	k := s.GameH / 480
	if k <= 0 {
		return math.NaN(), math.NaN()
	}
	pfX0 := (s.GameW - 512*k) / 2
	pfY0 := s.GameH/2 - 184*k
	return ((x-s.WinX)/sx - pfX0) / k, ((y-s.WinY)/sy - pfY0) / k
}

// OsuToScreen is the inverse of ScreenToOsu.
func (s Setup) OsuToScreen(x, y float64) (float64, float64) {
	sx, sy := s.StretchX, s.StretchY
	if sx <= 0 {
		sx = 1
	}
	if sy <= 0 {
		sy = 1
	}
	k := s.GameH / 480
	pfX0 := (s.GameW - 512*k) / 2
	pfY0 := s.GameH/2 - 184*k
	return s.WinX + (pfX0+x*k)*sx, s.WinY + (pfY0+y*k)*sy
}
