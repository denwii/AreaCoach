// Command fakelive plays maps against the program running in development
// and the fake OpenTabletDriver, as a player would during the diagnosis: each
// map with the area OpenTabletDriver has when it starts, from the start to
// the end, then the replay written into the osu! folder, then a wait for the
// program to set the area of the next map. It never talks to the program
// while it plays: the program only sees the replays, as with osu!.
package main

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"areacoach/internal/analysis"
	"areacoach/internal/filter"
	"areacoach/internal/osu"
	"areacoach/internal/otd"
	"areacoach/internal/sim"
)

var (
	api   = flag.String("api", "http://127.0.0.1:8765/api/", "program API")
	token = flag.String("token", "dev", "API token")
)

func getSetup() analysis.Setup {
	req, _ := http.NewRequest("GET", *api+"status", nil)
	req.Header.Set("X-Token", *token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	var st struct {
		Env struct {
			Setup analysis.Setup `json:"setup"`
		} `json:"env"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		log.Fatal(err)
	}
	return st.Env.Setup
}

func main() {
	osuDir := flag.String("osu", "", "osu! folder")
	mapsArg := flag.String("maps", "", "comma separated .osu files")
	n := flag.Int("n", 3, "maps to play")
	gap := flag.Duration("gap", 3*time.Second, "pause after the next area is set")
	speed := flag.Float64("speed", 0, "how fast a map is played against real time (0: at once)")
	var nat otd.Rect
	flag.Float64Var(&nat.W, "w", 78, "width the hand is used to")
	flag.Float64Var(&nat.H, "h", 78*1080.0/1920, "height the hand is used to")
	flag.Float64Var(&nat.X, "x", 71, "where the hand plays (X)")
	flag.Float64Var(&nat.Y, "y", 50, "where the hand plays (Y)")
	residual := flag.Float64("residual", 0.35, "share of a different area the hand never gets used to")
	absolute := flag.Float64("absolute", 0, "share of the size mismatch shown by position on the tablet")
	noReplay := flag.Int("noreplay", 0, "maps (the first ones) with no replay saved, as when failed")
	tablet := flag.String("tablet", "", "profile the tablet really uses (default: the one the program uses)")
	// the hand turns around the wrist: where it is from where the hand plays
	// (mm), how much, and where (mm to the right of it) it aims straight
	pivotX := flag.Float64("pivotx", 60, "wrist, mm to the right of where the hand plays")
	pivotY := flag.Float64("pivoty", 110, "wrist, mm below where the hand plays")
	kappa := flag.Float64("kappa", 0, "how much the movements turn around the wrist (0: not at all)")
	straight := flag.Float64("straight", 0, "the hand aims straight this many mm to the right of where it plays")
	failEvery := flag.Int("failevery", 0, "every this many maps one is failed (0: none), its replay saved with F2 in Replays")
	failAt := flag.Float64("failat", 0.5, "where in the map the failed ones end (share)")
	coach := flag.Bool("coach", false, "before every map, put in the filter values the program asks for, as from the OpenTabletDriver window (sending back its old area too)")
	forget := flag.Int("forget", 0, "with -coach: every this many maps the values are not put in (0: never)")
	tremor := flag.Float64("tremor", 0, "the pen trembles this much (mm, SD), frame to frame")
	flag.Parse()
	su := getSetup()
	if *tablet != "" {
		su.TabletName = *tablet
	}
	var maps []*osu.Beatmap
	for _, p := range strings.Split(*mapsArg, ",") {
		bm, err := osu.ParseBeatmapFile(p)
		if err != nil {
			log.Fatal(err)
		}
		maps = append(maps, bm)
	}
	hand := &sim.Hand{Natural: nat, Residual: *residual, CenterR: *residual, Fade: 2500, Absolute: *absolute}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	turn := func(pl *sim.Player) {
		if *kappa != 0 {
			pl.Pivot = [2]float64{nat.X + *pivotX, nat.Y + *pivotY}
			pl.Kappa, pl.Phi0 = *kappa, sim.PivotAngle(pl.Pivot, nat.X+*straight, nat.Y)
		}
	}
	for play := 0; play < *n; play++ {
		bm := maps[play%len(maps)]
		fail := 0.0
		if *failEvery > 0 && (play+1)%*failEvery == 0 {
			fail = *failAt
		}
		if *coach {
			if *forget > 0 && (play+1)%*forget == 0 {
				log.Printf("filter: not changing it this time")
			} else {
				followCoach(su)
			}
		}
		before := playOne(bm, su, hand, func(pl *sim.Player) { turn(pl); pl.Tremor = *tremor }, *osuDir, rng.Int63(), *speed, play < *noReplay, fail)
		if play == *n-1 {
			break
		}
		// the program sets the area of the next map after the replay
		deadline := time.Now().Add(25 * time.Second)
		for time.Now().Before(deadline) {
			if a, ok := areaNow(su); ok && moved(a, before) {
				log.Printf("next area %.1f×%.1f at %.1f,%.1f", a.W, a.H, a.X, a.Y)
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
		time.Sleep(*gap)
	}
}

// followCoach puts in the filter the program asks for, as a player does
// from the OpenTabletDriver window: Apply sends the window's settings, with
// the area it showed when it was opened (the first one seen), and the new
// filter. Then it waits for the program to put its area back.
var windowArea otd.Rect

func followCoach(su analysis.Setup) {
	req, _ := http.NewRequest("GET", *api+"calib", nil)
	req.Header.Set("X-Token", *token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("filter: %v", err)
		return
	}
	var st struct {
		Adapt *struct {
			Cur   otd.Rect `json:"cur"`
			Coach *struct {
				Active bool          `json:"active"`
				Ask    filter.Params `json:"ask"`
				AskOn  bool          `json:"askOn"`
			} `json:"coach"`
		} `json:"adapt"`
	}
	err = json.NewDecoder(resp.Body).Decode(&st)
	resp.Body.Close()
	if err != nil || st.Adapt == nil || st.Adapt.Coach == nil || !st.Adapt.Coach.Active {
		return
	}
	fc := st.Adapt.Coach
	d, err := otd.DialDaemon(time.Second)
	if err != nil {
		return
	}
	defer d.Close()
	s, err := d.GetSettings()
	if err != nil {
		return
	}
	cur, _ := otd.AreaOf(s, su.TabletName)
	if !windowArea.Valid() {
		windowArea = cur
	}
	ns, err := otd.WithRadialFollow(s, su.TabletName, otd.RadialSettings{Outer: fc.Ask.Outer, Inner: fc.Ask.Inner, Coef: fc.Ask.Coef, Knee: fc.Ask.Knee, Leak: fc.Ask.Leak}, fc.AskOn, 0)
	if err != nil {
		if errors.Is(err, otd.ErrNoRadialFollow) {
			if a, aerr := otd.AddRadialFollow(s, su.TabletName); aerr == nil {
				ns, err = otd.WithRadialFollow(a, su.TabletName, otd.RadialSettings{Outer: fc.Ask.Outer, Inner: fc.Ask.Inner, Coef: fc.Ask.Coef, Knee: fc.Ask.Knee, Leak: fc.Ask.Leak}, fc.AskOn, 0)
			}
		}
		if err != nil {
			log.Printf("filter: %v", err)
			return
		}
	}
	if stale := windowArea; stale.Valid() && moved(stale, cur) {
		ns, _ = otd.WithArea(ns, su.TabletName, stale)
	}
	if err := d.SetSettings(ns); err != nil {
		log.Printf("filter: %v", err)
		return
	}
	if fc.AskOn {
		log.Printf("filter: set outer %.2f inner %.2f coef %.2f knee %.2f leak %.2f (Apply from the window, with the %.1f×%.1f area it showed)", fc.Ask.Outer, fc.Ask.Inner, fc.Ask.Coef, fc.Ask.Knee, fc.Ask.Leak, windowArea.W, windowArea.H)
	} else {
		log.Printf("filter: off (Apply from the window, with the %.1f×%.1f area it showed)", windowArea.W, windowArea.H)
	}
	// the program puts its area back
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		if a, ok := areaNow(su); ok && !moved(a, cur) {
			log.Printf("filter: the area is back to %.1f×%.1f", a.W, a.H)
			return
		}
	}
	a, _ := areaNow(su)
	log.Printf("WARNING: the area didn't come back (now %.1f×%.1f, before %.1f×%.1f)", a.W, a.H, cur.W, cur.H)
}

func moved(a, b otd.Rect) bool {
	return math.Abs(a.W-b.W)+math.Abs(a.X-b.X)+math.Abs(a.Y-b.Y)+math.Abs(a.H-b.H) > 1e-6
}

func areaNow(su analysis.Setup) (otd.Rect, bool) {
	d, err := otd.DialDaemon(time.Second)
	if err != nil {
		return otd.Rect{}, false
	}
	defer d.Close()
	s, err := d.GetSettings()
	if err != nil {
		return otd.Rect{}, false
	}
	return otd.AreaOf(s, su.TabletName)
}

// playOne plays a map with the area OpenTabletDriver has now, and writes
// its replay. It returns that area.
func playOne(bm *osu.Beatmap, su analysis.Setup, hand *sim.Hand, turn func(*sim.Player), osuDir string, seed int64, speed float64, noReplay bool, fail float64) otd.Rect {
	objs, d := bm.Objects(0)
	title := fmt.Sprintf("%s - %s [%s]", bm.Artist, bm.Title, bm.Version)
	cur, ok := areaNow(su)
	if !ok {
		log.Fatal("the fake OpenTabletDriver doesn't answer")
	}
	setup := su
	setup.Area = cur
	pl := &sim.Player{Offset: [2]float64{0.25, -0.15}, APar: 0.55, BPar: 0.02, CPar: 50, P: 2, APerp: 0.45, BPerp: 0.012, CPerp: 27,
		TimingSD: 12, MissRate: 0.005, Rng: rand.New(rand.NewSource(seed)), Hand: hand}
	turn(pl)
	frames := pl.Play(bm, 0, setup)
	// Radial Follow, when OpenTabletDriver has it on: the replay records the
	// cursor, not the pen
	fl := ""
	if r, ok := radialNow(su); ok {
		frames = applyRadial(frames, setup, r)
		fl = fmt.Sprintf(", Radial Follow outer %.2f inner %.2f coef %.2f", r.Outer, r.Inner, r.Coef)
	}
	if fail > 0 {
		// failed there: the replay stops
		t0 := objs[0].Time
		cut := t0 + fail*bm.LengthMs()
		k := len(frames)
		for k > 0 && frames[k-1].T > cut {
			k--
		}
		frames = frames[:k]
		fl += fmt.Sprintf(", fails at %.0f%%", 100*fail)
	}
	log.Printf("playing %s (area %.1f×%.1f at %.1f,%.1f%s)", title, cur.W, cur.H, cur.X, cur.Y, fl)
	if speed > 0 {
		// the map takes its time: the area must not change meanwhile
		end := time.Now().Add(time.Duration(frames[len(frames)-1].T/speed) * time.Millisecond)
		for time.Now().Before(end) {
			if a, ok := areaNow(su); ok && moved(a, cur) {
				log.Printf("WARNING: the area changed during the map (%.1f×%.1f)", a.W, a.H)
				cur = a
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	if noReplay {
		log.Printf("end of map: no replay saved")
		return cur
	}
	time.Sleep(900 * time.Millisecond)
	_, sum := analysis.Judge(objs, d, frames)
	ts := time.Now()
	if fail > 0 {
		// F2 on the game over screen, a few seconds later
		time.Sleep(4 * time.Second)
	}
	h := osu.ReplayHeader{Mode: 0, Version: 20250301, BeatmapMD5: bm.MD5, Player: "Demo", N300: sum.N300, N100: sum.N100, N50: sum.N50,
		NMiss: sum.AimMiss + sum.NoPress, Score: 1000000, MaxCombo: sum.Heads, Timestamp: ts}
	sm := md5.Sum([]byte(fmt.Sprint("fakelive", seed)))
	h.ReplayMD5 = hex.EncodeToString(sm[:])
	b, err := osu.EncodeReplay(h, frames)
	if err != nil {
		log.Fatal(err)
	}
	p := filepath.Join(osuDir, "Data", "r", fmt.Sprintf("%s-%d.osr", bm.MD5, osu.TimeToTicks(ts)))
	if fail > 0 {
		_ = os.MkdirAll(filepath.Join(osuDir, "Replays"), 0o755)
		p = filepath.Join(osuDir, "Replays", fmt.Sprintf("Demo - %s - %s [%s] (%s) Osu.osr", bm.Artist, bm.Title, bm.Version, ts.Format("2006-01-02_15-04-05")))
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("end of map: %d aim misses, replay %s", sum.AimMiss, filepath.Base(p))
	return cur
}

// radialNow is the Radial Follow OpenTabletDriver has on now, if any.
func radialNow(su analysis.Setup) (otd.RadialFollow, bool) {
	d, err := otd.DialDaemon(time.Second)
	if err != nil {
		return otd.RadialFollow{}, false
	}
	defer d.Close()
	s, err := d.GetSettings()
	if err != nil {
		return otd.RadialFollow{}, false
	}
	b, _ := json.Marshal(s)
	st, err := otd.Parse(b)
	if err != nil {
		return otd.RadialFollow{}, false
	}
	p, ok := st.Best(su.TabletName)
	if !ok {
		return otd.RadialFollow{}, false
	}
	return p.Radial()
}

// applyRadial runs the pen path through Radial Follow at 133 reports a
// second, as the driver would, and returns the cursor at the frame times.
func applyRadial(frames []osu.Frame, su analysis.Setup, r otd.RadialFollow) []osu.Frame {
	if len(frames) < 2 {
		return frames
	}
	m := su.Mapper()
	det := m.A[0][0]*m.A[1][1] - m.A[0][1]*m.A[1][0]
	toPx := func(x, y float64) (float64, float64) {
		x -= m.B[0]
		y -= m.B[1]
		return (m.A[1][1]*x - m.A[0][1]*y) / det, (-m.A[1][0]*x + m.A[0][0]*y) / det
	}
	k := 1.0
	if !r.Tablet && su.Area.W > 0 {
		k = su.Display.W / su.Area.W
	}
	core := filter.NewCore(filter.Params{Outer: r.Outer / k, Inner: r.Inner / k, Coef: r.Coef, Knee: r.Knee, Leak: r.Leak})
	step := 1000 / 133.0
	type pt struct{ t, x, y float64 }
	var out []pt
	j := 0
	for t := frames[0].T; t <= frames[len(frames)-1].T; t += step {
		for j+1 < len(frames) && frames[j+1].T <= t {
			j++
		}
		a, b := frames[j], frames[min(j+1, len(frames)-1)]
		u := 0.0
		if b.T > a.T {
			u = (t - a.T) / (b.T - a.T)
		}
		px, py := float64(a.X)+u*float64(b.X-a.X), float64(a.Y)+u*float64(b.Y-a.Y)
		mx, my := m.Point(px, py)
		cx, cy := core.Step(mx, my)
		out = append(out, pt{t, cx, cy})
	}
	res := make([]osu.Frame, len(frames))
	j = 0
	for i, f := range frames {
		for j+1 < len(out) && out[j+1].t <= f.T {
			j++
		}
		x, y := toPx(out[j].x, out[j].y)
		res[i] = osu.Frame{T: f.T, X: float32(x), Y: float32(y), Keys: f.Keys}
	}
	return res
}
