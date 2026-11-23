package app

import (
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"areacoach/internal/aim"
	"areacoach/internal/osu"
	"areacoach/internal/otd"
)

// Report is the advice from one replay.
type Report struct {
	ID         int          `json:"id"`
	At         time.Time    `json:"at"`
	PlayedAt   time.Time    `json:"playedAt"`
	Map        string       `json:"map"`
	Mods       string       `json:"mods"`
	Lazer      bool         `json:"lazer"`
	Accuracy   float64      `json:"accuracy"`
	Heads      int          `json:"heads"`  // circles and slider heads
	Played     int          `json:"played"` // up to where the map was played
	Pressed    int          `json:"pressed"`
	Used       otd.Rect     `json:"used"`      // the area of the play (at its end)
	UsedGuess  bool         `json:"usedGuess"` // not known: the one in use now assumed
	Mixed      bool         `json:"mixed"`     // the area changed during the play
	WidthKept  bool         `json:"widthKept"` // the map can't tell the width: only moved
	Ratio      float64      `json:"ratio"`
	Result     *aim.Result  `json:"result,omitempty"`
	Dots       [][2]float32 `json:"dots,omitempty"` // presses around the centre (radii, + right, + down)
	Error      string       `json:"error,omitempty"`
	AppliedNow bool         `json:"appliedNow"`
}

type replayFile struct {
	path  string
	mod   time.Time
	lazer bool
}

// replays lists the local replays: osu! stable's Data/r and osu!lazer's.
func (a *App) replays(since time.Time) []replayFile {
	a.mu.Lock()
	osuDir := a.env.OsuDir
	lf := a.lazer
	a.mu.Unlock()
	var out []replayFile
	if osuDir != "" {
		for _, f := range a.stableReplays(filepath.Join(osuDir, "Data", "r")) {
			if !f.mod.Before(since) {
				out = append(out, f)
			}
		}
	}
	if lf != nil {
		for _, r := range lf.Replays() {
			if !r.MTime.Before(since) {
				out = append(out, replayFile{path: r.Path, mod: r.MTime, lazer: true})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].mod.After(out[j].mod) })
	return out
}

// stableReplays lists osu! stable's local replays (read again only when the
// folder changes: it can hold thousands).
func (a *App) stableReplays(dir string) []replayFile {
	st, err := os.Stat(dir)
	if err != nil {
		return nil
	}
	a.mu.Lock()
	if a.rDir == dir && a.rDirTime.Equal(st.ModTime()) && time.Since(a.rListed) < time.Minute {
		list := a.rList
		a.mu.Unlock()
		return list
	}
	a.mu.Unlock()
	var list []replayFile
	if ents, err := os.ReadDir(dir); err == nil {
		for _, de := range ents {
			if de.IsDir() || !strings.EqualFold(filepath.Ext(de.Name()), ".osr") {
				continue
			}
			if info, err := de.Info(); err == nil {
				list = append(list, replayFile{path: filepath.Join(dir, de.Name()), mod: info.ModTime()})
			}
		}
	}
	a.mu.Lock()
	a.rDir, a.rDirTime, a.rList, a.rListed = dir, st.ModTime(), list, time.Now()
	a.mu.Unlock()
	return list
}

// checkReplays analyses the newest replay saved since the program started.
func (a *App) checkReplays() {
	for _, f := range a.replays(a.started) {
		a.mu.Lock()
		tries := a.done[f.path]
		a.mu.Unlock()
		if tries < 0 || tries >= 3 {
			continue
		}
		if time.Since(f.mod) < 1500*time.Millisecond {
			return // still being written
		}
		rep, retry := a.analyze(f)
		a.mu.Lock()
		if retry {
			a.done[f.path] = tries + 1
		} else {
			a.done[f.path] = -1
		}
		a.mu.Unlock()
		if rep != nil {
			a.publish(rep)
		}
		// the older ones are not the current play
		for _, g := range a.replays(a.started) {
			a.mu.Lock()
			if _, ok := a.done[g.path]; !ok && g.mod.Before(f.mod) {
				a.done[g.path] = -1
			}
			a.mu.Unlock()
		}
		return
	}
}

func (a *App) publish(rep *Report) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reports++
	rep.ID = a.reports
	rep.At = time.Now()
	if rep.Error != "" && a.report != nil && a.report.Error == "" && rep.Map == "" {
		// a replay that is not a play (someone else's, autoplay): the advice
		// shown stays, with a note
		a.replayMsg = rep.Error
		return
	}
	a.report, a.replayMsg = rep, ""
}

