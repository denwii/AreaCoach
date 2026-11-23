package live

import (
	"bytes"
	"encoding/binary"
	"math"
)

// Tone is a short soft "tick" as a WAV file: a sine of the given pitch
// fading out, with a second, quieter one a fifth above.
func Tone(hz float64, ms int, volume float64) []byte {
	const rate = 44100
	n := rate * ms / 1000
	var pcm bytes.Buffer
	for i := 0; i < n; i++ {
		t := float64(i) / rate
		attack := math.Min(1, t/0.004)
		env := attack * math.Exp(-t/(float64(ms)/1000/4))
		v := volume * env * (0.8*math.Sin(2*math.Pi*hz*t) + 0.2*math.Sin(2*math.Pi*hz*1.5*t))
		_ = binary.Write(&pcm, binary.LittleEndian, int16(math.Max(-1, math.Min(1, v))*32767))
	}
	var b bytes.Buffer
	w := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	b.WriteString("RIFF")
	w(uint32(36 + pcm.Len()))
	b.WriteString("WAVEfmt ")
	w(uint32(16))
	w(uint16(1)) // PCM
	w(uint16(1)) // mono
	w(uint32(rate))
	w(uint32(rate * 2))
	w(uint16(2))
	w(uint16(16))
	b.WriteString("data")
	w(uint32(pcm.Len()))
	b.Write(pcm.Bytes())
	return b.Bytes()
}

// The sounds of a change: lower when the area grows (the cursor gets
// slower), higher when it shrinks, in between when it only moves.
var (
	SoundGrow   = Tone(660, 140, 0.35)
	SoundShrink = Tone(990, 140, 0.35)
	SoundMove   = Tone(825, 110, 0.3)
)
