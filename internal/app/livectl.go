package app

import (
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"

	"areacoach/internal/aim"
	"areacoach/internal/calib"
	"areacoach/internal/geom"
	"areacoach/internal/live"
	"areacoach/internal/osu"
	"areacoach/internal/otd"
	"areacoach/internal/platform"
)

// LiveStep is one change made while playing.
type LiveStep struct {
	At     time.Time `json:"at"`
	Map    string    `json:"map"`
	From   otd.Rect  `json:"from"`
	To     otd.Rect  `json:"to"`
	Clicks int       `json:"clicks"` // the clicks looked at
	Misses int       `json:"misses"` // of them, outside the circle
	Why    string    `json:"why"`
}

// LiveState is what the live section shows.
type LiveState struct {
	On      bool        `json:"on"`
	Phase   string      `json:"phase"` // off, noosu, menu, nomap, syncing, playing
	Map     string      `json:"map"`
	Lazer   bool        `json:"lazer"`
	Session live.Status `json:"session"`
	Start   *otd.Rect   `json:"start,omitempty"` // the area when it was turned on
	Steps   []LiveStep  `json:"steps"`
	Keys    []string    `json:"keys"`
	Error   string      `json:"error,omitempty"`
	// the stretch being played: clicks counted so far (the first ones after
	// a change count less) and clicks it needs before the next change
	BlockDone float64 `json:"blockDone"`
	BlockNeed float64 `json:"blockNeed"`
	Trying    bool    `json:"trying"` // the stretch is at a nearby width, to measure it
}

type liveCtl struct {
	a      *App
	mu     sync.Mutex
	spans  [][2]time.Time // when it was on (the last ones)
	st     LiveState
	stop   chan struct{}
	done   chan struct{}
	newSet bool
	// the osu! window and the keyboard (the system's; tests play instead)
	window func() (platform.Window, bool)
	input  live.Input
}

func newLiveCtl(a *App) *liveCtl {
	return &liveCtl{a: a, st: LiveState{Phase: "off"}, window: platform.OsuWindow, input: sysInput{}}
}

// The live calibration plays in stretches: after a change it waits until
// enough clicks are played with the new area (the first ones count less,
// while the hand gets used to it), then, at the end of the map or in a
// pause, it decides the next change. poolSize clicks are kept for that.
const poolSize = 300

// blockClicks is how many clicks a stretch needs, by how big the changes
// are meant to be.
func blockClicks(strength string) float64 {
	if blockOverride > 0 {
		return blockOverride
	}
	switch strength {
	case "gentle":
		return 200
	case "strong":
		return 100
	}
	return 150
}

var (
	blockOverride float64  // tests: shorter stretches
	breakGap      = 1500.0 // ms without presses that make a pause in a map
)

func (l *liveCtl) state() LiveState {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.st
	s.Steps = append([]LiveStep{}, l.st.Steps...)
	return s
}

func (l *liveCtl) settingsChanged() {
	l.mu.Lock()
	l.newSet = true
	l.mu.Unlock()
}

func (l *liveCtl) setOn(on bool) {
	l.mu.Lock()
	if on == l.st.On {
		l.mu.Unlock()
		return
	}
	l.st.On = on
	if on {
		if r, ok := l.a.current(); ok {
			l.st.Start = &r
		}
		l.st.Steps, l.st.Error, l.st.Phase = nil, "", "noosu"
		l.spans = append(l.spans, [2]time.Time{time.Now(), {}})
		if len(l.spans) > 20 {
			l.spans = l.spans[len(l.spans)-20:]
		}
		l.stop, l.done = make(chan struct{}), make(chan struct{})
		go l.run(l.stop, l.done)
		l.mu.Unlock()
		log.Printf("live: on")
		return
	}
	stop, done := l.stop, l.done
	l.st.Phase = "off"
	if n := len(l.spans); n > 0 {
		l.spans[n-1][1] = time.Now()
	}
	l.mu.Unlock()
	close(stop)
	<-done
	l.set(func(s *LiveState) { s.BlockDone, s.Trying = 0, false })
	log.Printf("live: off")
}

