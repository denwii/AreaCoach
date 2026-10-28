package engine

import (
	"errors"
	"log"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"areacoach/internal/analysis"
	"areacoach/internal/filter"
	"areacoach/internal/otd"
	"areacoach/internal/store"
)

// The filter that goes with the area: AbstractQbit's Radial Follow, tried
// on the pen movements recorded by the diagnosis with areas close to the
// one found (its radii are in millimetres of tablet, so the right values
// change with the size of the area). It is worked out again after the
// maps, never while one is played.

type adaptFilterRun struct {
	mu      sync.Mutex
	running bool
	started time.Time // the session it was worked out for
	n       int       // plays measured then
	w       float64   // width of the area found then
}

// filterAreaBands are how far from the area found the movements used may
// have been recorded (share of the width), tried in turn until there are
// enough clicks.
var filterAreaBands = []float64{0.12, 0.25, math.Inf(1)}

const adaptFilterMinClicks = 600

// adaptFilterKick starts working out the filter, if there is something new.
func (e *Engine) adaptFilterKick() {
	st := e.st.State()
	c := st.Calib
	if c == nil || c.Mode != diagMode {
		return
	}
	ls := &e.ad
	ls.mu.Lock()
	target := c.Cur
	if ls.run != nil && ls.runKey.Equal(c.Started) && ls.run.Fit.Target.Valid() {
		target = ls.run.Fit.Target
	}
	ls.mu.Unlock()
	if !target.Valid() {
		return
	}
	n := 0
	for _, m := range c.Maps {
		if m.Windows > 0 {
			n++
		}
	}
	if n == 0 {
		return
	}
	f := &e.adf
	f.mu.Lock()
	// again when there are new maps or the area found moved
	same := f.started.Equal(c.Started) && f.n == n && math.Abs(math.Log(target.W/math.Max(f.w, 1))) < 0.05
	if f.running || same {
		f.mu.Unlock()
		return
	}
	f.running, f.started, f.n, f.w = true, c.Started, n, target.W
	f.mu.Unlock()
	// a copy of what it reads, as the session goes on meanwhile
	var snap store.CalibSession
	e.st.Update(func(s *store.State) {
		if s.Calib != nil {
			snap = *s.Calib
			snap.Maps = append([]store.AdaptMap(nil), s.Calib.Maps...)
		}
	})
	e.emit("calib")
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("diagnosis: error working out the filter: %v", r)
			}
			f.mu.Lock()
			f.running = false
			f.mu.Unlock()
			e.emit("calib")
		}()
		e.adaptFilterStudy(&snap, target)
	}()
}

// adaptFilterBusy tells whether the filter is being worked out now.
func (e *Engine) adaptFilterBusy() bool {
	e.adf.mu.Lock()
	defer e.adf.mu.Unlock()
	return e.adf.running
}

