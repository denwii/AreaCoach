// Package engine ties detection, import, storage and analysis together.
package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"areacoach/internal/analysis"
	"areacoach/internal/osu"
	"areacoach/internal/otd"
	"areacoach/internal/platform"
	"areacoach/internal/store"
)

// Warning is a setup problem shown to the user.
type Warning struct {
	Level string `json:"level"` // info, warn, error
	Text  string `json:"text"`
}

// Env is the detected environment.
type Env struct {
	OsuDir      string          `json:"osuDir"`
	OsuFound    bool            `json:"osuFound"`
	SongsDir    string          `json:"songsDir"`
	CfgPath     string          `json:"cfgPath"`
	Game        *osu.GameConfig `json:"game"`
	OTDPath     string          `json:"otdPath"`
	OTDFound    bool            `json:"otdFound"`
	Profiles    []otd.Profile   `json:"profiles"`
	Profile     *otd.Profile    `json:"profile"`
	ScreenW     int             `json:"screenW"`
	ScreenH     int             `json:"screenH"`
	Setup       analysis.Setup  `json:"setup"`
	SetupOK     bool            `json:"setupOk"`
	Warnings    []Warning       `json:"warnings"`
	OTDModTime  time.Time       `json:"otdModTime"`
	Connected   []string        `json:"connected"` // tablets connected to OpenTabletDriver now
	MapsIndexed int             `json:"mapsIndexed"`
}

// ImportStatus describes the background import.
type ImportStatus struct {
	Running   bool      `json:"running"`
	Done      int       `json:"done"`
	Total     int       `json:"total"`
	Current   string    `json:"current"`
	LastError string    `json:"lastError"`
	LastNew   time.Time `json:"lastNew"`
	Imported  int       `json:"imported"`
	Missing   int       `json:"missing"`
	// the replays found and not used, and why (the latest first)
	Skipped []SkippedReplay `json:"skipped,omitempty"`
	// the folders looked at for replays
	Folders []ReplayFolder `json:"folders,omitempty"`
}

// ReplayFolder is a folder looked at for replays: whether it is there, how
// many replays it has, when the newest one was written.
type ReplayFolder struct {
	Path   string    `json:"path"`
	Exists bool      `json:"exists"`
	Files  int       `json:"files"`
	Latest time.Time `json:"latest,omitempty"`
}

// SkippedReplay is a replay file found and not used.
type SkippedReplay struct {
	File   string    `json:"file"`
	Map    string    `json:"map,omitempty"` // as far as it is known
	When   time.Time `json:"when"`          // when the file was written
	Reason string    `json:"reason"`
	Retry  bool      `json:"retry,omitempty"` // it is looked at again (the map may come)
}

// Engine is the application core.
type Engine struct {
	st *store.Store

	mu       sync.Mutex
	env      Env
	index    *osu.Index
	indexDir string
	bmCache  map[string]*osu.Beatmap
	bmOrder  []string
	missing  map[string]time.Time
	readFail map[string]int // replay files that could not be read yet, how many times
	scanned  time.Time      // last look for maps not found among the songs
	imp      ImportStatus
	otdSig   store.FileSig
	cfgSig   store.FileSig

	repMu      sync.Mutex
	report     *analysis.Report
	repVersion int
	repDirty   bool
	repBusy    bool
	repTimer   *time.Timer

	subMu sync.Mutex
	subs  map[chan string]bool

	wake chan struct{}

	fmu    sync.Mutex
	fstate FilterState
	fkey   string

	cmu        sync.Mutex
	lastLookup time.Time
	conn       []string  // tablets connected to OpenTabletDriver (under cmu)
	connAt     time.Time // when they were asked

	ad  adaptState     // the diagnosis
	adf adaptFilterRun // the filter worked out with it

	probeAt  time.Time // last check that OpenTabletDriver answers (under cmu)
	probeOK  bool
	probeErr string
}

// New creates the engine.
func New(st *store.Store) *Engine {
	e := &Engine{st: st, bmCache: map[string]*osu.Beatmap{}, missing: map[string]time.Time{}, readFail: map[string]int{},
		subs: map[chan string]bool{}, wake: make(chan struct{}, 1), repDirty: true}
	return e
}

// Store exposes the store.
func (e *Engine) Store() *store.Store { return e.st }

// Subscribe returns a channel of event names.
func (e *Engine) Subscribe() chan string {
	ch := make(chan string, 16)
	e.subMu.Lock()
	e.subs[ch] = true
	e.subMu.Unlock()
	return ch
}

// Unsubscribe removes a channel.
func (e *Engine) Unsubscribe(ch chan string) {
	e.subMu.Lock()
	delete(e.subs, ch)
	e.subMu.Unlock()
}

func (e *Engine) emit(ev string) {
	e.subMu.Lock()
	defer e.subMu.Unlock()
	for ch := range e.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Env returns the detected environment.
func (e *Engine) Env() Env {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.env
}

// Import returns the import status.
func (e *Engine) Import() ImportStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := e.imp
	out.Skipped = append([]SkippedReplay(nil), e.imp.Skipped...)
	return out
}

