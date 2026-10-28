package engine

import (
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"areacoach/internal/analysis"
	"areacoach/internal/filter"
	"areacoach/internal/osu"
	"areacoach/internal/otd"
	"areacoach/internal/store"
)

// FilterGroup is the real performance with one filter configuration.
type FilterGroup struct {
	Signature string  `json:"signature"`
	Label     string  `json:"label"`
	Plays     int     `json:"plays"`
	Notes     int     `json:"notes"`
	MissPer1k float64 `json:"missPer1k"`
	NormErr   float64 `json:"normErr"`
	Current   bool    `json:"current"`
}

// FilterState is what the filter page shows.
type FilterState struct {
	Running  bool              `json:"running"`
	Progress float64           `json:"progress"`
	Stage    string            `json:"stage"`
	Error    string            `json:"error"`
	Result   *filter.Result    `json:"result"`
	Updated  time.Time         `json:"updated"`
	Stale    bool              `json:"stale"` // new replays since the study
	Rate     float64           `json:"rate"`
	RateAuto bool              `json:"rateAuto"`
	Active   *otd.RadialFollow `json:"active"` // Radial Follow enabled now
	Others   []string          `json:"others"` // other enabled filters
	Real     []FilterGroup     `json:"real"`
	Filtered bool              `json:"filtered"` // the replays used were already smoothed
}

const (
	filterMaxPlays   = 220
	filterWindowMs   = 220.0
	filterMaxWindows = 14000
)

// defaultRate guesses the report rate of the tablet.
func defaultRate(name string) float64 {
	l := strings.ToLower(name)
	switch {
	case strings.Contains(l, "ctl-4") || strings.Contains(l, "ctl-6") || strings.Contains(l, "cth-") || strings.Contains(l, "ctl-") && strings.Contains(l, "wacom"):
		return 133
	case strings.Contains(l, "wacom"):
		return 200
	case strings.Contains(l, "xp-pen") || strings.Contains(l, "xp pen") || strings.Contains(l, "huion") || strings.Contains(l, "gaomon") || strings.Contains(l, "veikk"):
		return 266
	}
	return 200
}

func (e *Engine) filterRate() (float64, bool) {
	st := e.st.State()
	if st.Settings.FilterRate > 0 {
		return st.Settings.FilterRate, false
	}
	return defaultRate(e.Env().Setup.TabletName), true
}

// FilterStatus returns the state of the filter study.
func (e *Engine) FilterStatus() FilterState {
	e.fmu.Lock()
	fs := e.fstate
	key := e.fkey
	e.fmu.Unlock()
	fs.Rate, fs.RateAuto = e.filterRate()
	env := e.Env()
	fs.Active, fs.Others = nil, nil
	if env.Profile != nil {
		if r, ok := env.Profile.Radial(); ok {
			fs.Active = &r
		}
		for _, f := range env.Profile.Filters {
			if f.Enabled && !strings.Contains(strings.ToLower(f.Path), "radialfollow") {
				fs.Others = append(fs.Others, f.Name)
			}
		}
	}
	fs.Real = e.filterGroups()
	if fs.Result != nil && key != e.filterKey() {
		fs.Stale = true
	}
	return fs
}

func (e *Engine) filterKey() string {
	plays := e.st.Plays()
	rate, _ := e.filterRate()
	if len(plays) == 0 {
		return fmt.Sprintf("0|%g", rate)
	}
	return fmt.Sprintf("%d|%d|%g", len(plays), plays[0].ID, rate)
}