func (e *Engine) adaptFilterStudy(c *store.CalibSession, target otd.Rect) {
	rate, _ := e.filterRate()
	var ds *filter.Dataset
	filtered := false
	for _, band := range filterAreaBands {
		d, fl := e.adaptFilterData(c, target, rate, band)
		if d != nil && len(d.Windows) >= adaptFilterMinClicks {
			ds, filtered = d, fl
			break
		}
	}
	// first the kinds and strengths the guided filter moves along, and the
	// settings played (quick): on the movements of the diagnosis recorded
	// without a filter, or, while those are too few (the guided filter asks
	// for a map without one only now and then), on the replays of the
	// profile played without one
	var played []filter.Params
	seen := map[filter.Params]bool{}
	for i := len(c.Maps) - 1; i >= 0 && len(played) < 40; i-- {
		if m := c.Maps[i]; m.Filter == "on" && m.FParams != nil && !seen[*m.FParams] {
			seen[*m.FParams] = true
			played = append(played, *m.FParams)
		}
	}
	famDS, src := ds, "diagnosis"
	if ds == nil || filtered {
		famDS, src = nil, "replay"
		if old := c.FFam; old == nil || old.Src != "replay" || time.Since(old.When) > 30*time.Minute {
			if d, err := e.profileRawDataset(rate); err == nil {
				famDS = d
			}
		}
	}
	if famDS != nil {
		famDS.Workers = 1
		fam := filter.StudyFamily(famDS, played, 4000)
		if fam.OK() {
			fam.Src, fam.When = src, time.Now()
			e.st.Update(func(s *store.State) {
				if s.Calib != nil && s.Calib.Started.Equal(c.Started) {
					s.Calib.FFam = &fam
				}
			})
			_ = e.st.SaveState()
			e.emit("calib")
			log.Printf("diagnosis: filter simulated at %d strengths and %d shapes on %d clicks of %s (%d settings played)", len(fam.Outers), len(fam.Shapes), fam.N,
				map[bool]string{true: "diagnosis maps", false: "replays without a filter"}[src == "diagnosis"], len(played))
		}
	}
	if ds == nil {
		return
	}
	// one core only: the next map may be played meanwhile
	ds.Workers = 1
	var cur *filter.Params
	if env := e.Env(); env.Profile != nil {
		if r, ok := env.Profile.Radial(); ok && r.Tablet {
			cur = &filter.Params{Outer: r.Outer, Inner: r.Inner, Coef: r.Coef, Knee: r.Knee, Leak: r.Leak}
		}
	}
	res := filter.Optimize(ds, cur, nil)
	if res.Stopped {
		e.adf.mu.Lock()
		e.adf.n = -1 // not done: again after the map
		e.adf.mu.Unlock()
		return
	}
	af := &store.AdaptFilter{At: time.Now(), Area: target, Windows: len(ds.Windows), Plays: ds.Plays, Best: res.Best,
		MissRaw: res.Diagnosis.MissRaw, Filtered: filtered, Options: res.Options, Tries: res.Tries, Current: res.Current, Rate: rate}
	for _, o := range res.Options {
		if o.Key == res.Best {
			af.Params, af.Gain, af.GainLo, af.GainHi, af.Delay = o.Eval.Params, o.Eval.Gain, o.Eval.GainLo, o.Eval.GainHi, o.Eval.Delay
		}
	}
	e.st.Update(func(s *store.State) {
		if s.Calib == nil || !s.Calib.Started.Equal(c.Started) {
			return
		}
		s.Calib.Filter = af
		if s.CalibResult != nil && s.CalibResult.Mode == "adapt" {
			s.CalibResult.Filter = af
		}
	})
	_ = e.st.SaveState()
	if af.Best == "none" {
		log.Printf("diagnosis: filter for %.1f×%.1f mm: none (%d clicks, %d plays)", target.W, target.H, af.Windows, af.Plays)
	} else {
		p := af.Params
		log.Printf("diagnosis: filter for %.1f×%.1f mm: Radial Follow outer %.2f inner %.2f coef %.2f knee %.2f leak %.2f (+%.1f circles per 1000, %.1f ms; %d clicks)",
			target.W, target.H, p.Outer, p.Inner, p.Coef, p.Knee, p.Leak, af.Gain, af.Delay, af.Windows)
	}
}

// profileRawDataset is the pen paths before the clicks of the replays of
// the profile played without a smoothing filter (the newest ones).
func (e *Engine) profileRawDataset(rate float64) (*filter.Dataset, error) {
	plays, _, _ := e.PlaysForAnalysis()
	metas := map[int]string{}
	for _, m := range e.st.Plays() {
		metas[m.ID] = m.ReplayPath
	}
	ds := &filter.Dataset{Rate: rate}
	areas := map[otd.Rect]bool{}
	for _, p := range plays { // newest first
		if ds.Plays >= 60 || len(ds.Windows) >= filterMaxWindows {
			break
		}
		if !p.Setup.Valid() || p.Setup.Smoothing != "" {
			continue
		}
		path := metas[p.ID]
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			continue
		}
		ws := playWindows(p, path, rate)
		if len(ws) == 0 {
			continue
		}
		areas[p.Setup.Area] = true
		ds.Windows = append(ds.Windows, ws...)
		ds.Plays++
	}
	ds.Areas = len(areas)
	if len(ds.Windows) < adaptFilterMinClicks {
		return nil, errors.New("too few clicks without a filter")
	}
	return ds, nil
}