// recheckRecent looks again at the replays of the last week that were
// not used, so that why is shown (and those a newer version can use, used).
func (e *Engine) recheckRecent() {
	env := e.Env()
	if !env.OsuFound {
		return
	}
	var paths []string
	from := e.st.UseFrom()
	for _, f := range e.listReplays(env.OsuDir) {
		if time.Since(f.mod) > 7*24*time.Hour || f.mod.Before(from) {
			break // newest first
		}
		paths = append(paths, f.path)
	}
	if n := e.st.ForgetSeen(paths); n > 0 {
		log.Printf("looking again at %d recent unused replays", n)
	}
}

// skip records a replay found and not used, and why (in the log too, once
// per file and reason).
func (e *Engine) skip(f replayFile, title, reason string, retry bool) {
	name := filepath.Base(f.path)
	e.mu.Lock()
	for i, s := range e.imp.Skipped {
		if s.File == name {
			if s.Reason == reason {
				e.mu.Unlock()
				return
			}
			e.imp.Skipped = append(e.imp.Skipped[:i], e.imp.Skipped[i+1:]...)
			break
		}
	}
	e.imp.Skipped = append(e.imp.Skipped, SkippedReplay{File: name, Map: title, When: f.mod, Reason: reason, Retry: retry})
	sort.SliceStable(e.imp.Skipped, func(i, j int) bool { return e.imp.Skipped[i].When.After(e.imp.Skipped[j].When) })
	if len(e.imp.Skipped) > 40 {
		e.imp.Skipped = e.imp.Skipped[:40] // the newest
	}
	e.mu.Unlock()
	if time.Since(f.mod) > 24*time.Hour {
		return // the old ones, at the first import: in the list only
	}
	what := name
	if title != "" {
		what = fmt.Sprintf("%s (%s)", title, name)
	}
	log.Printf("replay not used: %s: %s", what, reason)
}

// unskip forgets a replay that was used in the end.
func (e *Engine) unskip(f replayFile) {
	name := filepath.Base(f.path)
	e.mu.Lock()
	defer e.mu.Unlock()
	for i, s := range e.imp.Skipped {
		if s.File == name {
			e.imp.Skipped = append(e.imp.Skipped[:i], e.imp.Skipped[i+1:]...)
			return
		}
	}
}

// replayTitle is the map of a replay as far as it is known: from osu!.db,
// or from the name osu! gives to the replays saved with F2.
func (e *Engine) replayTitle(f replayFile, md string) string {
	e.mu.Lock()
	ix := e.index
	e.mu.Unlock()
	if ix != nil {
		if m, ok := ix.Meta(md); ok && m.Title != "" {
			return fmt.Sprintf("%s - %s [%s]", m.Artist, m.Title, m.Version)
		}
	}
	// "Player - Artist - Title [Difficulty] (date) Mode.osr"
	n := strings.TrimSuffix(filepath.Base(f.path), filepath.Ext(f.path))
	if i := strings.Index(n, " - "); i > 0 && strings.Contains(n, "[") {
		n = n[i+3:]
		if j := strings.LastIndex(n, " ("); j > 0 {
			n = n[:j]
		}
		return n
	}
	return ""
}

// Wake triggers an immediate scan.
func (e *Engine) Wake() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

// Run starts the background loop.
func (e *Engine) Run() {
	// only the replays saved since the first start count
	if n := e.st.DropPlaysBefore(e.st.UseFrom()); n > 0 {
		log.Printf("removed %d plays saved before the first start (%s): only newer replays are used", n, e.st.UseFrom().Local().Format("2006-01-02 15:04"))
		_ = e.st.Save()
	}
	e.checkReplayClock()
	e.diagResume()
	if n := e.st.MergeRepeatedEpochs(); n > 0 {
		log.Printf("merged %d periods with the same area", n)
		_ = e.st.Save()
	}
	e.detect(true)
	e.recheckRecent()
	e.Report() // start the first analysis immediately
	go e.loop()
	go e.adaptLoop()
}

func (e *Engine) loop() {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	e.scanReplays()
	for {
		select {
		case <-tick.C:
		case <-e.wake:
		}
		e.detect(false)
		e.scanReplays()
	}
}

