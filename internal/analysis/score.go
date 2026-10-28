package analysis

import (
	"math"
	"sort"

	"areacoach/internal/osu"
)

// How sure and how clean, put together. Sure: the best width known within
// about this share, and the place within this share of the width, counts
// as half sure. Clean: this many aim misses per 1000 notes, or paths bent
// this much, take the area to about 37% of clean.
const (
	scoreSpread = 0.05
	scoreMiss   = 150.0
	scoreLines  = 0.25
)

// adaptScore puts together how close the area found is to a perfect one:
// spread is how unsure its width is (log), placeRel how unsure its place
// is (share of the width; place in mm, for showing), miss the aim misses
// per 1000 notes expected with it (NaN: not known, the maps tell).
func adaptScore(ps []CalibPoint, spread, placeRel, place, miss float64) AdaptScore {
	var out AdaptScore
	if len(ps) == 0 {
		return out
	}
	s := math.Hypot(spread, placeRel)
	sure := 1 / (1 + (s/scoreSpread)*(s/scoreSpread))
	// little to go by yet: not sure, whatever the model says
	notes := 0
	for _, p := range ps {
		notes += p.Notes
	}
	sure *= 1 - math.Exp(-float64(notes)/1500)
	// the aim misses: those expected with the area, or else those of the
	// last maps
	if !finite(miss) {
		var m, n float64
		for i := len(ps) - 1; i >= 0 && n < 3000; i-- {
			m += ps[i].Miss / 1000 * float64(ps[i].Notes)
			n += float64(ps[i].Notes)
		}
		if n > 0 {
			miss = 1000 * m / n
		} else {
			miss = 0
		}
	}
	clean := math.Exp(-miss / scoreMiss)
	// the paths of the last maps
	var lw, ls float64
	for i := len(ps) - 1; i >= 0 && lw < 600; i-- {
		if ps[i].LineN > 0 {
			lw += float64(ps[i].LineN)
			ls += float64(ps[i].LineN) * ps[i].Line
		}
	}
	if lw >= 20 {
		out.Lines = round(ls/lw, 4)
		clean = math.Pow(clean, 0.8) * math.Pow(math.Exp(-out.Lines/scoreLines), 0.2)
	}
	out.Sure = round(100*sure, 1)
	out.Clean = round(100*clean, 1)
	out.Pct = round(100*sure*clean, 1)
	out.Spread = round(spread, 4)
	out.Place = round(place, 2)
	out.Miss = round(miss, 2)
	return out
}

// curveMin is the width at the bottom of a curve of expected misses, between
// its points.
func curveMin(c []MissPt) float64 {
	if len(c) == 0 {
		return math.NaN()
	}
	k := 0
	for i := range c {
		if c[i].Miss < c[k].Miss {
			k = i
		}
	}
	if k == 0 || k == len(c)-1 {
		return c[k].W
	}
	// a parabola through the lowest point and its neighbours
	x0, x1, x2 := c[k-1].W, c[k].W, c[k+1].W
	y0, y1, y2 := c[k-1].Miss, c[k].Miss, c[k+1].Miss
	den := (x0 - x1) * (x0 - x2) * (x1 - x2)
	if den == 0 {
		return x1
	}
	A := (x2*(y1-y0) + x1*(y0-y2) + x0*(y2-y1)) / den
	B := (x2*x2*(y0-y1) + x1*x1*(y2-y0) + x0*x0*(y1-y2)) / den
	if A <= 0 {
		return x1
	}
	return clamp(-B/(2*A), x0, x2)
}

// Reach is the widest area whose maps the hand can still play at their
// speed: beyond the speeds of the replays, unless the diagnosis measured the
// hand there. 0: no limit known.
func (m *MissModel) Reach(cal PrecCal) float64 {
	if !m.OK() || math.IsInf(m.maxW, 1) || m.maxW <= 0 {
		return 0
	}
	return math.Max(m.maxW, cal.Hi)
}

// PathLines measures how straight the cursor went from one circle to the
// next: for each jump between two clicked circles (at least 2.5 radii apart,
// within 700 ms of play), how far the path strayed from the straight line
// between the two clicks, as a share of the jump; the mean and the jumps
// counted. Distances are in millimetres of tablet (m maps osu! pixels there).
func PathLines(hits []Hit, frames []osu.Frame, m Mapper, rate float64) (float64, int) {
	if len(frames) < 10 || len(hits) < 2 {
		return 0, 0
	}
	fr := append([]osu.Frame(nil), frames...)
	sort.SliceStable(fr, func(i, j int) bool { return fr[i].T < fr[j].T })
	if rate <= 0 {
		rate = 1
	}
	j := 0
	at := func(t float64) (float64, float64, bool) {
		for j > 0 && fr[j].T > t {
			j--
		}
		for j+1 < len(fr) && fr[j+1].T <= t {
			j++
		}
		if j+1 >= len(fr) || fr[j].T > t {
			return 0, 0, false
		}
		a, b := fr[j], fr[j+1]
		u := 0.0
		if b.T > a.T {
			u = (t - a.T) / (b.T - a.T)
		}
		x, y := m.Point(float64(a.X)+u*float64(b.X-a.X), float64(a.Y)+u*float64(b.Y-a.Y))
		return x, y, true
	}
	var devs []float64
	for i := 1; i < len(hits); i++ {
		h0, h1 := &hits[i-1], &hits[i]
		if h0.Kind != 0 || h1.Kind > 1 || !h0.HasClick() || !h1.HasClick() || h1.Radius <= 0 {
			continue
		}
		t0 := float64(h0.Time) + float64(h0.CT)
		t1 := float64(h1.Time) + float64(h1.CT)
		if t1-t0 < 40*rate || t1-t0 > 700*rate {
			continue
		}
		x0, y0, ok0 := at(t0)
		x1, y1, ok1 := at(t1)
		if !ok0 || !ok1 {
			continue
		}
		dx, dy := x1-x0, y1-y0
		L := math.Hypot(dx, dy)
		rmm := float64(h1.Radius) * m.MMPerPx()
		if L < 2.5*rmm || L <= 0 {
			continue
		}
		ux, uy := dx/L, dy/L
		dev := 0.0
		for k := range fr {
			if fr[k].T <= t0 {
				continue
			}
			if fr[k].T >= t1 {
				break
			}
			x, y := m.Point(float64(fr[k].X), float64(fr[k].Y))
			// only the part between the two clicks, across the line
			along := (x-x0)*ux + (y-y0)*uy
			if along < 0 || along > L {
				continue
			}
			dev = math.Max(dev, math.Abs(-(x-x0)*uy+(y-y0)*ux))
		}
		devs = append(devs, math.Min(dev/L, 1))
	}
	if len(devs) == 0 {
		return 0, 0
	}
	// the mean without the few strangest (breaks in the pattern, slips)
	sort.Float64s(devs)
	n := len(devs) - len(devs)/20
	t := 0.0
	for _, d := range devs[:n] {
		t += d
	}
	return t / float64(n), len(devs)
}
