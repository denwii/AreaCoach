package sim

// Synthetic aim maps for the tests: controlled jumps in every direction and
// over the whole playfield, plus short bursts.

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"math"
	"math/rand"
	"strings"
)

// Variant is one difficulty of the synthetic test map.
type Variant struct {
	Key     string
	Name    string
	CS      float64
	MD5     string
	Content []byte
}

const (
	bpm       = 150.0
	beat      = 60000 / bpm
	leadIn    = 4 * beat
	audioName = "audio.wav"
)

type note struct {
	x, y float64
	t    float64
	nc   bool
}

type walker struct {
	rng  *rand.Rand
	x, y float64
	sect int
}

func (w *walker) jump(dist float64) (float64, float64) {
	const minX, maxX, minY, maxY = 36.0, 476.0, 36.0, 348.0
	for attempt := 0; attempt < 40; attempt++ {
		a := (float64(w.sect%16) + w.rng.Float64()*0.6 - 0.3) * 2 * math.Pi / 16
		if attempt >= 16 {
			a = w.rng.Float64() * 2 * math.Pi
		}
		nx, ny := w.x+dist*math.Cos(a), w.y+dist*math.Sin(a)
		w.sect += 5 // co-prime with 16: visits every direction
		if nx >= minX && nx <= maxX && ny >= minY && ny <= maxY {
			w.x, w.y = nx, ny
			return nx, ny
		}
	}
	// fall back towards the centre
	w.x, w.y = 256+(w.rng.Float64()-0.5)*200, 192+(w.rng.Float64()-0.5)*150
	return w.x, w.y
}

func pattern(seed int64, scale float64) []note {
	rng := rand.New(rand.NewSource(seed))
	w := &walker{rng: rng, x: 256, y: 192}
	var notes []note
	t := leadIn
	count := 0
	add := func(x, y float64) {
		notes = append(notes, note{x: math.Round(x), y: math.Round(y), t: math.Round(t), nc: count%8 == 0})
		count++
	}
	add(256, 192)
	for cycle := 0; cycle < 3; cycle++ {
		// fast jumps on 1/2
		dists := []float64{110, 170, 230, 290}
		for i := 0; i < 48; i++ {
			t += beat / 2
			x, y := w.jump(dists[i%len(dists)] * scale)
			add(x, y)
		}
		t += beat
		// wide jumps on 1/1
		for i := 0; i < 24; i++ {
			t += beat
			x, y := w.jump([]float64{260, 330}[i%2] * scale)
			add(x, y)
		}
		t += beat
		// short bursts on 1/4
		for b := 0; b < 8; b++ {
			t += beat
			x, y := w.jump(150 * scale)
			add(x, y)
			a := rng.Float64() * 2 * math.Pi
			for k := 0; k < 4; k++ {
				t += beat / 4
				nx := clampF(x+math.Cos(a)*45*scale*float64(k+1), 36, 476)
				ny := clampF(y+math.Sin(a)*45*scale*float64(k+1), 36, 348)
				add(nx, ny)
				w.x, w.y = nx, ny
			}
		}
		t += beat
	}
	return notes
}

func clampF(x, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, x)) }

func osuFile(v Variant, notes []note) []byte {
	var sb strings.Builder
	sb.WriteString("osu file format v14\r\n\r\n[General]\r\n")
	fmt.Fprintf(&sb, "AudioFilename: %s\r\nAudioLeadIn: 0\r\nPreviewTime: -1\r\nCountdown: 0\r\nSampleSet: Soft\r\nStackLeniency: 0.7\r\nMode: 0\r\nLetterboxInBreaks: 0\r\nWidescreenStoryboard: 0\r\n\r\n", audioName)
	sb.WriteString("[Editor]\r\nDistanceSpacing: 1\r\nBeatDivisor: 4\r\nGridSize: 8\r\nTimelineZoom: 1\r\n\r\n")
	sb.WriteString("[Metadata]\r\nTitle:Aim test\r\nTitleUnicode:Aim test\r\nArtist:Area Coach\r\nArtistUnicode:Area Coach\r\nCreator:Area Coach\r\n")
	fmt.Fprintf(&sb, "Version:%s\r\nSource:\r\nTags:areacoach test %s\r\nBeatmapID:0\r\nBeatmapSetID:-1\r\n\r\n", v.Name, v.Key)
	fmt.Fprintf(&sb, "[Difficulty]\r\nHPDrainRate:2\r\nCircleSize:%g\r\nOverallDifficulty:7\r\nApproachRate:9\r\nSliderMultiplier:1.4\r\nSliderTickRate:1\r\n\r\n", v.CS)
	sb.WriteString("[Events]\r\n//Background and Video events\r\n//Break Periods\r\n\r\n")
	fmt.Fprintf(&sb, "[TimingPoints]\r\n0,%g,4,2,0,40,1,0\r\n\r\n", beat)
	sb.WriteString("[HitObjects]\r\n")
	for _, n := range notes {
		typ := 1
		if n.nc {
			typ = 5
		}
		fmt.Fprintf(&sb, "%d,%d,%d,%d,0,0:0:0:0:\r\n", int(n.x), int(n.y), int(n.t), typ)
	}
	return []byte(sb.String())
}

// TestMaps builds the synthetic test maps. spacing scales all jump
// distances (1 = default).
func TestMaps(spacing float64) []Variant {
	if spacing <= 0 {
		spacing = 1
	}
	vs := []Variant{{Key: "cs4", Name: "Jumps and streams (CS 4)", CS: 4}, {Key: "cs6", Name: "Precision (CS 6)", CS: 6},
		{Key: "cs5j", Name: "Long jumps (CS 5)", CS: 5}, {Key: "cs45", Name: "Mixed rhythm (CS 4.5)", CS: 4.5}}
	notes := pattern(20261018, spacing)
	other := map[string][]note{"cs5j": pattern(77, spacing*1.12), "cs45": pattern(4242, spacing*0.92)}
	for i := range vs {
		v := &vs[i]
		ns := notes
		if o, ok := other[v.Key]; ok {
			ns = o
		}
		v.Content = osuFile(*v, ns)
		sum := md5.Sum(v.Content)
		v.MD5 = hex.EncodeToString(sum[:])
	}
	return vs
}