// detect refreshes the environment and opens a new area period when the
// OpenTabletDriver area or the game resolution changed.
func (e *Engine) detect(force bool) {
	settings := e.st.State().Settings
	env := Env{}
	env.OsuDir = settings.OsuDir
	if env.OsuDir == "" {
		env.OsuDir = platform.FindOsuDir()
	}
	if env.OsuDir != "" {
		if _, err := os.Stat(env.OsuDir); err == nil {
			env.OsuFound = true
		}
	}
	env.ScreenW, env.ScreenH = platform.ScreenSize()
	if env.OsuFound {
		env.CfgPath = osu.FindUserConfig(env.OsuDir)
		if env.CfgPath != "" {
			if c, err := osu.ReadGameConfig(env.CfgPath); err == nil {
				env.Game = c
			}
		}
		env.SongsDir = filepath.Join(env.OsuDir, "Songs")
		if env.Game != nil {
			env.SongsDir = env.Game.SongsDir(env.OsuDir)
		}
	}
	env.OTDPath = settings.OTDPath
	if env.OTDPath == "" {
		env.OTDPath = otd.Find(platform.ProcessDir("OpenTabletDriver.Daemon.exe"), platform.ProcessDir("OpenTabletDriver.UX.Wpf.exe"))
	}
	var profile otd.Profile
	env.Connected = e.connectedTablets()
	if env.OTDPath != "" {
		if s, err := otd.Load(env.OTDPath); err == nil {
			env.OTDFound = true
			env.Profiles = s.Profiles
			env.OTDModTime = s.ModTime
			// the tablet chosen in the settings, otherwise the one connected
			// now (the file keeps a profile for every tablet ever plugged in)
			pick := settings.PreferredTablet
			if pick == "" {
				for _, name := range env.Connected {
					if _, ok := s.Best(name); ok && hasProfile(s, name) {
						pick = name
						break
					}
				}
			}
			if pick == "" && len(env.Connected) > 0 {
				// connected but not saved in the file yet: the live settings have it
				if p, ok := e.liveProfile(env.Connected[0]); ok {
					profile = p
					env.Profile = &p
					env.Warnings = append(env.Warnings, Warning{"warn", fmt.Sprintf("The profile of the connected tablet (%s) isn't saved in the OpenTabletDriver file yet: press Save once in OpenTabletDriver.", p.Tablet)})
				}
			}
			if env.Profile == nil {
				if p, ok := s.Best(pick); ok {
					profile = p
					env.Profile = &p
				}
			}
			if env.Connected != nil && len(env.Connected) == 0 {
				env.Warnings = append(env.Warnings, Warning{"warn", "OpenTabletDriver sees no connected tablet: the diagnosis can't change the area. Plug in the tablet and check that no other driver (for example the Wacom one) is using it."})
			}
			if env.Profile != nil && len(env.Connected) > 0 && !slices.Contains(env.Connected, profile.Tablet) {
				env.Warnings = append(env.Warnings, Warning{"warn", fmt.Sprintf("I'm using the OpenTabletDriver profile of the %s tablet, but the connected one is %s: pick the right tablet in Settings.", profile.Tablet, strings.Join(env.Connected, ", "))})
			}
		} else {
			env.Warnings = append(env.Warnings, Warning{"error", "I can't read the OpenTabletDriver settings: " + err.Error()})
		}
	}
	// setup
	su := analysis.Setup{StretchX: 1, StretchY: 1}
	if env.Profile != nil {
		su.TabletName = profile.Tablet
		su.Area = profile.Area
		su.Display = profile.Display
		su.TabletW, su.TabletH = profile.TabletW, profile.TabletH
		su.Smoothing, su.SmoothingSet = profile.SmoothingSignature(), true
		if !profile.Absolute {
			env.Warnings = append(env.Warnings, Warning{"error", "OpenTabletDriver isn't in absolute mode: osu! needs the absolute output."})
		}
	}
	if t := settings.Tablet; t != nil && t.W > 0 && t.H > 0 {
		su.TabletW, su.TabletH = t.W, t.H
		if t.Name != "" && env.Profile == nil {
			su.TabletName = t.Name
		}
	}
	if su.TabletW == 0 && env.Profile != nil {
		env.Warnings = append(env.Warnings, Warning{"warn", fmt.Sprintf("I don't know the size of the \"%s\" tablet: set it in Settings to see the right edges.", profile.Tablet)})
	}
	gw, gh := 0, 0
	if env.Game != nil {
		gw, gh = env.Game.GameResolution()
	}
	if settings.GameW > 0 && settings.GameH > 0 {
		gw, gh = settings.GameW, settings.GameH
	}
	if gw <= 0 || gh <= 0 {
		gw, gh = env.ScreenW, env.ScreenH
	}
	su.GameW, su.GameH = float64(gw), float64(gh)
	if g := env.Game; g != nil {
		switch {
		case g.Fullscreen && !g.Letterboxing:
			if gw != env.ScreenW || gh != env.ScreenH {
				su.StretchX = float64(env.ScreenW) / float64(gw)
				su.StretchY = float64(env.ScreenH) / float64(gh)
			}
		case g.Fullscreen && g.Letterboxing:
			su.WinX = float64(env.ScreenW-gw) * (g.LetterboxX + 100) / 200
			su.WinY = float64(env.ScreenH-gh) * (g.LetterboxY + 100) / 200
		default:
			// a window: osu! opens it in the middle of the screen
			su.WinX = float64(env.ScreenW-gw) / 2
			su.WinY = float64(env.ScreenH-gh) / 2
		}
		if math.Abs(g.MouseSpeed-1) > 0.001 {
			env.Warnings = append(env.Warnings, Warning{"warn", fmt.Sprintf("The mouse sensitivity in osu! is %s: with the tablet in absolute mode it should stay at 1.0, otherwise the mapping is distorted.", fmt.Sprintf("%.2f", g.MouseSpeed))})
		}
		if g.AbsoluteToWindow && !g.Fullscreen {
			env.Warnings = append(env.Warnings, Warning{"info", "\"Map absolute input to window\" is on in osu!: the real mapping may differ from the one worked out."})
		}
	} else if env.OsuFound {
		env.Warnings = append(env.Warnings, Warning{"warn", "I can't find the osu! configuration file: using the screen resolution."})
	}
	env.Setup = su
	env.SetupOK = su.Valid()
	if !env.OsuFound {
		env.Warnings = append(env.Warnings, Warning{"error", "I can't find the osu! folder. Set it in Settings."})
	}
	if !env.OTDFound {
		env.Warnings = append(env.Warnings, Warning{"error", "I can't find the OpenTabletDriver settings. Set the settings.json file in Settings."})
	}

	e.mu.Lock()
	if env.OsuFound && (e.index == nil || e.indexDir != env.SongsDir) {
		e.index = osu.NewIndex(env.OsuDir, env.SongsDir, filepath.Join(e.st.Dir(), "maps-index.json"))
		e.indexDir = env.SongsDir
	}
	ix := e.index
	e.mu.Unlock()
	if ix != nil {
		if err := ix.Refresh(); err != nil {
			env.Warnings = append(env.Warnings, Warning{"info", "osu!.db can't be read, looking for the maps directly in the Songs folder."})
		}
		env.MapsIndexed = ix.Count()
	}

	changed := e.updateEpochs(env, force)
	e.mu.Lock()
	old := e.env
	e.env = env
	e.mu.Unlock()
	if changed || force || len(old.Warnings) != len(env.Warnings) || old.SetupOK != env.SetupOK {
		e.emit("status")
	}
	if changed {
		e.invalidate()
	}
}