// AnalyzeLatest analyses the newest replay there is, whenever it was made.
func (a *App) AnalyzeLatest() error {
	a.mu.Lock()
	if a.busy {
		a.mu.Unlock()
		return fmt.Errorf("already looking at a replay")
	}
	a.busy = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.busy = false
		a.mu.Unlock()
	}()
	a.scanLazer(5 * time.Second)
	list := a.replays(time.Time{})
	if len(list) == 0 {
		return fmt.Errorf("I can't find any replay (osu! saves one for every map you finish)")
	}
	var last *Report
	for i, f := range list {
		if i >= 15 {
			break
		}
		rep, _ := a.analyze(f)
		if rep == nil {
			continue
		}
		last = rep
		if rep.Error == "" || rep.Map != "" {
			break // a play of the player's (with or without advice)
		}
	}
	if last == nil {
		return fmt.Errorf("I can't read the latest replays")
	}
	a.mu.Lock()
	a.done[list[0].path] = -1
	a.mu.Unlock()
	a.publish(last)
	return nil
}

const skipMods = osu.ModAutopilot | osu.ModAutoplay | osu.ModCinema | osu.ModTarget | osu.ModRelax

// analyze makes the advice from one replay. retry: it could not be read
// yet (it may still be being written).
func (a *App) analyze(f replayFile) (*Report, bool) {
	rep, err := osu.ReadReplay(f.path)
	if err != nil {
		log.Printf("replay %s: %v", filepath.Base(f.path), err)
		return nil, true
	}
	out := &Report{Lazer: rep.Lazer || f.lazer, PlayedAt: f.mod, Mods: osu.ModString(rep.Mods)}
	if rep.Lazer {
		out.Mods = rep.LazerModString()
	}
	fail := func(msg string) (*Report, bool) {
		out.Error = msg
		log.Printf("replay %s: %s", filepath.Base(f.path), msg)
		return out, false
	}
	a.mu.Lock()
	env := a.env
	ix, lf := a.index, a.lazer
	a.mu.Unlock()
	if rep.Lazer && env.Lazer != nil && env.Lazer.Username != "" && rep.Player != "" && !strings.EqualFold(env.Lazer.Username, rep.Player) {
		return fail(fmt.Sprintf("the newest replay is %s's, not yours", rep.Player))
	}
	switch {
	case rep.Mode != 0:
		return fail("the newest replay isn't osu!standard")
	case rep.Mods&skipMods != 0:
		return fail("the newest replay is played with " + osu.ModString(rep.Mods&skipMods) + ": the aim or the clicks aren't yours")
	case len(rep.Frames) < 10:
		return fail("the newest replay is too short")
	}
	var adj osu.Adjust
	if rep.Lazer {
		ad, bad := rep.LazerAdjust()
		if bad != "" {
			return fail("the newest replay is played with " + bad + ": the circles or the speed change while you play")
		}
		adj = ad
	}
	path, ok := "", false
	if out.Lazer && lf != nil {
		path, ok = lf.Lookup(rep.BeatmapMD5)
	}
	if !ok && ix != nil {
		if path, ok = ix.Lookup(rep.BeatmapMD5); !ok {
			_ = ix.Refresh()
			if path, ok = ix.Lookup(rep.BeatmapMD5); !ok && ix.Scan(true) > 0 {
				path, ok = ix.Lookup(rep.BeatmapMD5)
			}
		}
	}
	if !ok && lf != nil {
		path, ok = lf.Lookup(rep.BeatmapMD5)
	}
	if !ok {
		return fail("I can't find the map of the newest replay (if you just downloaded it, try again in a minute)")
	}
	bm, err := osu.ParseBeatmapFile(path)
	if err != nil {
		return fail("I can't read the map: " + err.Error())
	}
	out.Map = fmt.Sprintf("%s - %s [%s]", bm.Artist, bm.Title, bm.Version)
	if bm.Mode != 0 {
		return fail("the map isn't osu!standard")
	}
	if tot := rep.N300 + rep.N100 + rep.N50 + rep.NMiss; tot > 0 {
		out.Accuracy = float64(300*rep.N300+100*rep.N100+50*rep.N50) / float64(300*tot)
	}
	objs, d := bm.ObjectsAdj(rep.Mods, adj)
	clicks := aim.Judge(objs, d, aim.KeyPresses(rep.Frames))
	out.Heads = len(clicks)
	clicks = clicks[:aim.Played(clicks)]
	out.Played = len(clicks)
	for _, c := range clicks {
		if c.Pressed {
			out.Pressed++
		}
	}
	// the area of each press: the replay was saved when the play ended
	lastT := rep.Frames[len(rep.Frames)-1].T
	rate := d.Rate
	if rate <= 0 {
		rate = 1
	}
	now, nowOK := a.current()
	wallOf := func(c aim.Click) time.Time {
		return f.mod.Add(-time.Duration((lastT - c.PressT) / rate * float64(time.Millisecond)))
	}
	areas := make([]otd.Rect, len(clicks))
	for i, c := range clicks {
		if !c.Pressed {
			continue
		}
		r, _ := a.areaAt(wallOf(c))
		if !r.Valid() {
			if !nowOK {
				return fail("I don't know the area you played with: OpenTabletDriver doesn't answer")
			}
			r = now
		}
		areas[i] = r
	}
	// played while the program was closed: the area then is not known
	out.UsedGuess = f.mod.Before(a.started)
	for i := len(clicks) - 1; i >= 0; i-- {
		if clicks[i].Pressed {
			out.Used = areas[i]
			break
		}
	}
	for i, c := range clicks {
		if c.Pressed && !areas[i].Same(out.Used) {
			out.Mixed = true
			break
		}
	}
	if !out.Used.Valid() {
		out.Used = now
	}
	if !env.HasProf {
		return fail("I can't read the OpenTabletDriver profile")
	}
	su := env.setup(out.Lazer, out.Used)
	samples := aim.Samples(clicks, su, func(i int) otd.Rect { return areas[i] })
	if len(samples) < aim.MinSamples {
		return fail(fmt.Sprintf("too few clicks in the newest replay (%d): play a longer stretch of a map", len(samples)))
	}
	lim := a.limitsFor(out.Used)
	out.Ratio = lim.Ratio
	res, ok := aim.Fit(samples, lim, out.Used, true)
	if !ok {
		return fail("I can't work out an area from this replay")
	}
	if se := res.SE[0]; se > math.Max(1.5, 0.02*out.Used.W) {
		// the notes of this map don't tell the width apart (all in one
		// part of the screen, or too few): only the position is changed
		w := math.Max(lim.MinW, out.Used.W)
		if lim.MaxW > 0 {
			w = math.Min(w, lim.MaxW)
		}
		fixed := lim
		fixed.MinW, fixed.MaxW = w, w
		if r2, ok := aim.Fit(samples, fixed, out.Used, true); ok {
			r2.SE[0] = se
			res, out.WidthKept = r2, true
		}
	}
	out.Result = &res
	// the clicks go in the memory of the widths, once, when the area of the
	// play is known and the live section didn't already take them
	if !out.UsedGuess && !a.live.wasOn(wallOf(firstPressed(clicks)), f.mod) {
		id := rep.ReplayMD5
		if id == "" {
			id = f.path
		}
		if !a.mem.Seen(a.memKey(out.Used), id) {
			a.remember(f.mod, id, clicks, aim.Moves(objs, d, rate, clicks), areas)
		}
	}
	for _, s := range samples {
		if len(out.Dots) >= 1500 {
			break
		}
		x, y := s.Err[0]/s.Radius, s.Err[1]/s.Radius
		if math.Hypot(x, y) <= 2.5 {
			out.Dots = append(out.Dots, [2]float32{float32(x), float32(y)})
		}
	}
	log.Printf("replay %s: %s, %d clicks, misses %d -> %d, area %.2f x %.3f at %.2f, %.2f -> %.2f x %.3f at %.2f, %.2f",
		filepath.Base(f.path), out.Map, res.Before.Clicks, res.Before.Misses, res.After.Misses,
		out.Used.W, out.Used.H, out.Used.X, out.Used.Y, res.Area.W, res.Area.H, res.Area.X, res.Area.Y)
	return out, false
}

func firstPressed(cs []aim.Click) aim.Click {
	for _, c := range cs {
		if c.Pressed {
			return c
		}
	}
	return aim.Click{}
}

// ApplySuggestion puts the area of the advice in OpenTabletDriver.
func (a *App) ApplySuggestion(id int) error {
	a.mu.Lock()
	rep := a.report
	a.mu.Unlock()
	if rep == nil || rep.ID != id || rep.Result == nil {
		return fmt.Errorf("that advice isn't there any more")
	}
	if err := a.SetArea(rep.Result.Area); err != nil {
		return err
	}
	a.mu.Lock()
	if a.report != nil && a.report.ID == id {
		a.report.AppliedNow = true
	}
	a.mu.Unlock()
	return nil
}