func (l *liveCtl) set(f func(*LiveState)) {
	l.mu.Lock()
	f(&l.st)
	l.mu.Unlock()
}

// sysInput is the real keyboard and cursor.
type sysInput struct{}

func (sysInput) Cursor() (int, int)  { return platform.CursorPos() }
func (sysInput) KeyDown(vk int) bool { return platform.KeyDown(vk) }
func (sysInput) FineTimer(on bool)   { platform.FineTimer(on) }

// keys are the keys (and buttons) that click in osu!.
func (l *liveCtl) keys(lazer bool) ([]int, []string) {
	a := l.a
	a.mu.Lock()
	s, g := a.set, a.env.Game
	a.mu.Unlock()
	var names []string
	switch {
	case s.Keys != "":
		names = strings.Fields(s.Keys)
	case !lazer && g != nil:
		for _, k := range []string{g.KeyLeft, g.KeyRight} {
			if k != "" {
				names = append(names, k)
			}
		}
	}
	if len(names) == 0 {
		names = []string{"Z", "X"}
	}
	pen := s.PenTip == "on" || s.PenTip == "auto" && (lazer || g == nil || !g.MouseButtonsOff)
	if pen {
		names = append(names, "MouseLeft", "MouseRight")
	}
	var vks []int
	var used []string
	for _, n := range names {
		if vk, ok := platform.VirtualKey(n); ok {
			vks = append(vks, vk)
			used = append(used, n)
		}
	}
	return vks, used
}

// mapsByTitle finds the maps named in the window title.
func (a *App) mapsByTitle(title string, lazer bool) []*osu.Beatmap {
	a.mu.Lock()
	ix, lf := a.index, a.lazer
	a.mu.Unlock()
	var paths []string
	if lazer && lf != nil {
		paths = lf.FindByTitle(title)
	}
	if len(paths) == 0 && ix != nil {
		if paths = ix.FindByTitle(title); len(paths) == 0 {
			_ = ix.Refresh()
			paths = ix.FindByTitle(title)
		}
		if len(paths) == 0 && a.scanning.CompareAndSwap(false, true) {
			// a map just downloaded is in osu!.db only once osu! closes:
			// the Songs folder is read meanwhile (the next look finds it)
			go func() {
				defer a.scanning.Store(false)
				ix.ScanIfOlder(30 * time.Second)
			}()
		}
	}
	if len(paths) == 0 && !lazer && lf != nil {
		paths = lf.FindByTitle(title)
	}
	var out []*osu.Beatmap
	for i, p := range paths {
		if i >= 4 {
			break
		}
		if bm, err := osu.ParseBeatmapFile(p); err == nil {
			out = append(out, bm)
		}
	}
	return out
}