// sameSetup compares what decides a period: the area, the display mapping
// and the filters. The game window (resolution, position) is not part of it:
// when osu! is closed it can only be guessed from its config, and a guess
// must never split the plays of one area into two periods.
func sameSetup(a, b analysis.Setup) bool {
	return a.Area.Same(b.Area) && a.Display.Same(b.Display) &&
		(!a.SmoothingSet || !b.SmoothingSet || a.Smoothing == b.Smoothing)
}

func sameGame(a, b analysis.Setup) bool {
	return math.Abs(a.GameW-b.GameW) < 0.5 && math.Abs(a.GameH-b.GameH) < 0.5 &&
		math.Abs(a.StretchX-b.StretchX) < 1e-3 && math.Abs(a.StretchY-b.StretchY) < 1e-3 &&
		math.Abs(a.WinX-b.WinX) <= 0.5 && math.Abs(a.WinY-b.WinY) <= 0.5
}

func (e *Engine) updateEpochs(env Env, force bool) bool {
	if !env.SetupOK {
		return false
	}
	cur, ok := e.st.CurrentEpoch()
	if !ok {
		from := env.OTDModTime
		if from.IsZero() || from.After(time.Now()) {
			from = time.Now()
		}
		ep := e.st.StartEpoch(env.Setup, from, "otd")
		established := time.Since(from) > 72*time.Hour
		e.st.Update(func(s *store.State) {
			for i := range s.Epochs {
				if s.Epochs[i].ID == ep.ID {
					s.Epochs[i].Established = established
				}
			}
		})
		_ = e.st.Save()
		e.reassignPending()
		return true
	}
	if sameSetup(cur.Setup, env.Setup) {
		if !cur.Setup.SmoothingSet && env.Setup.SmoothingSet {
			// periods from older versions: the filters were not recorded yet
			e.st.UpdateCurrentSetup(func(s *analysis.Setup) { s.Smoothing, s.SmoothingSet = env.Setup.Smoothing, true })
			_ = e.st.Save()
			return true
		}
		changed := false
		if cur.Setup.TabletW != env.Setup.TabletW || cur.Setup.TabletH != env.Setup.TabletH || cur.Setup.TabletName != env.Setup.TabletName {
			e.st.UpdateCurrentSetup(func(s *analysis.Setup) {
				s.TabletW, s.TabletH, s.TabletName = env.Setup.TabletW, env.Setup.TabletH, env.Setup.TabletName
			})
			changed = true
		}
		// the game window, from the osu! config
		if env.Game != nil && !sameGame(cur.Setup, env.Setup) {
			e.st.UpdateCurrentSetup(func(s *analysis.Setup) {
				s.GameW, s.GameH, s.StretchX, s.StretchY = env.Setup.GameW, env.Setup.GameH, env.Setup.StretchX, env.Setup.StretchY
				s.WinX, s.WinY = env.Setup.WinX, env.Setup.WinY
			})
			changed = true
		}
		if changed {
			_ = e.st.Save()
		}
		return changed
	}
	at := time.Now()
	if !cur.Setup.Area.Same(env.Setup.Area) || !cur.Setup.Display.Same(env.Setup.Display) {
		if !env.OTDModTime.IsZero() && env.OTDModTime.After(cur.From) && env.OTDModTime.Before(at.Add(time.Minute)) {
			at = env.OTDModTime
		}
	}
	src := "otd"
	if cur.Setup.Area.Same(env.Setup.Area) {
		src = "osu"
		if cur.Setup.Display.Same(env.Setup.Display) && cur.Setup.Smoothing != env.Setup.Smoothing {
			src = "filtro"
		}
	}
	e.st.StartEpoch(env.Setup, at, src)
	_ = e.st.Save()
	log.Printf("new area: %+v", env.Setup.Area)
	return true
}

