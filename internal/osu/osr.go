package osu

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ulikunitz/xz/lzma"
)

// Mod bits (osu! stable).
const (
	ModNoFail      = 1
	ModEasy        = 2
	ModTouchDevice = 4
	ModHidden      = 8
	ModHardRock    = 16
	ModSuddenDeath = 32
	ModDoubleTime  = 64
	ModRelax       = 128
	ModHalfTime    = 256
	ModNightcore   = 512
	ModFlashlight  = 1024
	ModAutoplay    = 2048
	ModSpunOut     = 4096
	ModAutopilot   = 8192
	ModPerfect     = 16384
	ModCinema      = 4194304
	ModTarget      = 8388608
	ModScoreV2     = 536870912
)

// Key bits in replay frames.
const (
	KeyM1    = 1
	KeyM2    = 2
	KeyK1    = 4
	KeyK2    = 8
	KeySmoke = 16
)

// Frame is one cursor sample of a replay, in song time (ms) and osu!pixels.
type Frame struct {
	T    float64
	X, Y float32
	Keys int32
}

// ReplayHeader holds the score information of a replay.
type ReplayHeader struct {
	Mode       byte
	Version    int32
	BeatmapMD5 string
	Player     string
	ReplayMD5  string
	N300       int
	N100       int
	N50        int
	NGeki      int
	NKatu      int
	NMiss      int
	Score      int64
	MaxCombo   int
	Perfect    bool
	Mods       uint32
	Timestamp  time.Time
	OnlineID   int64
	compressed []byte
}

// Replay is a fully decoded replay.
type Replay struct {
	ReplayHeader
	Frames []Frame
}

// ModString renders mods in the usual short form ("HDHR").
func ModString(m uint32) string {
	type pair struct {
		bit  uint32
		name string
	}
	order := []pair{{ModEasy, "EZ"}, {ModNoFail, "NF"}, {ModHalfTime, "HT"}, {ModHidden, "HD"},
		{ModHardRock, "HR"}, {ModSuddenDeath, "SD"}, {ModPerfect, "PF"}, {ModNightcore, "NC"},
		{ModDoubleTime, "DT"}, {ModFlashlight, "FL"}, {ModRelax, "RX"}, {ModAutopilot, "AP"},
		{ModSpunOut, "SO"}, {ModTouchDevice, "TD"}, {ModScoreV2, "V2"}}
	var sb strings.Builder
	for _, p := range order {
		if m&p.bit == 0 {
			continue
		}
		if p.bit == ModDoubleTime && m&ModNightcore != 0 {
			continue
		}
		if p.bit == ModSuddenDeath && m&ModPerfect != 0 {
			continue
		}
		sb.WriteString(p.name)
	}
	return sb.String()
}

// RateOf returns the playback rate implied by the mods.
func RateOf(m uint32) float64 {
	switch {
	case m&(ModDoubleTime|ModNightcore) != 0:
		return 1.5
	case m&ModHalfTime != 0:
		return 0.75
	}
	return 1
}

func parseReplayHeader(b []byte) (*ReplayHeader, error) {
	r := &reader{b: b}
	h := &ReplayHeader{}
	h.Mode = r.u8()
	h.Version = r.i32()
	h.BeatmapMD5 = r.str()
	h.Player = r.str()
	h.ReplayMD5 = r.str()
	h.N300 = int(r.i16())
	h.N100 = int(r.i16())
	h.N50 = int(r.i16())
	h.NGeki = int(r.i16())
	h.NKatu = int(r.i16())
	h.NMiss = int(r.i16())
	h.Score = int64(r.i32())
	h.MaxCombo = int(r.i16())
	h.Perfect = r.boolean()
	h.Mods = uint32(r.i32())
	_ = r.str() // life bar
	h.Timestamp = TicksToTime(r.i64())
	n := int(r.i32())
	if r.err == nil && n > 0 {
		h.compressed = r.bytes(n)
	} else if n < 0 {
		return nil, errors.New("invalid replay data length")
	}
	if r.err == nil && r.pos+8 <= len(b) {
		h.OnlineID = r.i64()
		r.err = nil
	}
	if r.err != nil {
		return nil, fmt.Errorf("unreadable replay: %w", r.err)
	}
	if h.Mode > 3 || len(h.BeatmapMD5) != 32 {
		return nil, errors.New("the file doesn't look like an osu! replay")
	}
	return h, nil
}

// ReadReplay parses and decompresses a replay file.
func ReadReplay(path string) (*Replay, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseReplay(b)
}

// ParseReplay decodes a replay from memory.
func ParseReplay(b []byte) (*Replay, error) {
	h, err := parseReplayHeader(b)
	if err != nil {
		return nil, err
	}
	rep := &Replay{ReplayHeader: *h}
	if len(h.compressed) == 0 {
		return rep, nil
	}
	frames, err := DecodeFrames(h.compressed)
	if err != nil {
		return nil, err
	}
	rep.Frames = frames
	rep.compressed = nil
	return rep, nil
}

// DecodeFrames decompresses LZMA replay data and parses the frames.
func DecodeFrames(compressed []byte) ([]Frame, error) {
	zr, err := lzma.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, fmt.Errorf("replay data can't be decompressed: %w", err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil && len(raw) == 0 {
		return nil, fmt.Errorf("corrupt replay data: %w", err)
	}
	return parseFrames(string(raw)), nil
}

func parseFrames(s string) []Frame {
	parts := strings.Split(s, ",")
	frames := make([]Frame, 0, len(parts))
	t := 0.0
	for i, p := range parts {
		if p == "" {
			continue
		}
		f := strings.Split(p, "|")
		if len(f) < 4 {
			continue
		}
		w, err1 := strconv.ParseFloat(f[0], 64)
		x, err2 := strconv.ParseFloat(f[1], 32)
		y, err3 := strconv.ParseFloat(f[2], 32)
		kf, err4 := strconv.ParseFloat(f[3], 64)
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
			continue
		}
		if w == -12345 {
			continue // RNG seed frame
		}
		t += w
		if i < 2 && x == 256 && y == -500 {
			continue
		}
		if w < 0 {
			continue
		}
		frames = append(frames, Frame{T: t, X: float32(x), Y: float32(y), Keys: int32(kf)})
	}
	return frames
}