// StartFilterStudy runs the simulation in the background.
func (e *Engine) StartFilterStudy() {
	e.fmu.Lock()
	if e.fstate.Running {
		e.fmu.Unlock()
		return
	}
	e.fstate.Running, e.fstate.Progress, e.fstate.Error, e.fstate.Stage = true, 0, "", "Reading the replays"
	e.fmu.Unlock()
	e.emit("filter")
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("filter study error: %v", r)
				e.fmu.Lock()
				e.fstate.Running, e.fstate.Error = false, fmt.Sprint(r)
				e.fmu.Unlock()
				e.emit("filter")
			}
		}()
		key := e.filterKey()
		rate, _ := e.filterRate()
		ds, filtered, err := e.filterDataset(rate, func(p float64) {
			e.fmu.Lock()
			e.fstate.Progress = p * 0.35
			e.fmu.Unlock()
		})
		if err != nil {
			e.fmu.Lock()
			e.fstate.Running, e.fstate.Error = false, err.Error()
			e.fmu.Unlock()
			e.emit("filter")
			return
		}
		e.fmu.Lock()
		e.fstate.Stage = "Trying the filter settings on your movements"
		e.fmu.Unlock()
		var cur *filter.Params
		if env := e.Env(); env.Profile != nil {
			if r, ok := env.Profile.Radial(); ok && r.Tablet {
				cur = &filter.Params{Outer: r.Outer, Inner: r.Inner, Coef: r.Coef, Knee: r.Knee, Leak: r.Leak}
			}
		}
		last := time.Now()
		res := filter.Optimize(ds, cur, func(p float64) {
			e.fmu.Lock()
			e.fstate.Progress = 0.35 + 0.65*p
			e.fmu.Unlock()
			if time.Since(last) > 700*time.Millisecond {
				last = time.Now()
				e.emit("filter")
			}
		})
		e.fmu.Lock()
		e.fstate = FilterState{Result: &res, Updated: time.Now(), Filtered: filtered}
		e.fkey = key
		e.fmu.Unlock()
		e.emit("filter")
		log.Printf("filter study: %d notes, %d plays, best %s", res.Notes, res.Plays, res.Best)
	}()
}

// filterDataset rebuilds the pen path before every click from the replay
// files, in tablet millimetres with the area of each play.
func (e *Engine) filterDataset(rate float64, progress func(float64)) (*filter.Dataset, bool, error) {
	plays, _, _ := e.PlaysForAnalysis()
	metas := map[int]string{}
	for _, m := range e.st.Plays() {
		metas[m.ID] = m.ReplayPath
	}
	var use []*analysis.PlayData
	for _, p := range plays { // newest first
		if !p.Setup.Valid() {
			continue
		}
		if path := metas[p.ID]; path != "" {
			if _, err := os.Stat(path); err == nil {
				use = append(use, p)
			}
		}
		if len(use) >= filterMaxPlays {
			break
		}
	}
	if len(use) < 6 {
		return nil, false, fmt.Errorf("at least 6 replays of the active profile still in the osu! folder are needed (I find %d)", len(use))
	}
	ds := &filter.Dataset{Rate: rate}
	areas := map[string]bool{}
	filtered := false
	for n, p := range use {
		if progress != nil {
			progress(float64(n) / float64(len(use)))
		}
		if p.Setup.Smoothing != "" {
			filtered = true
		}
		ws := playWindows(p, metas[p.ID], rate)
		if len(ws) == 0 {
			continue
		}
		areas[fmt.Sprintf("%.2f|%.2f|%.2f|%.2f", p.Setup.Area.W, p.Setup.Area.H, p.Setup.Area.X, p.Setup.Area.Y)] = true
		ds.Windows = append(ds.Windows, ws...)
		ds.Plays++
	}
	ds.Areas = len(areas)
	// keep the data manageable: an even subsample of the clicks
	if len(ds.Windows) > filterMaxWindows {
		step := float64(len(ds.Windows)) / filterMaxWindows
		out := make([]filter.Window, 0, filterMaxWindows)
		for i := 0; i < filterMaxWindows; i++ {
			out = append(out, ds.Windows[int(float64(i)*step)])
		}
		ds.Windows = out
	}
	if len(ds.Windows) < 500 {
		return nil, filtered, fmt.Errorf("too few usable clicks (%d): play a few more maps", len(ds.Windows))
	}
	return ds, filtered, nil
}

// filterGroups summarises real performance per filter configuration.
func (e *Engine) filterGroups() []FilterGroup {
	st := e.st.State()
	epochs := st.Epochs
	sig := map[int]string{}
	for _, ep := range epochs {
		sig[ep.ID] = ep.Setup.Smoothing
	}
	curSig := ""
	if env := e.Env(); env.Profile != nil {
		curSig = env.Profile.SmoothingSignature()
	}
	type acc struct {
		plays, notes, miss int
		err                float64
		errN               int
	}
	groups := map[string]*acc{}
	for _, p := range e.st.Plays() {
		if p.Status != "ok" || p.Mixed || st.PlayProfile(p) != st.ActiveProfile {
			continue
		}
		s := sig[store.EpochAt(epochs, p.Time)]
		a := groups[s]
		if a == nil {
			a = &acc{}
			groups[s] = a
		}
		a.plays++
		a.notes += p.Summary.Heads
		a.miss += p.Summary.AimMiss
		if p.NormErr > 0 {
			a.err += p.NormErr
			a.errN++
		}
	}
	var out []FilterGroup
	for s, a := range groups {
		g := FilterGroup{Signature: s, Label: smoothingLabel(s), Plays: a.plays, Notes: a.notes, Current: s == curSig}
		if a.notes > 0 {
			g.MissPer1k = math.Round(10000*float64(a.miss)/float64(a.notes)) / 10
		}
		if a.errN > 0 {
			g.NormErr = math.Round(10000*a.err/float64(a.errN)) / 10000
		}
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Plays > out[j].Plays })
	return out
}