func (l *liveCtl) run(stop, done chan struct{}) {
	defer close(done)
	a := l.a
	var smp *live.Sampler
	var ses *live.Session
	// across maps: the last clicks ready for the fit, how used to the area
	// the hand is, the stretch being played
	var pool []aim.Sample
	var adapt adaptation
	var plan calib.Plan
	var preProbe otd.Rect
	block := 0.0
	lastPress := 0.0
	seen := map[[2]float64]bool{}
	stepped := false
	var nomapAt time.Time
	playID, title := "", ""
	need := func() float64 { return blockClicks(a.Settings().Strength) }
	showBlock := func() {
		l.set(func(s *LiveState) { s.BlockDone, s.BlockNeed, s.Trying = block, need(), plan.WasProbe })
	}
	// decide the next change, if the stretch is complete
	try := func() {
		if block < need() || len(pool) < aim.MinSamples {
			return
		}
		cur, ok := a.current()
		if !ok {
			return
		}
		if l.step(pool, cur, &plan, &preProbe, title) {
			stepped = true
		}
		block = 0
		showBlock()
	}
	finish := func(change bool) {
		if change {
			try() // between maps: the moment to change the area
		}
		if smp != nil {
			smp.Stop()
			smp = nil
		}
		if stepped {
			// what was found while playing stays when OpenTabletDriver restarts
			if r, ok := a.current(); ok {
				a.mu.Lock()
				tablet, path := a.tabletLocked(), a.env.OTDPath
				a.mu.Unlock()
				if err := saveArea(path, tablet, r); err != nil {
					log.Printf("live: saving the area: %v", err)
				}
			}
		}
		ses, stepped = nil, false
		seen = map[[2]float64]bool{}
	}
	end := func() { finish(true) }
	defer func() {
		// turned off in the middle of a trial width: back to the area before
		if plan.WasProbe && preProbe.Valid() {
			if cur, ok := a.current(); ok && !cur.Same(preProbe) {
				a.mu.Lock()
				tablet := a.tabletLocked()
				a.mu.Unlock()
				if err := setLiveArea(tablet, preProbe); err == nil {
					a.mu.Lock()
					a.noteAreaLocked(preProbe, "live")
					a.mu.Unlock()
					stepped = true
					log.Printf("live: off while trying %.2f mm: back to %.2f x %.3f", cur.W, preProbe.W, preProbe.H)
				}
			}
		}
		finish(false) // turned off: the area stays as it is (saved)
	}()
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	idle := 0
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		if idle > 0 {
			idle-- // osu! isn't open: looked for once a second
			continue
		}
		w, ok := l.window()
		if !ok {
			end()
			idle = 4
			l.set(func(s *LiveState) { s.Phase, s.Map = "noosu", "" })
			continue
		}
		mt := platform.MapTitle(w.Title)
		if mt == "" {
			end()
			l.set(func(s *LiveState) { s.Phase, s.Map, s.Lazer = "menu", "", w.Lazer })
			continue
		}
		l.mu.Lock()
		reset := l.newSet
		l.newSet = false
		l.mu.Unlock()
		retry := ses != nil && ses.Title == mt && !ses.Playable() && time.Since(nomapAt) > 5*time.Second
		if ses == nil || ses.Title != mt || reset && smp != nil || retry {
			end()
			title = mt
			ses = live.NewSession(title, a.mapsByTitle(title, w.Lazer), w.Lazer)
			nomapAt = time.Now()
			playID = title + "@" + nomapAt.Format(time.RFC3339Nano)
			if !ses.Playable() {
				l.set(func(s *LiveState) {
					s.Phase, s.Map, s.Lazer, s.Session = "nomap", title, w.Lazer, live.Status{Title: title}
				})
				continue
			}
			vks, names := l.keys(w.Lazer)
			smp = live.StartSampler(l.input, vks)
			l.set(func(s *LiveState) { s.Keys = names })
			log.Printf("live: %s (%s), keys %v", title, map[bool]string{false: "stable", true: "lazer"}[w.Lazer], names)
		}
		if !ses.Playable() || smp == nil {
			continue
		}
		cur, ok := a.current()
		if !ok {
			l.set(func(s *LiveState) { s.Error = "I can't read the area from OpenTabletDriver" })
			continue
		}
		a.mu.Lock()
		env := a.env
		a.mu.Unlock()
		su, toOsu := env.liveSetup(w, cur)
		for _, p := range smp.Drain() {
			x, y := toOsu(p.X, p.Y)
			ses.Add(live.Press{At: p.At, X: x, Y: y, Area: cur})
			lastPress = p.At
		}
		now := smp.Now()
		ses.Update(now)
		st := ses.Status(now)
		phase := "syncing"
		if st.Synced {
			phase = "playing"
		}
		l.set(func(s *LiveState) { s.Phase, s.Map, s.Lazer, s.Session = phase, title, w.Lazer, st })
		if !st.Synced {
			continue
		}
		var newCs []aim.Click
		var newMoves []aim.Move
		var newAreas []otd.Rect
		var newW []float64
		for _, c := range ses.Clicks(now) {
			k := [2]float64{c.T, c.PressT}
			if seen[k] {
				continue
			}
			seen[k] = true
			wt, changed := adapt.weight(c.Area)
			if changed {
				block = 0 // the area changed (here or by hand): a new stretch
			}
			block += wt
			newCs, newMoves, newAreas, newW = append(newCs, c.Click), append(newMoves, c.Move), append(newAreas, c.Area), append(newW, wt)
			if wt < 0.5 {
				continue // still getting used to the area: not for the fit
			}
			area := c.Area
			pool = append(pool, aim.Samples([]aim.Click{c.Click}, su, func(int) otd.Rect { return area })...)
		}
		a.remember(time.Now(), playID, newCs, newMoves, newAreas, newW)
		if len(pool) > poolSize {
			pool = pool[len(pool)-poolSize:]
		}
		showBlock()
		// a long map: the change comes in a pause, after a double stretch
		if block >= 2*need() && now-lastPress >= breakGap {
			try()
		}
	}
}

