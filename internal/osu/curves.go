package osu

import (
	"math"
	"sort"
)

// sliderPath is a slider curve approximated as a polyline (relative to the
// slider head), following the game's approximation rules.
type sliderPath struct {
	pts      []Vec
	cum      []float64
	distance float64 // expected distance (pixel length) used by the game
}

func newSliderPath(curve byte, ctrl []Vec, length float64) *sliderPath {
	var pts []Vec
	switch curve {
	case 'L':
		pts = append(pts, ctrl...)
	case 'P':
		if len(ctrl) == 3 {
			if arc := circularArc(ctrl[0], ctrl[1], ctrl[2]); arc != nil {
				pts = arc
				break
			}
		}
		pts = bezierSegments(ctrl)
	case 'C':
		pts = catmull(ctrl)
	default:
		pts = bezierSegments(ctrl)
	}
	if len(pts) == 0 {
		pts = []Vec{{0, 0}}
	}
	p := &sliderPath{pts: pts}
	p.cum = make([]float64, len(pts))
	for i := 1; i < len(pts); i++ {
		p.cum[i] = p.cum[i-1] + pts[i].Dist(pts[i-1])
	}
	calc := p.cum[len(p.cum)-1]
	if length > 0 && !math.IsNaN(length) && !math.IsInf(length, 0) {
		p.distance = length
		// stable: no extension when the last two points are equal
		if len(pts) >= 2 && pts[len(pts)-1].Equal(pts[len(pts)-2]) && length > calc {
			p.distance = calc
		}
	} else {
		p.distance = calc
	}
	return p
}

// positionAt returns the point at arc length d, extending or trimming the
// last segment as the game does.
func (p *sliderPath) positionAt(d float64) Vec {
	n := len(p.pts)
	if n == 1 || d <= 0 {
		return p.pts[0]
	}
	total := p.cum[n-1]
	if d >= total {
		// extend along last non-degenerate segment
		for i := n - 1; i > 0; i-- {
			seg := p.pts[i].Sub(p.pts[i-1])
			l := seg.Len()
			if l > 1e-9 {
				return p.pts[i-1].Add(seg.Mul((d - p.cum[i-1]) / l))
			}
		}
		return p.pts[n-1]
	}
	i := sort.SearchFloat64s(p.cum, d)
	if i <= 0 {
		return p.pts[0]
	}
	segLen := p.cum[i] - p.cum[i-1]
	if segLen <= 0 {
		return p.pts[i]
	}
	return p.pts[i-1].Lerp(p.pts[i], (d-p.cum[i-1])/segLen)
}

func bezierSegments(ctrl []Vec) []Vec {
	var out []Vec
	start := 0
	for i := 1; i <= len(ctrl); i++ {
		if i == len(ctrl) || (ctrl[i].Equal(ctrl[i-1]) && i-start > 0) {
			seg := ctrl[start:i]
			if len(seg) >= 1 {
				pts := bezier(seg)
				if len(out) > 0 && len(pts) > 0 && out[len(out)-1].Equal(pts[0]) {
					pts = pts[1:]
				}
				out = append(out, pts...)
			}
			start = i
		}
	}
	return out
}

func bezier(c []Vec) []Vec {
	if len(c) <= 2 {
		return append([]Vec(nil), c...)
	}
	poly := 0.0
	for i := 1; i < len(c); i++ {
		poly += c[i].Dist(c[i-1])
	}
	steps := int(math.Ceil(poly / 1.5))
	if steps < 12 {
		steps = 12
	}
	if steps > 600 {
		steps = 600
	}
	out := make([]Vec, 0, steps+1)
	tmp := make([]Vec, len(c))
	for s := 0; s <= steps; s++ {
		t := float64(s) / float64(steps)
		copy(tmp, c)
		for k := len(c) - 1; k > 0; k-- {
			for j := 0; j < k; j++ {
				tmp[j] = tmp[j].Lerp(tmp[j+1], t)
			}
		}
		out = append(out, tmp[0])
	}
	return out
}

func circularArc(a, b, c Vec) []Vec {
	if math.Abs((b.Y-a.Y)*(c.X-a.X)-(b.X-a.X)*(c.Y-a.Y)) < 1e-3 {
		return nil
	}
	d := 2 * (a.X*(b.Y-c.Y) + b.X*(c.Y-a.Y) + c.X*(a.Y-b.Y))
	if math.Abs(d) < 1e-9 {
		return nil
	}
	aSq := a.X*a.X + a.Y*a.Y
	bSq := b.X*b.X + b.Y*b.Y
	cSq := c.X*c.X + c.Y*c.Y
	centre := Vec{
		(aSq*(b.Y-c.Y) + bSq*(c.Y-a.Y) + cSq*(a.Y-b.Y)) / d,
		(aSq*(c.X-b.X) + bSq*(a.X-c.X) + cSq*(b.X-a.X)) / d,
	}
	dA := a.Sub(centre)
	dC := c.Sub(centre)
	r := dA.Len()
	thetaStart := math.Atan2(dA.Y, dA.X)
	thetaEnd := math.Atan2(dC.Y, dC.X)
	for thetaEnd < thetaStart {
		thetaEnd += 2 * math.Pi
	}
	dir := 1.0
	thetaRange := thetaEnd - thetaStart
	ortho := Vec{c.Sub(a).Y, -c.Sub(a).X}
	if ortho.Dot(b.Sub(a)) < 0 {
		dir = -1
		thetaRange = 2*math.Pi - thetaRange
	}
	amount := 2
	if 2*r > 0.1 {
		amount = int(math.Ceil(thetaRange / (2 * math.Acos(1-0.1/r))))
		if amount < 2 {
			amount = 2
		}
		if amount > 2000 {
			amount = 2000
		}
	}
	out := make([]Vec, amount)
	for i := 0; i < amount; i++ {
		f := float64(i) / float64(amount-1)
		th := thetaStart + dir*f*thetaRange
		out[i] = Vec{centre.X + math.Cos(th)*r, centre.Y + math.Sin(th)*r}
	}
	return out
}

func catmull(c []Vec) []Vec {
	if len(c) < 2 {
		return append([]Vec(nil), c...)
	}
	const detail = 50
	var out []Vec
	for i := 0; i < len(c)-1; i++ {
		v1 := c[i]
		if i > 0 {
			v1 = c[i-1]
		}
		v2 := c[i]
		var v3, v4 Vec
		if i < len(c)-1 {
			v3 = c[i+1]
		} else {
			v3 = v2.Add(v2).Sub(v1)
		}
		if i < len(c)-2 {
			v4 = c[i+2]
		} else {
			v4 = v3.Add(v3).Sub(v2)
		}
		for k := 0; k < detail; k++ {
			out = append(out, catmullPoint(v1, v2, v3, v4, float64(k)/detail))
		}
	}
	out = append(out, c[len(c)-1])
	return out
}

func catmullPoint(v1, v2, v3, v4 Vec, t float64) Vec {
	t2 := t * t
	t3 := t2 * t
	f := func(a, b, c, d float64) float64 {
		return 0.5 * (2*b + (-a+c)*t + (2*a-5*b+4*c-d)*t2 + (-a+3*b-3*c+d)*t3)
	}
	return Vec{f(v1.X, v2.X, v3.X, v4.X), f(v1.Y, v2.Y, v3.Y, v4.Y)}
}