func (e *Engine) reassignPending() {}

func (e *Engine) invalidate() {
	e.repMu.Lock()
	e.repDirty = true
	e.repMu.Unlock()
	e.emit("dirty")
	// recompute right away (debounced) so the report is ready when looked at
	e.repMu.Lock()
	if e.repTimer != nil {
		e.repTimer.Stop()
	}
	e.repTimer = time.AfterFunc(600*time.Millisecond, func() { e.Report() })
	e.repMu.Unlock()
}

// replay files on disk
type replayFile struct {
	path string
	sig  store.FileSig
	mod  time.Time
}

func (e *Engine) listReplays(osuDir string) []replayFile {
	var out []replayFile
	var folders []ReplayFolder
	for _, sub := range []string{filepath.Join("Data", "r"), "Replays"} {
		dir := filepath.Join(osuDir, sub)
		entries, err := os.ReadDir(dir)
		fo := ReplayFolder{Path: dir, Exists: err == nil}
		if err != nil {
			folders = append(folders, fo)
			continue
		}
		for _, de := range entries {
			if de.IsDir() || !strings.EqualFold(filepath.Ext(de.Name()), ".osr") {
				continue
			}
			info, err := de.Info()
			if err != nil {
				continue
			}
			p := filepath.Join(osuDir, sub, de.Name())
			out = append(out, replayFile{path: p, sig: store.FileSig{Size: info.Size(), MTime: info.ModTime().UnixNano()}, mod: info.ModTime()})
			fo.Files++
			if info.ModTime().After(fo.Latest) {
				fo.Latest = info.ModTime()
			}
		}
		folders = append(folders, fo)
	}
	e.mu.Lock()
	e.imp.Folders = folders
	e.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].mod.After(out[j].mod) })
	return out
}

func (e *Engine) scanReplays() {
	env := e.Env()
	if !env.OsuFound {
		return
	}
	e.mu.Lock()
	if e.imp.Running {
		e.mu.Unlock()
		return
	}
	e.mu.Unlock()
	files := e.listReplays(env.OsuDir)
	st := e.st.State()
	limit := st.Settings.MaxImport
	from := e.st.UseFrom()
	var todo []replayFile
	for i, f := range files {
		if limit > 0 && i >= limit {
			break
		}
		if f.mod.Before(from) {
			break // newest first: the rest was saved before the first start
		}
		if e.st.Seen(f.path, f.sig) {
			continue
		}
		if time.Since(f.mod) < 2*time.Second {
			continue // osu! may still be writing it: the next look
		}
		e.mu.Lock()
		retry, waiting := e.missing[f.path]
		e.mu.Unlock()
		if waiting && time.Now().Before(retry) {
			continue
		}
		todo = append(todo, f)
	}
	if len(todo) == 0 {
		return
	}
	e.mu.Lock()
	e.imp = ImportStatus{Running: true, Total: len(todo), Imported: e.imp.Imported, Missing: e.imp.Missing, Skipped: e.imp.Skipped, Folders: e.imp.Folders}
	e.mu.Unlock()
	e.emit("import")
	newPlays := 0
	lastEmit := time.Now()
	for i, f := range todo {
		e.mu.Lock()
		e.imp.Done = i
		e.imp.Current = filepath.Base(f.path)
		e.mu.Unlock()
		added, err := e.importReplay(f, env)
		if err != nil {
			e.mu.Lock()
			e.imp.LastError = err.Error()
			e.mu.Unlock()
		}
		if added {
			newPlays++
		}
		if time.Since(lastEmit) > 700*time.Millisecond {
			e.emit("import")
			lastEmit = time.Now()
		}
		if newPlays > 0 && newPlays%150 == 0 {
			_ = e.st.Save()
		}
	}
	_ = e.st.Save()
	e.mu.Lock()
	e.imp.Running = false
	e.imp.Done = len(todo)
	e.imp.Current = ""
	if newPlays > 0 {
		e.imp.LastNew = time.Now()
		e.imp.Imported += newPlays
	}
	e.mu.Unlock()
	e.emit("import")
	if newPlays > 0 {
		e.invalidate()
	}
}

func (e *Engine) beatmap(path, md string) (*osu.Beatmap, error) {
	e.mu.Lock()
	if bm, ok := e.bmCache[md]; ok {
		e.mu.Unlock()
		return bm, nil
	}
	e.mu.Unlock()
	bm, err := osu.ParseBeatmapFile(path)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	e.bmCache[md] = bm
	e.bmOrder = append(e.bmOrder, md)
	if len(e.bmOrder) > 64 {
		delete(e.bmCache, e.bmOrder[0])
		e.bmOrder = e.bmOrder[1:]
	}
	e.mu.Unlock()
	return bm, nil
}

