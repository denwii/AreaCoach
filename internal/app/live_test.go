package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"areacoach/internal/geom"
	"areacoach/internal/otd"
	"areacoach/internal/platform"
)

// fakeOTD is an OpenTabletDriver daemon on the pipe the program dials.
type fakeOTD struct {
	mu       sync.Mutex
	settings map[string]any
	sets     int
}

func startFakeOTD(t *testing.T, settingsJSON string) *fakeOTD {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "acd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("TMPDIR", dir)
	f := &fakeOTD{}
	if err := json.Unmarshal([]byte(settingsJSON), &f.settings); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", filepath.Join(dir, "CoreFxPipe_OpenTabletDriver.Daemon"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeOTD) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	send := func(v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(c, "Content-Length: %d\r\n\r\n", len(b))
		c.Write(b)
	}
	for {
		n := -1
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSpace(line)
			if line == "" {
				break
			}
			if k, v, ok := strings.Cut(line, ":"); ok && k == "Content-Length" {
				n, _ = strconv.Atoi(strings.TrimSpace(v))
			}
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(r, buf); err != nil {
			return
		}
		var m map[string]any
		_ = json.Unmarshal(buf, &m)
		f.mu.Lock()
		switch m["method"] {
		case "GetSettings":
			send(map[string]any{"jsonrpc": "2.0", "id": m["id"], "result": f.settings})
		case "SetSettings":
			f.settings = m["params"].([]any)[0].(map[string]any)
			f.sets++
			send(map[string]any{"jsonrpc": "2.0", "id": m["id"], "result": nil})
		case "GetTablets":
			send(map[string]any{"jsonrpc": "2.0", "id": m["id"], "result": []any{map[string]any{"Properties": map[string]any{"Name": "Wacom CTL-472"}}}})
		default:
			send(map[string]any{"jsonrpc": "2.0", "id": m["id"], "result": nil})
		}
		f.mu.Unlock()
	}
}

func (f *fakeOTD) area() otd.Rect {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, _ := otd.AreaOf(f.settings, "Wacom CTL-472")
	return r
}

// player plays the map in real time: their hand goes where the circles
// would be with the area hand, whatever area is set; the cursor goes where
// the area set puts the pen.
type player struct {
	w     *world
	otd   *fakeOTD
	hand  otd.Rect
	start time.Time
	noise [][2]float64
	vk    [2]int
}

func newPlayer(w *world, f *fakeOTD, hand otd.Rect, start time.Time) *player {
	p := &player{w: w, otd: f, hand: hand, start: start}
	rng := rand.New(rand.NewSource(3))
	for range w.objs {
		p.noise = append(p.noise, [2]float64{rng.NormFloat64() * 1.1, rng.NormFloat64() * 1.1})
	}
	p.vk[0], _ = platform.VirtualKey("V")
	p.vk[1], _ = platform.VirtualKey("B")
	return p
}

func (p *player) setup(area otd.Rect) geom.Setup {
	return geom.Setup{TabletW: 152, TabletH: 95, Area: area, Display: otd.Rect{W: 1920, H: 1080, X: 960, Y: 540}, GameW: 1920, GameH: 1080, StretchX: 1, StretchY: 1}
}

// next is the object being aimed at now (map ms).
func (p *player) next() int {
	ms := float64(time.Since(p.start).Microseconds()) / 1000
	for i, o := range p.w.objs {
		if o[2]+40 >= ms {
			return i
		}
	}
	return len(p.w.objs) - 1
}

func (p *player) Cursor() (int, int) {
	i := p.next()
	o := p.w.objs[i]
	px, py := p.setup(p.hand).Mapper().Point(o[0], o[1])
	px += p.noise[i][0]
	py += p.noise[i][1]
	su := p.setup(p.otd.area())
	ox, oy := su.Mapper().Osu(px, py)
	sx, sy := su.OsuToScreen(ox, oy)
	return int(math.Round(sx)), int(math.Round(sy))
}

func (p *player) KeyDown(vk int) bool {
	ms := float64(time.Since(p.start).Microseconds()) / 1000
	for i, o := range p.w.objs {
		if ms >= o[2] && ms < o[2]+25 {
			return vk == p.vk[i%2]
		}
	}
	return false
}

func (p *player) FineTimer(bool) {}

func TestLiveSculptsTowardsTheHand(t *testing.T) {
	w := newWorldSpaced(t, 150)
	f := startFakeOTD(t, otdJSON)
	if err := saveSettings(w.data, Settings{OsuDir: w.osuDir, OTDPath: w.otdPath, Ratio: 0.5625, Strength: "strong"}); err != nil {
		t.Fatal(err)
	}
	old := stepGap
	stepGap = time.Second
	defer func() { stepGap = old }()
	a := New(w.data, "test")
	hand := otd.Rect{W: 80, H: 45, X: 77, Y: 47}
	pl := newPlayer(w, f, hand, time.Now().Add(500*time.Millisecond))
	a.live.window = func() (platform.Window, bool) {
		return platform.Window{Title: "osu!  - A - T [V]", W: 1920, H: 1080}, true
	}
	a.live.input = pl
	a.Run()
	defer a.Close()
	waitFor(t, "the area", func() bool { return a.State().Area != nil })
	a.SetLive(true)
	waitLong(t, "a few changes", 30*time.Second, func() bool { return len(a.State().Live.Steps) >= 3 })
	st := a.State().Live
	if st.Phase != "playing" || !st.Session.Synced || st.Session.Rate != 1 || st.Session.HR {
		t.Fatalf("live state %+v", st)
	}
	first, last := st.Steps[0], st.Steps[len(st.Steps)-1]
	t.Logf("steps: %d, first %+v -> %+v, last -> %+v, keys %v", len(st.Steps), first.From, first.To, last.To, st.Keys)
	if !(first.To.W > first.From.W && last.To.W > first.To.W && last.To.X > 76) {
		t.Fatalf("not going towards the hand: %+v", st.Steps)
	}
	if d := first.To.W - first.From.W; d > 0.02*first.From.W+0.01 {
		t.Fatalf("a change too big: %.2f", d)
	}
	if got := f.area(); !got.Same(last.To) {
		t.Fatalf("OpenTabletDriver has %+v, want %+v", got, last.To)
	}
	if last.Why == "" {
		t.Fatal("a change without its reason")
	}
	if m := a.State().Memory; m == nil || m.Clicks < 60 {
		t.Fatalf("live clicks not remembered: %+v", m)
	}
	if strings.Join(st.Keys, " ") != "V B" {
		t.Fatalf("keys %v", st.Keys)
	}
	a.SetLive(false)
	if got := otdArea(t, w.otdPath); !got.Same(last.To) && !got.Same(f.area()) {
		t.Fatalf("not saved at the end: file %+v, live %+v", got, f.area())
	}
	if err := a.LiveBack(); err != nil {
		t.Fatal(err)
	}
	if got := f.area(); got.W != 72 {
		t.Fatalf("back: %+v", got)
	}
}
