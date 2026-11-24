// Package app ties the parts together: it follows the area in
// OpenTabletDriver, the replays osu! saves and the live play.
package app

import (
	"errors"
	"log"
	"math"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"areacoach/internal/aim"
	"areacoach/internal/calib"
	"areacoach/internal/osu"
	"areacoach/internal/otd"
)

// App is the program.
type App struct {
	dir     string
	version string

	mu      sync.Mutex
	set     Settings
	env     Env
	envAt   time.Time
	area    otd.Rect
	areaOK  bool
	areaErr string
	marks   []areaMark // the areas in use, oldest first
	prev    *otd.Rect  // the area before the last one set here (to go back)
	note    string     // what did not work when the area was last set

	index    *osu.Index
	lazer    *osu.LazerFiles
	indexFor string
	lazerFor string

	started   time.Time
	done      map[string]int // replays looked at (tries)
	report    *Report
	reports   int
	busy      bool
	replayMsg string
	rDir      string // the listing of osu! stable's replays
	rDirTime  time.Time
	rList     []replayFile
	rListed   time.Time

	live     *liveCtl
	scanning atomic.Bool // the Songs folder is being read
	mem      *calib.Store
	memSaved time.Time
	// the reliability seen lately (see relDrop)
	relKey    string
	relHigh   float64
	relDownAt time.Time

	stop  chan struct{}
	kick  chan struct{}
	ended chan struct{}
}

// New prepares the program; dir keeps its settings and caches.
func New(dir, version string) *App {
	a := &App{dir: dir, version: version, set: loadSettings(dir), started: time.Now(), done: map[string]int{},
		stop: make(chan struct{}), kick: make(chan struct{}, 1), ended: make(chan struct{})}
	a.live = newLiveCtl(a)
	a.mem = calib.Open(filepath.Join(dir, "calibration.json"))
	return a
}

// Run starts the work in the background.
func (a *App) Run() {
	go a.loop()
}

// Close stops everything.
func (a *App) Close() {
	a.live.setOn(false)
	close(a.stop)
	<-a.ended
	a.saveMemory(true)
}

func (a *App) wake() {
	select {
	case a.kick <- struct{}{}:
	default:
	}
}

func (a *App) loop() {
	defer close(a.ended)
	a.refreshEnv()
	a.pollArea()
	a.openIndexes()
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-a.stop:
			return
		case <-t.C:
		case <-a.kick:
		}
		a.mu.Lock()
		old := time.Since(a.envAt) > time.Minute
		a.mu.Unlock()
		if old {
			a.refreshEnv()
			a.openIndexes()
		}
		a.pollArea()
		a.scanLazer(300 * time.Millisecond)
		a.checkReplays()
		a.saveMemory(false)
	}
}

func (a *App) refreshEnv() {
	a.mu.Lock()
	s := a.set
	a.mu.Unlock()
	e := detect(s)
	a.mu.Lock()
	a.env, a.envAt = e, time.Now()
	a.mu.Unlock()
}

// openIndexes prepares the lists of maps (osu!.db and the Songs folder for
// stable, the files store for osu!lazer).
func (a *App) openIndexes() {
	a.mu.Lock()
	e := a.env
	ix, lf := a.index, a.lazer
	ixFor, lfFor := a.indexFor, a.lazerFor
	a.mu.Unlock()
	if e.OsuDir != "" && (ix == nil || ixFor != e.OsuDir) {
		ix = osu.NewIndex(e.OsuDir, e.SongsDir, filepath.Join(a.dir, "songs-index-v3.json"))
		if err := ix.Refresh(); err != nil {
			log.Printf("osu!.db: %v", err)
		}
		a.mu.Lock()
		a.index, a.indexFor = ix, e.OsuDir
		a.mu.Unlock()
	} else if ix != nil {
		_ = ix.Refresh()
	}
	if e.LazerData != "" && (lf == nil || lfFor != e.LazerData) {
		lf = osu.NewLazerFiles(e.LazerData, filepath.Join(a.dir, "lazer-files-v3.json"))
		a.mu.Lock()
		a.lazer, a.lazerFor = lf, e.LazerData
		a.mu.Unlock()
	}
}

func (a *App) scanLazer(budget time.Duration) {
	a.mu.Lock()
	lf := a.lazer
	a.mu.Unlock()
	if lf != nil {
		lf.Scan(budget)
	}
}