// replayReadTries is how many times a replay file that cannot be read is
// tried before it is given up.
const replayReadTries = 8

// maxAnalysisPlays caps memory and time: only the most recent plays are analysed.
const maxAnalysisPlays = 1000

const skipMods = osu.ModRelax | osu.ModAutopilot | osu.ModAutoplay | osu.ModCinema | osu.ModTarget

func (e *Engine) importReplay(f replayFile, env Env) (bool, error) {
	rep, err := osu.ReadReplay(f.path)
	if err != nil {
		// osu! may still be writing it, or an antivirus looking at it: it
		// is tried again a few times before giving up on it
		e.mu.Lock()
		e.readFail[f.path]++
		n := e.readFail[f.path]
		if n < replayReadTries {
			e.missing[f.path] = time.Now().Add(15 * time.Second)
		}
		e.mu.Unlock()
		if n < replayReadTries {
			e.skip(f, e.replayTitle(f, ""), "I can't read the replay file yet ("+err.Error()+"): trying again shortly", true)
			return false, nil
		}
		e.st.MarkSeen(f.path, f.sig)
		e.skip(f, e.replayTitle(f, ""), "the replay file can't be read: "+err.Error(), false)
		return false, fmt.Errorf("%s: %w", filepath.Base(f.path), err)
	}
	e.mu.Lock()
	delete(e.readFail, f.path)
	e.mu.Unlock()
	if rep.Mode != 0 || rep.Mods&skipMods != 0 || len(rep.Frames) < 10 {
		e.st.MarkSeen(f.path, f.sig)
		why := "replay too short"
		switch {
		case rep.Mode != 0:
			why = "not osu!standard (taiko, catch or mania)"
		case rep.Mods&skipMods != 0:
			why = "played with " + osu.ModString(rep.Mods&skipMods) + ": the aim isn't yours"
		}
		e.skip(f, e.replayTitle(f, rep.BeatmapMD5), why, false)
		return false, nil
	}
	if rep.ReplayMD5 != "" && e.st.HasReplay(rep.ReplayMD5) {
		e.st.MarkSeen(f.path, f.sig)
		return false, nil
	}
	e.mu.Lock()
	ix := e.index
	e.mu.Unlock()
	if ix == nil {
		return false, errors.New("map index not ready")
	}
	path, ok := ix.Lookup(rep.BeatmapMD5)
	if !ok {
		_ = ix.Refresh()
		path, ok = ix.Lookup(rep.BeatmapMD5)
	}
	if !ok {
		// a map just downloaded (as in multiplayer) is not in osu!.db until
		// osu! closes: the songs are looked through, at most every half
		// minute
		e.mu.Lock()
		force := time.Since(e.scanned) > 30*time.Second
		if force {
			e.scanned = time.Now()
		}
		e.mu.Unlock()
		if ix.Scan(force) > 0 {
			path, ok = ix.Lookup(rep.BeatmapMD5)
		}
	}
	if !ok {
		e.mu.Lock()
		if _, was := e.missing[f.path]; !was {
			e.imp.Missing++
		}
		e.missing[f.path] = time.Now().Add(time.Minute)
		e.mu.Unlock()
		e.skip(f, e.replayTitle(f, rep.BeatmapMD5), "I can't find the map in the Songs folder (if you just downloaded it I look again every minute; if you updated or deleted it, the replay is of a version that no longer exists)", true)
		return false, nil
	}
	e.mu.Lock()
	delete(e.missing, f.path)
	e.mu.Unlock()
	bm, err := e.beatmap(path, rep.BeatmapMD5)
	if err != nil {
		e.st.MarkSeen(f.path, f.sig)
		e.skip(f, e.replayTitle(f, rep.BeatmapMD5), "the map file can't be read: "+err.Error(), false)
		return false, fmt.Errorf("unreadable map: %w", err)
	}
	if bm.Mode != 0 {
		e.st.MarkSeen(f.path, f.sig)
		e.skip(f, fmt.Sprintf("%s - %s [%s]", bm.Artist, bm.Title, bm.Version), "the map isn't osu!standard", false)
		return false, nil
	}
	objs, d := bm.Objects(rep.Mods)
	hits, sum := analysis.Judge(objs, d, rep.Frames)
	meta := &store.PlayMeta{ReplayPath: f.path, FileTime: f.mod, ReplayMD5: rep.ReplayMD5, BeatmapMD5: rep.BeatmapMD5, Player: rep.Player,
		Time: rep.Timestamp, Mods: rep.Mods, ModStr: osu.ModString(rep.Mods), Title: bm.Title, Artist: bm.Artist, Version: bm.Version,
		Creator: bm.Creator, CS: round2(d.CS), AR: round2(d.AR), OD: round2(d.OD), BPM: math.Round(bm.BPM() * d.Rate), LengthMs: bm.LengthMs(),
		Rate: d.Rate, N300: rep.N300, N100: rep.N100, N50: rep.N50, NMiss: rep.NMiss, MaxCombo: rep.MaxCombo, Summary: sum, Status: "ok"}
	if meta.ReplayMD5 == "" {
		meta.ReplayMD5 = fmt.Sprintf("%s-%d", rep.BeatmapMD5, rep.Timestamp.UnixNano())
	}
	if meta.Time.IsZero() {
		meta.Time = f.mod
	} else if tz := e.st.State().ReplayTZMinutes; tz != 0 {
		meta.Time = meta.Time.Add(-time.Duration(tz) * time.Minute)
	}
	tot := rep.N300 + rep.N100 + rep.N50 + rep.NMiss
	if tot > 0 {
		meta.Accuracy = float64(300*rep.N300+100*rep.N100+50*rep.N50) / float64(300*tot)
	}
	var errs []float64
	for i := range hits {
		h := &hits[i]
		if h.HasClick() {
			v := math.Hypot(float64(h.CX-h.TX), float64(h.CY-h.TY)) / float64(h.Radius)
			if v <= 3 {
				errs = append(errs, math.Min(v, 3))
			}
		}
	}
	if len(errs) > 0 {
		s := 0.0
		for _, v := range errs {
			s += v * v
		}
		meta.NormErr = math.Sqrt(s / float64(len(errs)) / 2)
	}
	if err := e.st.AddPlay(meta, hits); err != nil {
		return false, err
	}
	e.st.MarkSeen(f.path, f.sig)
	e.unskip(f)
	if time.Since(f.mod) < 24*time.Hour {
		log.Printf("replay imported: %s - %s [%s] (%s)", bm.Artist, bm.Title, bm.Version, filepath.Base(f.path))
	}
	return true, nil
}

