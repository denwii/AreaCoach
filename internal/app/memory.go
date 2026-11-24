package app

import (
	"errors"
	"log"
	"math"
	"time"

	"areacoach/internal/aim"
	"areacoach/internal/calib"
	"areacoach/internal/geom"
	"areacoach/internal/otd"
)

// The memory of the calibration: how you play at each width, per tablet
// and shape of area, kept across days (see package calib).

// memKey names the memory for an area.
func (a *App) memKey(r otd.Rect) string {
	a.mu.Lock()
	tablet := a.tabletLocked()
	a.mu.Unlock()
	if r.W <= 0 {
		return ""
	}
	return calib.Key(tablet, r.H/r.W)
}

// remember adds clicks of the play src, each with the area it was played
// with and how much it counts (nil: fully).
func (a *App) remember(t time.Time, src string, cs []aim.Click, moves []aim.Move, areas []otd.Rect, weights []float64) {
	byKey := map[string][]calib.Click{}
	for i, c := range cs {
		if !areas[i].Valid() {
			continue
		}
		var m aim.Move
		if i < len(moves) {
			m = moves[i]
		}
		if x, ok := calib.FromAim(c, m, areas[i].W); ok {
			if weights != nil {
				x.Weight = weights[i]
			}
			k := a.memKey(areas[i])
			byKey[k] = append(byKey[k], x)
		}
	}
	for k, list := range byKey {
		a.mem.Add(k, t, src, list)
	}
}

// memReport is what the memory says for the area r.
func (a *App) memReport(r otd.Rect) calib.Report {
	return a.mem.Analyze(a.memKey(r), time.Now())
}

// saveMemory writes the memory (at most every half minute unless now).
func (a *App) saveMemory(now bool) {
	a.mu.Lock()
	due := now || time.Since(a.memSaved) > 30*time.Second
	if due {
		a.memSaved = time.Now()
	}
	a.mu.Unlock()
	if due {
		if err := a.mem.Save(); err != nil {
			log.Printf("calibration memory: %v", err)
		}
	}
}

// Forget drops the memory of the tablet and shape in use.
func (a *App) Forget() {
	if r, ok := a.current(); ok {
		a.mem.Forget(a.memKey(r))
		a.saveMemory(true)
		log.Printf("calibration memory forgotten")
	}
}

// BestArea is the area that went best so far, for the area in use: the
// width where you missed the least (where several widths are about as good,
// the one in use), where the area went best on the tablet, and how sure
// that is (0-1).
func (a *App) BestArea() (otd.Rect, float64, bool) {
	cur, ok := a.current()
	if !ok {
		return otd.Rect{}, 0, false
	}
	m := a.memReport(cur)
	if !m.HasBest {
		return otd.Rect{}, m.Reliability, false
	}
	w := m.Best
	if m.HasBand && cur.W >= m.BandLo && cur.W <= m.BandHi && !m.ClearlyBetter(cur.W) {
		w = cur.W
	}
	lim := a.limitsFor(cur)
	if lim.MinW > 0 {
		w = math.Max(w, lim.MinW)
	}
	if lim.MaxW > 0 {
		w = math.Min(w, lim.MaxW)
	}
	r := otd.Rect{W: round2(w), X: cur.X, Y: cur.Y, Rot: cur.Rot}
	r.H = math.Round(r.W*lim.Ratio*1000) / 1000
	if m.HasPos {
		r.X, r.Y = round2(m.PosX), round2(m.PosY)
	}
	if lim.TabletW > 0 && lim.TabletH > 0 {
		r = geom.ClampToTablet(r, lim.TabletW, lim.TabletH)
		r.X, r.Y = round2(r.X), round2(r.Y)
	}
	return r, m.Reliability, true
}

// UseBest puts the best area in OpenTabletDriver (stopping the live
// calibration first, so that it doesn't move it again).
func (a *App) UseBest() error {
	r, _, ok := a.BestArea()
	if !ok {
		return errors.New("there's no best area yet: play a few maps with Live on")
	}
	a.SetLive(false)
	return a.SetArea(r)
}

// adaptation follows how used to the area the hand is: after a change, the
// first clicks count less (see calib.AdaptWeight).
type adaptation struct {
	area  otd.Rect
	known bool
	since int
	need  float64
}

// weight is how much the next click, played with the area r, counts; and
// whether the area had just changed.
func (ad *adaptation) weight(r otd.Rect) (float64, bool) {
	changed := false
	if !ad.known {
		ad.area, ad.known = r, true
	} else if !r.Same(ad.area) {
		ch := math.Abs(r.W-ad.area.W) + math.Abs(r.X-ad.area.X) + math.Abs(r.Y-ad.area.Y)
		ad.area, ad.since, ad.need, changed = r, 0, calib.AdaptNeed(ch), true
	}
	w := calib.AdaptWeight(ad.since, ad.need)
	ad.since++
	return w, changed
}

// lastChange is the last change of the area before t: from where, and when.
func (a *App) lastChange(t time.Time) (otd.Rect, otd.Rect, time.Time, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := len(a.marks) - 1; i > 0; i-- {
		if !a.marks[i].At.After(t) {
			return a.marks[i-1].Area, a.marks[i].Area, a.marks[i].At, true
		}
	}
	return otd.Rect{}, otd.Rect{}, time.Time{}, false
}

// dataFactor is how fine the changes get as the memory grows (rough first,
// then finer, never stopping).
func dataFactor(r calib.Report) float64 {
	return math.Max(0.45, 1/math.Sqrt(1+r.Clicks/3000))
}