// pollArea reads the area in use and notes when it changes.
func (a *App) pollArea() {
	a.mu.Lock()
	tablet, path := a.tabletLocked(), a.env.OTDPath
	a.mu.Unlock()
	r, err := readArea(tablet, path)
	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil {
		a.areaErr = err.Error()
		return
	}
	a.areaErr = ""
	a.noteAreaLocked(r)
}

func (a *App) noteAreaLocked(r otd.Rect) {
	if !a.areaOK || !r.Same(a.area) {
		a.marks = append(a.marks, areaMark{At: time.Now(), Area: r})
		if len(a.marks) > 2000 {
			a.marks = a.marks[len(a.marks)-1000:]
		}
	}
	a.area, a.areaOK = r, true
}

func (a *App) tabletLocked() string {
	if a.env.HasProf {
		return a.env.Profile.Tablet
	}
	return a.set.Tablet
}

// areaAt is the area that was in use at t; before the first one seen, that
// one (ok false: a guess).
func (a *App) areaAt(t time.Time) (otd.Rect, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.marks) == 0 {
		return otd.Rect{}, false
	}
	if t.Before(a.marks[0].At) {
		return a.marks[0].Area, false
	}
	r := a.marks[0].Area
	for _, m := range a.marks {
		if m.At.After(t) {
			break
		}
		r = m.Area
	}
	return r, true
}

// current is the area in use now.
func (a *App) current() (otd.Rect, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.area, a.areaOK
}

// limits are what the suggested area may be, around the area r.
func (a *App) limitsFor(r otd.Rect) aim.Limits {
	a.mu.Lock()
	defer a.mu.Unlock()
	lim := aim.Limits{Rot: r.Rot, Ratio: a.set.Ratio, MinW: a.set.MinW, MaxW: a.set.MaxW}
	if lim.Ratio <= 0 && r.W > 0 {
		lim.Ratio = r.H / r.W
	}
	if a.env.HasProf {
		lim.Display = a.env.Profile.Display
		if a.env.Profile.SizeKnown {
			lim.TabletW, lim.TabletH = a.env.Profile.TabletW, a.env.Profile.TabletH
		}
	}
	return lim
}

// SetArea puts an area in OpenTabletDriver (now and in its file).
func (a *App) SetArea(r otd.Rect) error {
	if !r.Valid() {
		return errors.New("that area isn't valid")
	}
	a.mu.Lock()
	tablet, path := a.tabletLocked(), a.env.OTDPath
	before, had := a.area, a.areaOK
	a.mu.Unlock()
	note, err := putArea(path, tablet, r, true)
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.note = note
	if had && !before.Same(r) {
		b := before
		a.prev = &b
	}
	a.noteAreaLocked(r)
	a.mu.Unlock()
	log.Printf("area set: %.2f x %.3f at %.2f, %.2f (rotation %.1f)", r.W, r.H, r.X, r.Y, r.Rot)
	return nil
}

// Undo puts back the area there was before the last one set here.
func (a *App) Undo() error {
	a.mu.Lock()
	p := a.prev
	a.mu.Unlock()
	if p == nil {
		return errors.New("there's no earlier area to go back to")
	}
	if err := a.SetArea(*p); err != nil {
		return err
	}
	a.mu.Lock()
	a.prev = nil
	a.mu.Unlock()
	return nil
}

// ReopenOTD reopens the OpenTabletDriver window so it shows the new area.
func (a *App) ReopenOTD() error { return reopenOTD() }

// UpdateSettings changes the settings with the fields given.
func (a *App) UpdateSettings(f func(*Settings)) (Settings, error) {
	a.mu.Lock()
	s := a.set
	f(&s)
	s.fix()
	reenv := s.OsuDir != a.set.OsuDir || s.LazerDir != a.set.LazerDir || s.OTDPath != a.set.OTDPath || s.Tablet != a.set.Tablet
	a.set = s
	if reenv {
		a.envAt = time.Time{}
	}
	a.mu.Unlock()
	if err := saveSettings(a.dir, s); err != nil {
		return s, err
	}
	a.live.settingsChanged()
	a.wake()
	return s, nil
}

// Settings returns the settings.
func (a *App) Settings() Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.set
}

// round2 rounds to two decimals.
func round2(v float64) float64 { return math.Round(v*100) / 100 }