func round2(x float64) float64 { return math.Round(x*100) / 100 }

// Report returns the latest report and starts a recomputation if needed.
func (e *Engine) Report() (*analysis.Report, int, bool) {
	e.repMu.Lock()
	defer e.repMu.Unlock()
	if e.repDirty && !e.repBusy {
		e.repBusy = true
		e.repDirty = false
		go e.compute()
	}
	return e.report, e.repVersion, e.repBusy
}

// Invalidate forces a recomputation.
func (e *Engine) Invalidate() { e.invalidate() }

// PlaysForAnalysis loads every usable play with its area.
func (e *Engine) PlaysForAnalysis() ([]*analysis.PlayData, []analysis.EpochInfo, *analysis.EpochInfo) {
	plays, groups, cur, _ := e.playsForAnalysis()
	return plays, groups, cur
}

// sameMappingSetup: two periods with the same area, display and filters are
// the same area for the analysis, even when used at different times.
func sameMappingSetup(a, b analysis.Setup) bool {
	return a.Area.Same(b.Area) && a.Display.Same(b.Display) &&
		(!a.SmoothingSet || !b.SmoothingSet || a.Smoothing == b.Smoothing)
}

// playsForAnalysis returns the plays of the active profile, with the
// periods of the same area joined into one (so the plays made with an area
// count for it whenever they were made), and the periods as they happened.
func (e *Engine) playsForAnalysis() ([]*analysis.PlayData, []analysis.EpochInfo, *analysis.EpochInfo, []analysis.EpochInfo) {
	st := e.st.State()
	periods := st.Epochs
	canon := map[int]int{}
	var groups []analysis.EpochInfo
	gi := map[int]int{} // canonical id -> index in groups
	for i, p := range periods {
		c := p.ID
		for j := 0; j < i; j++ {
			if sameMappingSetup(periods[j].Setup, p.Setup) {
				c = canon[periods[j].ID]
				break
			}
		}
		canon[p.ID] = c
		if k, ok := gi[c]; ok {
			g := &groups[k]
			g.Setup = p.Setup // the latest details
			g.Established = g.Established || p.Established
			g.Adapted = g.Adapted || p.Adapted
			if p.To == nil {
				g.To = nil
			} else if g.To != nil && p.To.After(*g.To) {
				t := *p.To
				g.To = &t
			}
		} else {
			g := p
			g.ID = c
			gi[c] = len(groups)
			groups = append(groups, g)
		}
	}
	var plays []*analysis.PlayData
	used := 0
	for _, p := range e.st.Plays() { // newest first
		if p.Status != "ok" || p.Mixed || st.PlayProfile(p) != st.ActiveProfile {
			continue
		}
		if used >= maxAnalysisPlays {
			break
		}
		used++
		hits, err := e.st.Hits(p.ID)
		if err != nil {
			continue
		}
		eid := canon[store.EpochAt(periods, p.Time)]
		pd := &analysis.PlayData{ID: p.ID, Time: p.Time, Epoch: eid, Rate: p.Rate, Hits: hits, Duration: p.LengthMs,
			MapKey: fmt.Sprintf("%s|%d", p.BeatmapMD5, p.Mods&(osu.ModHardRock|osu.ModEasy|osu.ModDoubleTime|osu.ModHalfTime))}
		if k, ok := gi[eid]; ok {
			pd.Setup = groups[k].Setup
		}
		plays = append(plays, pd)
	}
	var cur *analysis.EpochInfo
	for i := len(periods) - 1; i >= 0; i-- {
		if periods[i].To == nil {
			if k, ok := gi[canon[periods[i].ID]]; ok {
				c := groups[k]
				c.To = nil
				cur = &c
			}
			break
		}
	}
	return plays, groups, cur, periods
}