// step decides the next area after a stretch: towards the one your clicks
// fit, with what the memory of the widths knows (or a stretch at a nearby
// width, to measure it); nothing when nothing asks clearly for a change.
func (l *liveCtl) step(pool []aim.Sample, cur otd.Rect, plan *calib.Plan, pre *otd.Rect, title string) bool {
	a := l.a
	set := a.Settings()
	lim := a.limitsFor(cur)
	base := cur
	back := plan.WasProbe && pre.Valid()
	if back {
		base = *pre // after a trial width, from where it was
	}
	res, ok := aim.Fit(pool, lim, base, true)
	if !ok {
		return false
	}
	mem := a.memReport(base)
	p := *plan
	p.Explore, p.Min, p.Max = set.TryWidths, lim.MinW, lim.MaxW
	dec := mem.Next(base.W, res.Area.W, res.SE[0], p)
	w := dec.Width
	if lim.MinW > 0 {
		w = math.Max(w, lim.MinW)
	}
	if lim.MaxW > 0 {
		w = math.Min(w, lim.MaxW)
	}
	// where the area goes for that width
	at := func(width float64) otd.Rect {
		fixed := lim
		fixed.MinW, fixed.MaxW = width, width
		if r, ok := aim.Fit(pool, fixed, base, false); ok {
			return r.Area
		}
		r := res.Area
		r.W, r.H = width, width*lim.Ratio
		return r
	}
	target := at(w)
	var next otd.Rect
	if dec.Probe != 0 {
		next = target
		next.W = round2(next.W)
		next.H = math.Round(next.W*lim.Ratio*1000) / 1000
		next.X, next.Y = round2(next.X), round2(next.Y)
		*pre = base
		plan.WasProbe, plan.LastSide = true, dec.Probe
	} else {
		plan.WasProbe = false
		// the position moves only when your clicks ask for it clearly
		if math.Abs(target.X-base.X) < math.Max(0.1, res.SE[1]) {
			target.X = base.X
		}
		if math.Abs(target.Y-base.Y) < math.Max(0.1, res.SE[2]) {
			target.Y = base.Y
		}
		if dec.Hold {
			target.W = base.W
		}
		next, _ = sculpt(base, target, lim, set.Strength, dec.Factor, dataFactor(mem))
	}
	// where the best area sits on the tablet, for the best area shown
	if mem.HasBest {
		b := at(mem.Best)
		a.mem.SetPos(a.memKey(base), b.X, b.Y)
	}
	why := dec.Why
	if back {
		why = fmt.Sprintf("back from trying %.1f mm; %s", cur.W, why)
	}
	if next.Same(cur) {
		log.Printf("live: stays at %.2f x %.3f at %.2f, %.2f (%s)", cur.W, cur.H, cur.X, cur.Y, why)
		return false
	}
	a.mu.Lock()
	tablet := a.tabletLocked()
	a.mu.Unlock()
	if err := setLiveArea(tablet, next); err != nil {
		l.set(func(s *LiveState) { s.Error = "I can't change the area: " + err.Error() })
		return false
	}
	a.mu.Lock()
	a.noteAreaLocked(next, "live")
	a.mu.Unlock()
	if set.Sound {
		switch {
		case next.W > cur.W+0.005:
			platform.PlayWav(live.SoundGrow)
		case next.W < cur.W-0.005:
			platform.PlayWav(live.SoundShrink)
		default:
			platform.PlayWav(live.SoundMove)
		}
	}
	misses := 0
	for _, x := range pool {
		if math.Hypot(x.Err[0], x.Err[1]) > x.Radius {
			misses++
		}
	}
	stepLog := LiveStep{At: time.Now(), Map: title, From: cur, To: next, Clicks: len(pool), Misses: misses, Why: why}
	l.set(func(s *LiveState) {
		s.Error = ""
		s.Steps = append(s.Steps, stepLog)
		if len(s.Steps) > 200 {
			s.Steps = s.Steps[len(s.Steps)-200:]
		}
	})
	log.Printf("live: %.2f x %.3f at %.2f, %.2f -> %.2f x %.3f at %.2f, %.2f (clicks ask %.2f ± %.2f: %s)",
		cur.W, cur.H, cur.X, cur.Y, next.W, next.H, next.X, next.Y, res.Area.W, res.SE[0], why)
	return true
}

