package live

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"areacoach/internal/osu"
	"areacoach/internal/otd"
)

func testMap(t *testing.T, n int, seed int64) (*osu.Beatmap, [][2]float64) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	var b strings.Builder
	b.WriteString("osu file format v14\n\n[General]\nMode: 0\nStackLeniency: 0.7\n\n[Metadata]\nTitle:T\nArtist:A\nVersion:V\n\n[Difficulty]\nHPDrainRate:5\nCircleSize:4\nOverallDifficulty:8\nApproachRate:9\nSliderMultiplier:1.4\nSliderTickRate:1\n\n[TimingPoints]\n0,300,4,2,0,50,1,0\n\n[HitObjects]\n")
	var pos [][2]float64
	for i := 0; i < n; i++ {
		x, y := 20+rng.Intn(472), 20+rng.Intn(344)
		pos = append(pos, [2]float64{float64(x), float64(y)})
		fmt.Fprintf(&b, "%d,%d,%d,1,0,0:0:0:0:\n", x, y, 1000+300*i)
	}
	bm, err := osu.ParseBeatmap([]byte(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	return bm, pos
}

// play presses the objects from..to with the map started at t0 (program
// clock), at the given speed, flipped for HR.
func playPresses(s *Session, pos [][2]float64, from, to int, t0, rate float64, hr bool, rng *rand.Rand, area otd.Rect) float64 {
	last := 0.0
	for i := from; i < to; i++ {
		x, y := pos[i][0], pos[i][1]
		if hr {
			y = 384 - y
		}
		at := t0 + float64(1000+300*i)/rate + rng.NormFloat64()*12
		s.Add(Press{At: at, X: x + rng.NormFloat64()*10, Y: y + rng.NormFloat64()*10, Area: area})
		s.Update(at + 1)
		last = at
	}
	return last
}

func TestSessionSyncsSpeedAndHR(t *testing.T) {
	bm, pos := testMap(t, 300, 1)
	s := NewSession("A - T [V]", []*osu.Beatmap{bm}, false)
	rng := rand.New(rand.NewSource(2))
	area := otd.Rect{W: 80, H: 45, X: 76, Y: 47.5}
	end := playPresses(s, pos, 0, 300, 5000, 1.5, true, rng, area)
	st := s.Status(end + 2000)
	if !st.Synced || st.Rate != 1.5 || !st.HR {
		t.Fatalf("status %+v", st)
	}
	if st.Clicks < 285 || st.Hits < 270 {
		t.Fatalf("clicks %d hits %d", st.Clicks, st.Hits)
	}
}

func TestSessionPauseAndRetry(t *testing.T) {
	bm, pos := testMap(t, 300, 3)
	s := NewSession("A - T [V]", []*osu.Beatmap{bm}, false)
	rng := rand.New(rand.NewSource(4))
	area := otd.Rect{W: 80, H: 45, X: 76, Y: 47.5}
	// first half, a pause of 8 s, the rest
	playPresses(s, pos, 0, 150, 5000, 1, false, rng, area)
	end := playPresses(s, pos, 150, 300, 13000, 1, false, rng, area)
	st := s.Status(end + 2000)
	t.Logf("after pause %+v", st)
	if !st.Synced || st.Clicks < 260 || st.Clicks > 300 {
		t.Fatalf("after pause: %+v", st)
	}
	seen := map[int]int{}
	for _, c := range s.Clicks(end + 2000) {
		seen[c.Obj]++
		if seen[c.Obj] > 1 {
			t.Fatalf("object %d counted twice", c.Obj)
		}
	}
	// a retry from the start: the same objects again, counted again
	end = playPresses(s, pos, 0, 100, end+5000, 1, false, rng, area)
	st2 := s.Status(end + 2000)
	t.Logf("after retry %+v", st2)
	if !st2.Synced || st2.Clicks < st.Clicks+80 {
		t.Fatalf("after retry: %+v (before %+v)", st2, st)
	}
}

func TestSessionLazerRate(t *testing.T) {
	bm, pos := testMap(t, 200, 5)
	s := NewSession("A - T [V]", []*osu.Beatmap{bm}, true)
	rng := rand.New(rand.NewSource(6))
	area := otd.Rect{W: 80, H: 45, X: 76, Y: 47.5}
	end := playPresses(s, pos, 0, 200, 3000, 1.25, false, rng, area)
	st := s.Status(end + 2000)
	t.Logf("lazer %+v", st)
	if !st.Synced || st.Rate < 1.24 || st.Rate > 1.26 || st.Clicks < 180 {
		t.Fatalf("status %+v", st)
	}
}

func TestSessionIgnoresRandomPresses(t *testing.T) {
	bm, _ := testMap(t, 300, 7)
	s := NewSession("A - T [V]", []*osu.Beatmap{bm}, false)
	rng := rand.New(rand.NewSource(8))
	for i := 0; i < 200; i++ {
		at := 1000 + float64(i)*170 + rng.Float64()*50
		s.Add(Press{At: at, X: rng.Float64() * 512, Y: rng.Float64() * 384})
		s.Update(at)
	}
	if st := s.Status(40000); st.Synced || st.Clicks > 0 {
		t.Fatalf("synced on noise: %+v", st)
	}
}