func smoothingLabel(s string) string {
	if s == "" {
		return "No filter"
	}
	s = strings.ReplaceAll(s, "RadialFollowSmoothingTabletSpace", "Radial Follow (tablet)")
	s = strings.ReplaceAll(s, "RadialFollowSmoothingScreenSpace", "Radial Follow (screen)")
	s = strings.ReplaceAll(s, "OuterRadius", "outer")
	s = strings.ReplaceAll(s, "InnerRadius", "inner")
	s = strings.ReplaceAll(s, "SmoothingCoefficient", "coef")
	s = strings.ReplaceAll(s, "SoftKneeScale", "knee")
	s = strings.ReplaceAll(s, "SmoothingLeakCoefficient", "leak")
	s = strings.ReplaceAll(s, ")(", ") (")
	return strings.ReplaceAll(s, ",", ", ")
}

// playWindows rebuilds the pen path before every click of one play, in
// tablet millimetres, sampled at the tablet report rate.
func playWindows(p *analysis.PlayData, path string, rate float64) []filter.Window {
	area := p.Setup.Area
	return playWindowsBy(p, path, rate, func(float64, float64) (otd.Rect, bool) { return area, true })
}

// playWindowsBy is playWindows when the area changed during the play:
// areaFor gives the area between two map times, if it stayed the same.
func playWindowsBy(p *analysis.PlayData, path string, rate float64, areaFor func(t0, t1 float64) (otd.Rect, bool)) []filter.Window {
	rep, err := osu.ReadReplay(path)
	if err != nil || len(rep.Frames) < 10 {
		return nil
	}
	step := 1000 / rate
	k := int(math.Round(filterWindowMs / step))
	fr := rep.Frames
	sort.SliceStable(fr, func(i, j int) bool { return fr[i].T < fr[j].T })
	mappers := map[otd.Rect]analysis.Mapper{}
	mapperOf := func(a otd.Rect) analysis.Mapper {
		if m, ok := mappers[a]; ok {
			return m
		}
		su := p.Setup
		su.Area = a
		m := su.Mapper()
		mappers[a] = m
		return m
	}
	gameRate := p.Rate
	if gameRate <= 0 {
		gameRate = 1
	}
	// position at a map time, linear between frames
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
		return float64(a.X) + u*float64(b.X-a.X), float64(a.Y) + u*float64(b.Y-a.Y), true
	}
	var out []filter.Window
	for i := range p.Hits {
		h := &p.Hits[i]
		if !h.HasClick() || !h.HasOrigin() {
			continue
		}
		press := float64(h.Time) + float64(h.CT)
		area, same := areaFor(press-float64(k)*step*gameRate, press)
		if !same {
			continue
		}
		m := mapperOf(area)
		w := filter.Window{Play: p.ID, R: float32(float64(h.Radius) * m.MMPerPx())}
		ok := true
		for s := k; s >= 0; s-- {
			t := press - float64(s)*step*gameRate
			x, y, good := at(t)
			if !good {
				ok = false
				break
			}
			tx, ty := m.Point(x, y)
			w.X = append(w.X, float32(tx))
			w.Y = append(w.Y, float32(ty))
		}
		if !ok || len(w.X) < 6 {
			continue
		}
		tx, ty := m.Point(float64(h.TX), float64(h.TY))
		w.TX, w.TY = float32(tx), float32(ty)
		ox, oy := m.Point(float64(h.OX), float64(h.OY))
		dx, dy := tx-ox, ty-oy
		if d := math.Hypot(dx, dy); d > 0.5 {
			w.DirX, w.DirY = float32(dx/d), float32(dy/d)
		} else {
			w.DirX = 1
		}
		out = append(out, w)
	}
	return out
}
