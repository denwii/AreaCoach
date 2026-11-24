package app

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"areacoach/internal/aim"
	"areacoach/internal/calib"
	"areacoach/internal/geom"
	"areacoach/internal/osu"
	"areacoach/internal/otd"
)

const otdJSON = `{"Profiles":[{"Tablet":"Wacom CTL-472","OutputMode":{"Path":"OpenTabletDriver.Desktop.Output.AbsoluteMode"},
 "AbsoluteModeSettings":{"Display":{"Width":1920,"Height":1080,"X":960,"Y":540,"Rotation":0},
 "Tablet":{"Width":72,"Height":40.5,"X":76,"Y":47.5,"Rotation":0},"EnableClipping":true},"Filters":[]}],"Revision":"0.6.4.0"}`

// world is a fake osu! folder and OpenTabletDriver settings file.
type world struct {
	root, osuDir, otdPath, data string
	md5                         string
	objs                        [][3]float64 // x, y, time
}

func newWorld(t *testing.T) *world { return newWorldSpaced(t, 400) }

// newWorldSpaced has a map with a note every spacing ms.
func newWorldSpaced(t *testing.T, spacing int) *world {
	t.Helper()
	w := &world{root: t.TempDir()}
	w.osuDir = filepath.Join(w.root, "osu!")
	songs := filepath.Join(w.osuDir, "Songs", "1 A - T")
	for _, d := range []string{songs, filepath.Join(w.osuDir, "Data", "r")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := "Fullscreen = 1\nWidth = 1920\nHeight = 1080\nWidthFullscreen = 1920\nHeightFullscreen = 1080\nkeyOsuLeft = V\nkeyOsuRight = B\nMouseDisableButtons = 1\n"
	if err := os.WriteFile(filepath.Join(w.osuDir, "osu!.tester.cfg"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(1))
	var b strings.Builder
	b.WriteString("osu file format v14\n\n[General]\nMode: 0\nStackLeniency: 0.7\n\n[Metadata]\nTitle:T\nArtist:A\nVersion:V\n\n[Difficulty]\nHPDrainRate:5\nCircleSize:4\nOverallDifficulty:8\nApproachRate:9\nSliderMultiplier:1.4\nSliderTickRate:1\n\n[TimingPoints]\n0,400,4,2,0,50,1,0\n\n[HitObjects]\n")
	for i := 0; i < 250; i++ {
		x, y, tm := 20+rng.Intn(472), 20+rng.Intn(344), 1000+spacing*i
		w.objs = append(w.objs, [3]float64{float64(x), float64(y), float64(tm)})
		fmt.Fprintf(&b, "%d,%d,%d,1,0,0:0:0:0:\n", x, y, tm)
	}
	sum := md5.Sum([]byte(b.String()))
	w.md5 = hex.EncodeToString(sum[:])
	if err := os.WriteFile(filepath.Join(songs, "A - T (x) [V].osu"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	w.otdPath = filepath.Join(w.root, "otd", "settings.json")
	_ = os.MkdirAll(filepath.Dir(w.otdPath), 0o755)
	if err := os.WriteFile(w.otdPath, []byte(otdJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	w.data = filepath.Join(w.root, "data")
	_ = os.MkdirAll(w.data, 0o755)
	t.Setenv("LAZER_DIR", filepath.Join(w.root, "no-lazer"))
	return w
}

// replay plays the map with a hand that fits the area hand, the area used
// being used.
func (w *world) replay(t *testing.T, hand, used otd.Rect, name string) {
	t.Helper()
	su := geom.Setup{TabletW: 152, TabletH: 95, Display: otd.Rect{W: 1920, H: 1080, X: 960, Y: 540}, GameW: 1920, GameH: 1080, StretchX: 1, StretchY: 1}
	sh, su2 := su, su
	sh.Area, su2.Area = hand, used
	mh, mu := sh.Mapper(), su2.Mapper()
	rng := rand.New(rand.NewSource(2))
	var frames []osu.Frame
	n300 := 0
	for _, o := range w.objs {
		px, py := mh.Point(o[0], o[1])
		px += rng.NormFloat64() * 1.1
		py += rng.NormFloat64() * 1.1
		cx, cy := mu.Osu(px, py)
		frames = append(frames,
			osu.Frame{T: o[2] - 60, X: float32(cx - 20), Y: float32(cy)},
			osu.Frame{T: o[2] + rng.NormFloat64()*10, X: float32(cx), Y: float32(cy), Keys: osu.KeyK1 | osu.KeyM1},
			osu.Frame{T: o[2] + 60, X: float32(cx), Y: float32(cy)})
		n300++
	}
	b, err := osu.EncodeReplay(osu.ReplayHeader{Mode: 0, Version: 20250101, BeatmapMD5: w.md5, Player: "tester", ReplayMD5: name, N300: n300, Timestamp: time.Now()}, frames)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(w.osuDir, "Data", "r", w.md5+"-"+name+".osr")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-3 * time.Second)
	_ = os.Chtimes(p, old, old)
}

func waitFor(t *testing.T, what string, ok func() bool) { waitLong(t, what, 15*time.Second, ok) }

func waitLong(t *testing.T, what string, d time.Duration, ok func() bool) {
	t.Helper()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if ok() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func otdArea(t *testing.T, path string) otd.Rect {
	t.Helper()
	st, err := otd.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.Profiles[0].Area
}

func TestReplayAdviceEndToEnd(t *testing.T) {
	w := newWorld(t)
	if err := saveSettings(w.data, Settings{OsuDir: w.osuDir, OTDPath: w.otdPath, Ratio: 0.5625, Sound: true}); err != nil {
		t.Fatal(err)
	}
	a := New(w.data, "test")
	a.Run()
	defer a.Close()
	waitFor(t, "the area", func() bool { return a.State().Area != nil })
	used := otd.Rect{W: 72, H: 40.5, X: 76, Y: 47.5}
	hand := otd.Rect{W: 80, H: 45, X: 77, Y: 47}
	w.replay(t, hand, used, "r1")
	// a replay saved before the program started is not the current play
	waitFor(t, "nothing", func() bool { return true })
	time.Sleep(2500 * time.Millisecond)
	if st := a.State(); st.Report != nil {
		t.Fatalf("an old replay was taken: %+v", st.Report)
	}
	// a new play
	w.replay(t, hand, used, "r2")
	p := filepath.Join(w.osuDir, "Data", "r", w.md5+"-r2.osr")
	now := time.Now().Add(-2 * time.Second)
	_ = os.Chtimes(p, now, now)
	waitFor(t, "the advice", func() bool { return a.State().Report != nil })
	rep := a.State().Report
	if rep.Error != "" || rep.Result == nil {
		t.Fatalf("no advice: %+v", rep)
	}
	r := rep.Result
	t.Logf("advice %+v, misses %d -> %d", r.Area, r.Before.Misses, r.After.Misses)
	if math.Abs(r.Area.W-80) > 1.2 || math.Abs(r.Area.X-77) > 0.5 || math.Abs(r.Area.Y-47) > 0.5 || math.Abs(r.Area.H-r.Area.W*0.5625) > 0.002 {
		t.Fatalf("advice %+v", r.Area)
	}
	if r.After.Misses >= r.Before.Misses || rep.UsedGuess {
		t.Fatalf("report %+v", rep)
	}
	// the clicks of the play are remembered for the calibration, once
	if m := a.State().Memory; m == nil || m.Clicks < 240 || m.Clicks > 250 {
		t.Fatalf("memory: %+v", m)
	}
	if err := a.ApplySuggestion(rep.ID); err != nil {
		t.Fatal(err)
	}
	if got := otdArea(t, w.otdPath); !got.Same(r.Area) {
		t.Fatalf("settings.json has %+v", got)
	}
	if _, err := os.Stat(w.otdPath + ".areacoach-backup"); err != nil {
		t.Fatal("no backup of the settings")
	}
	if err := a.Undo(); err != nil {
		t.Fatal(err)
	}
	if got := otdArea(t, w.otdPath); !got.Same(used) {
		t.Fatalf("after undo settings.json has %+v", got)
	}
	// "analyse my latest replay" takes the newest one, whenever it was made
	if err := a.AnalyzeLatest(); err != nil {
		t.Fatal(err)
	}
	if st := a.State(); st.Report == nil || st.Report.Result == nil || st.Report.ID != rep.ID+1 {
		t.Fatalf("latest: %+v", st.Report)
	}
	if m := a.State().Memory; m == nil || m.Clicks > 250 {
		t.Fatalf("the same replay counted twice: %+v", m)
	}
}

func TestSculptMovesALittle(t *testing.T) {
	cur := otd.Rect{W: 80, H: 45, X: 76, Y: 47.5}
	lim := aim.Limits{Ratio: 0.5625, TabletW: 152, TabletH: 95}
	next, ok := sculpt(cur, otd.Rect{W: 90, H: 50.625, X: 80, Y: 47.5}, lim, "normal", 1, 1)
	if !ok || math.Abs(next.W-81.2) > 0.01 || math.Abs(next.X-76.8) > 0.01 || next.Y != 47.5 || math.Abs(next.H-next.W*0.5625) > 0.001 {
		t.Fatalf("got %+v", next)
	}
	if _, ok := sculpt(cur, otd.Rect{W: 80.05, H: 45.03, X: 76.05, Y: 47.5}, lim, "normal", 1, 1); ok {
		t.Fatal("a tiny change should not move the area")
	}
	g, _ := sculpt(cur, otd.Rect{W: 90, H: 50.625, X: 80, Y: 47.5}, lim, "gentle", 1, 1)
	s, _ := sculpt(cur, otd.Rect{W: 90, H: 50.625, X: 80, Y: 47.5}, lim, "strong", 1, 1)
	if !(g.W < next.W && next.W < s.W) {
		t.Fatalf("strengths %v %v %v", g.W, next.W, s.W)
	}
}

// TestDemoWorld writes a fake osu! folder and OpenTabletDriver file to
// $AREACOACH_DEMO (and a fresh replay when $AREACOACH_DEMO_REPLAY is set),
// to look at the interface by hand. Skipped otherwise.
func TestDemoWorld(t *testing.T) {
	dir := os.Getenv("AREACOACH_DEMO")
	if dir == "" {
		t.Skip("AREACOACH_DEMO not set")
	}
	w := newWorld(t)
	if os.Getenv("AREACOACH_DEMO_REPLAY") == "" {
		_ = os.RemoveAll(dir)
		if err := os.CopyFS(dir, os.DirFS(w.root)); err != nil {
			t.Fatal(err)
		}
		data := filepath.Join(dir, "data")
		s := Settings{OsuDir: filepath.Join(dir, "osu!"), OTDPath: filepath.Join(dir, "otd", "settings.json"), MinW: 60, MaxW: 95, Sound: true, Strength: "normal", PenTip: "auto"}
		if err := saveSettings(data, s); err != nil {
			t.Fatal(err)
		}
		return
	}
	w.osuDir = filepath.Join(dir, "osu!")
	used := otdArea(t, filepath.Join(dir, "otd", "settings.json"))
	w.replay(t, otd.Rect{W: 80, H: 45, X: 77, Y: 47}, used, fmt.Sprint(time.Now().Unix()))
	for _, e := range must(os.ReadDir(filepath.Join(w.osuDir, "Data", "r"))) {
		now := time.Now().Add(-2 * time.Second)
		_ = os.Chtimes(filepath.Join(w.osuDir, "Data", "r", e.Name()), now, now)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func TestBestAreaAndUseIt(t *testing.T) {
	w := newWorld(t)
	if err := saveSettings(w.data, Settings{OsuDir: w.osuDir, OTDPath: w.otdPath, Ratio: 0.5625}); err != nil {
		t.Fatal(err)
	}
	a := New(w.data, "test")
	a.Run()
	defer a.Close()
	waitFor(t, "the area", func() bool { return a.State().Area != nil })
	if err := a.UseBest(); err == nil {
		t.Fatal("a best area out of nothing")
	}
	// plays at a few widths: fewer misses at 74 than at 72 and 76
	cur, _ := a.current()
	key := a.memKey(cur)
	rng := rand.New(rand.NewSource(5))
	for _, wd := range []float64{72.5, 74.5, 76.5} {
		for k := 0; k < 6; k++ {
			var cs []calib.Click
			for i := 0; i < 120; i++ {
				s := 0.3
				if wd != 74.5 {
					s = 0.4
				}
				ex, ey := rng.NormFloat64()*s*36, rng.NormFloat64()*s*36
				cs = append(cs, calib.Click{Width: wd, Dist: math.Hypot(ex, ey) / 36, ErrX: ex, ErrY: ey, Radius: 36, MoveX: 150, MoveY: 0, Gap: 250, Weight: 1})
			}
			a.mem.Add(key, time.Now(), fmt.Sprint(wd, k), cs)
		}
	}
	a.mem.SetPos(key, 77.1, 47.2)
	st := a.State()
	if st.Best == nil || math.Abs(st.Best.W-74.5) > 0.01 || st.Best.X != 77.1 || math.Abs(st.Best.H-74.5*0.5625) > 0.001 || st.Reliability <= 0 {
		t.Fatalf("best %+v reliability %.2f", st.Best, st.Reliability)
	}
	if err := a.UseBest(); err != nil {
		t.Fatal(err)
	}
	if got := otdArea(t, w.otdPath); !got.Same(*st.Best) {
		t.Fatalf("settings.json has %+v, want %+v", got, *st.Best)
	}
}

func TestReliabilityDropIsExplained(t *testing.T) {
	a := New(t.TempDir(), "test")
	a.mu.Lock()
	a.noteAreaLocked(otd.Rect{W: 80, H: 45, X: 76, Y: 47.5})
	a.mu.Unlock()
	for _, c := range []struct{ rel, want float64 }{{0.5, 0}, {0.9, 0}, {0.85, 0}, {0.5, 0.9}, {0.6, 0.9}, {0.95, 0}} {
		if got := a.relDrop(c.rel); got != c.want {
			t.Fatalf("reliability %.2f: drop from %.2f, want %.2f", c.rel, got, c.want)
		}
	}
}