func (e *Engine) compute() {
	t0 := time.Now()
	defer func() { log.Printf("analysis done in %v", time.Since(t0).Round(time.Millisecond)) }()
	defer func() {
		if r := recover(); r != nil {
			log.Printf("analysis error: %v", r)
			e.repMu.Lock()
			e.repBusy = false
			e.repMu.Unlock()
		}
	}()
	e.diagStep() // moves the diagnosis forward
	plays, epochs, cur, periods := e.playsForAnalysis()
	st := e.st.State()
	rep := analysis.Analyze(analysis.Input{Plays: plays, Epochs: epochs, Periods: periods, Current: cur, Weights: st.Settings.Weights, Now: time.Now(),
		PrevCompromise: st.LastCompromise})
	// remember the advice, so that an area change can be matched to it
	e.st.Update(func(s *store.State) {
		if rep.Coach.Next != nil {
			n := *rep.Coach.Next
			s.LastAdvice = &n
		} else {
			s.LastAdvice = nil
		}
		if rep.Areas.Final != nil {
			c := rep.Areas.Final.Rect
			s.LastCompromise = &c
		}
	})
	e.repMu.Lock()
	e.report = rep
	e.repVersion++
	e.repBusy = false
	again := e.repDirty
	e.repMu.Unlock()
	e.emit("report")
	if again {
		e.Report()
	}
}

func isLocalReplay(path string) bool {
	return strings.Contains(strings.ToLower(filepath.ToSlash(path)), "/data/r/")
}

// checkReplayClock makes sure replay times are real instants. osu! saves
// local replays right after the play, so the file time and the time stored
// in the replay must agree; if nearly all of them differ by the same whole
// number of hours, the replays carry local time and every play would land in
// the wrong area period around a change. Then all times are corrected.
func (e *Engine) checkReplayClock() {
	st := e.st.State()
	if st.ReplayTZChecked {
		return
	}
	counts := map[time.Duration]int{}
	n := 0
	for _, p := range e.st.Plays() {
		if p.Status != "ok" || !isLocalReplay(p.ReplayPath) {
			continue
		}
		fi, err := os.Stat(p.ReplayPath)
		if err != nil {
			continue
		}
		d := p.Time.Sub(fi.ModTime())
		r := d.Round(30 * time.Minute)
		if (d - r).Abs() > 6*time.Minute {
			r = -1 // not a whole offset
		}
		counts[r]++
		n++
	}
	if n < 10 {
		return // decide later, with more replays
	}
	best, bn := time.Duration(0), 0
	for r, c := range counts {
		if r != -1 && c > bn {
			best, bn = r, c
		}
	}
	tz := 0
	if best != 0 && best.Abs() <= 14*time.Hour && float64(bn) >= 0.8*float64(n) {
		tz = int(best / time.Minute)
		moved := e.st.ShiftPlayTimes(func(p *store.PlayMeta) time.Duration {
			return -best // osu! writes every replay the same way
		})
		log.Printf("replay times in local time (%+d min): fixed %d replays", tz, moved)
	}
	e.st.Update(func(s *store.State) { s.ReplayTZMinutes, s.ReplayTZChecked = tz, true })
	_ = e.st.Save()
}

// connectedTablets asks OpenTabletDriver which tablets are connected (at
// most every few seconds).
func (e *Engine) connectedTablets() []string {
	e.cmu.Lock()
	if time.Since(e.connAt) < 8*time.Second {
		c := e.conn
		e.cmu.Unlock()
		return c
	}
	e.connAt = time.Now()
	e.cmu.Unlock()
	var names []string
	if d, err := otd.DialDaemon(600 * time.Millisecond); err == nil {
		names, _ = d.Tablets()
		d.Close()
	}
	e.cmu.Lock()
	e.conn = names
	e.cmu.Unlock()
	return names
}

// liveProfile is the profile of a tablet in the settings OpenTabletDriver
// uses now (also when not saved in its file yet).
func (e *Engine) liveProfile(tablet string) (otd.Profile, bool) {
	d, s, err := e.daemon()
	if err != nil {
		return otd.Profile{}, false
	}
	d.Close()
	b, err := json.Marshal(s)
	if err != nil {
		return otd.Profile{}, false
	}
	ps, err := otd.Parse(b)
	if err != nil {
		return otd.Profile{}, false
	}
	for _, p := range ps.Profiles {
		if p.Tablet == tablet && p.Area.Valid() {
			return p, true
		}
	}
	return otd.Profile{}, false
}

func hasProfile(s *otd.Settings, tablet string) bool {
	for _, p := range s.Profiles {
		if p.Tablet == tablet && p.Area.Valid() {
			return true
		}
	}
	return false
}