// sculpt is the next area after a stretch: part of the way from cur to fit,
// by at most a little (wf and pf scale the step of the width and of the
// position), and only if it changes something.
func sculpt(cur, fit otd.Rect, lim aim.Limits, strength string, wf, pf float64) (otd.Rect, bool) {
	frac, maxW, maxXY := 0.6, 0.015, 0.8
	switch strength {
	case "gentle":
		frac, maxW, maxXY = 0.4, 0.008, 0.5
	case "strong":
		frac, maxW, maxXY = 0.8, 0.025, 1.2
	}
	maxW *= cur.W
	lim1 := func(v, m float64) float64 { return math.Max(-m, math.Min(m, v)) }
	dw := lim1(frac*wf*(fit.W-cur.W), maxW*wf)
	dx := lim1(frac*pf*(fit.X-cur.X), maxXY*pf)
	dy := lim1(frac*pf*(fit.Y-cur.Y), maxXY*pf)
	ratio := lim.Ratio
	if ratio <= 0 {
		ratio = cur.H / cur.W
	}
	if math.Abs(dw) < 0.05 && math.Abs(dx) < 0.05 && math.Abs(dy) < 0.05 && math.Abs(cur.H-cur.W*ratio) < 0.01 {
		return cur, false
	}
	w := round2(cur.W + dw)
	next := otd.Rect{W: w, H: math.Round(w*ratio*1000) / 1000, X: round2(cur.X + dx), Y: round2(cur.Y + dy), Rot: cur.Rot}
	if lim.TabletW > 0 && lim.TabletH > 0 {
		next = geom.ClampToTablet(next, lim.TabletW, lim.TabletH)
		next.X, next.Y = round2(next.X), round2(next.Y)
	}
	return next, !next.Same(cur)
}

// wasOn tells whether the live section was on at some moment between from
// and to (its clicks are already in the memory).
func (l *liveCtl) wasOn(from, to time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, s := range l.spans {
		end := s[1]
		if end.IsZero() {
			end = time.Now()
		}
		if !s[0].After(to) && !end.Add(time.Minute).Before(from) {
			return true
		}
	}
	return false
}

// SetLive turns the live section on or off.
func (a *App) SetLive(on bool) { a.live.setOn(on) }

// LiveBack puts back the area there was when the live section was turned on.
func (a *App) LiveBack() error {
	st := a.live.state()
	if st.Start == nil {
		return nil
	}
	return a.SetArea(*st.Start)
}
