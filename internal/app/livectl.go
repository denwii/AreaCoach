package app

import (
	"log"
	"math"
	"strings"
	"sync"
	"time"

	"areacoach/internal/aim"
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

// sculpting: every stepClicks clicks (and at least stepGap apart) the area
// moves part of the way towards the one that fits the last fitClicks
// clicks, by at most a little.
const (
	stepClicks = 30
	fitClicks  = 150
)

var stepGap = 8 * time.Second

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
	var lastStep time.Time
	// the last clicks, across maps, ready for the fit
	var pool []aim.Sample
	var poolHits []bool
	fresh := 0
	seen := map[[2]float64]bool{}
	stepped := false
	var nomapAt time.Time
	playID := ""
	end := func() {
		if smp != nil {
			smp.Stop()
			smp = nil
		}
		if ses != nil && stepped {
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
	defer end()
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
		title := platform.MapTitle(w.Title)
		if title == "" {
			end()
			l.set(func(s *LiveState) { s.Phase, s.Map, s.Lazer = "menu", "", w.Lazer })
			continue
		}
		l.mu.Lock()
		reset := l.newSet
		l.newSet = false
		l.mu.Unlock()
		retry := ses != nil && ses.Title == title && !ses.Playable() && time.Since(nomapAt) > 5*time.Second
		if ses == nil || ses.Title != title || reset && smp != nil || retry {
			end()
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
		for _, c := range ses.Clicks(now) {
			k := [2]float64{c.T, c.PressT}
			if seen[k] {
				continue
			}
			seen[k] = true
			newCs, newMoves, newAreas = append(newCs, c.Click), append(newMoves, c.Move), append(newAreas, c.Area)
			area := c.Area
			for _, x := range aim.Samples([]aim.Click{c.Click}, su, func(int) otd.Rect { return area }) {
				pool = append(pool, x)
				poolHits = append(poolHits, c.Hit)
				fresh++
			}
		}
		a.remember(time.Now(), playID, newCs, newMoves, newAreas)
		if len(pool) > fitClicks {
			pool, poolHits = pool[len(pool)-fitClicks:], poolHits[len(poolHits)-fitClicks:]
		}
		if fresh < stepClicks || time.Since(lastStep) < stepGap {
			continue
		}
		misses := 0
		for _, h := range poolHits[max(0, len(poolHits)-fresh):] {
			if !h {
				misses++
			}
		}
		n := min(fresh, len(pool))
		fresh, lastStep = 0, time.Now()
		if l.step(pool, n, misses, cur, title) {
			stepped = true
		}
	}
}

// step moves the area a little towards the one that fits the last clicks
// (n of them new since the last step, misses of those outside the circle).
func (l *liveCtl) step(samples []aim.Sample, n, misses int, cur otd.Rect, title string) bool {
	a := l.a
	lim := a.limitsFor(cur)
	res, ok := aim.Fit(samples, lim, cur, false)
	if !ok {
		return false
	}
	// what the memory of the widths says about the width the clicks ask for
	mem := a.memReport(cur)
	dec := mem.Decide(cur.W, res.Area.W)
	w := dec.Width
	if lim.MinW > 0 {
		w = math.Max(w, lim.MinW)
	}
	if lim.MaxW > 0 {
		w = math.Min(w, lim.MaxW)
	}
	target := res.Area
	if math.Abs(w-res.Area.W) > 0.005 {
		// the best place for that width
		fixed := lim
		fixed.MinW, fixed.MaxW = w, w
		if r2, ok := aim.Fit(samples, fixed, cur, false); ok {
			target = r2.Area
		}
	}
	next, changed := sculpt(cur, target, lim, a.Settings().Strength, dec.Factor, dataFactor(mem))
	if !changed {
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
	sound := a.set.Sound
	a.mu.Unlock()
	if sound {
		switch {
		case next.W > cur.W+0.005:
			platform.PlayWav(live.SoundGrow)
		case next.W < cur.W-0.005:
			platform.PlayWav(live.SoundShrink)
		default:
			platform.PlayWav(live.SoundMove)
		}
	}
	stepLog := LiveStep{At: time.Now(), Map: title, From: cur, To: next, Clicks: n, Misses: misses, Why: dec.Why}
	l.set(func(s *LiveState) {
		s.Error = ""
		s.Steps = append(s.Steps, stepLog)
		if len(s.Steps) > 200 {
			s.Steps = s.Steps[len(s.Steps)-200:]
		}
	})
	log.Printf("live: %.2f x %.3f at %.2f, %.2f -> %.2f x %.3f at %.2f, %.2f (clicks ask %.2f, towards %.2f: %s)",
		cur.W, cur.H, cur.X, cur.Y, next.W, next.H, next.X, next.Y, res.Area.W, target.W, dec.Why)
	return true
}

// sculpt is the next area: part of the way from cur to fit, by at most a
// little (wf and pf scale the step of the width and of the position), and
// only if it changes something.
func sculpt(cur, fit otd.Rect, lim aim.Limits, strength string, wf, pf float64) (otd.Rect, bool) {
	frac, k := 0.4, 1.0
	switch strength {
	case "gentle":
		frac, k = 0.25, 0.6
	case "strong":
		frac, k = 0.6, 1.6
	}
	maxW, maxXY := 0.012*cur.W*k, 0.6*k
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
