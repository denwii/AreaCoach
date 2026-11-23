package app

import (
	"log"
	"math"
	"time"

	"areacoach/internal/aim"
	"areacoach/internal/calib"
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
// with.
func (a *App) remember(t time.Time, src string, cs []aim.Click, moves []aim.Move, areas []otd.Rect) {
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

// dataFactor is how fine the changes get as the memory grows (rough first,
// then finer, never stopping).
func dataFactor(r calib.Report) float64 {
	return math.Max(0.45, 1/math.Sqrt(1+r.Clicks/3000))
}