// adaptFilterData gathers the pen movements before every click of the
// diagnosis maps played with an area within band (share of the width) of
// target. Movements recorded with a smoothing filter on are used only when
// there are not enough others.
func (e *Engine) adaptFilterData(c *store.CalibSession, target otd.Rect, rate, band float64) (*filter.Dataset, bool) {
	st := e.st.State()
	env := e.Env()
	near := func(a otd.Rect) bool { return math.Abs(math.Log(a.W/target.W)) <= band }
	var raw, smooth []filter.Window
	rawPlays, smoothPlays := map[int]bool{}, map[int]bool{}
	used := 0
	for i := len(c.Maps) - 1; i >= 0 && used < 80; i-- {
		m := c.Maps[i]
		if m.Windows == 0 || !near(m.Area) || filterOn(m.Filter) || m.Filter == "mixed" {
			continue
		}
		meta, ok := e.st.Play(m.PlayID)
		if !ok || meta.ReplayPath == "" {
			continue
		}
		if _, err := os.Stat(meta.ReplayPath); err != nil {
			continue
		}
		hits, err := e.st.Hits(m.PlayID)
		if err != nil {
			continue
		}
		pd := &analysis.PlayData{ID: m.PlayID, Time: meta.Time, Rate: meta.Rate, Hits: hits, Duration: meta.LengthMs}
		pd.Setup = env.Setup
		for _, x := range st.Epochs {
			if x.ID == store.EpochAt(st.Epochs, pd.Time) {
				pd.Setup = x.Setup
			}
		}
		// played with a filter: as read from OpenTabletDriver then, or (not
		// known) as its settings file had it
		smoothed := m.Filter != "off" && pd.Setup.Smoothing != ""
		pd.Setup.Area = m.Area
		ws := playWindows(pd, meta.ReplayPath, rate)
		if len(ws) == 0 {
			continue
		}
		used++
		if smoothed {
			smooth = append(smooth, ws...)
			smoothPlays[pd.ID] = true
		} else {
			raw = append(raw, ws...)
			rawPlays[pd.ID] = true
		}
	}
	ds := &filter.Dataset{Rate: rate, Windows: raw, Plays: len(rawPlays), Areas: 1}
	filtered := false
	if len(raw) < adaptFilterMinClicks && len(raw)+len(smooth) >= adaptFilterMinClicks {
		ds.Windows = append(raw, smooth...)
		ds.Plays += len(smoothPlays)
		filtered = len(smooth) > len(raw)
	}
	if len(ds.Windows) > filterMaxWindows {
		step := float64(len(ds.Windows)) / filterMaxWindows
		out := make([]filter.Window, 0, filterMaxWindows)
		for i := 0; i < filterMaxWindows; i++ {
			out = append(out, ds.Windows[int(float64(i)*step)])
		}
		ds.Windows = out
	}
	return ds, filtered
}

// radialPlugin tells whether the profile has a Radial Follow filter (on or
// off), and the one on now, if any.
func radialPlugin(p *otd.Profile) (bool, *otd.RadialFollow) {
	if p == nil {
		return false, nil
	}
	has := false
	for _, f := range p.Filters {
		if strings.Contains(strings.ToLower(f.Path), "radialfollow") {
			has = true
		}
	}
	if r, ok := p.Radial(); ok {
		return has, &r
	}
	return has, nil
}
