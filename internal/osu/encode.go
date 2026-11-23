package osu

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ulikunitz/xz/lzma"
)

// writer writes the binary values osu! files use.
type writer struct{ b []byte }

func (w *writer) u8(v byte)    { w.b = append(w.b, v) }
func (w *writer) i16(v int16)  { w.b = binary.LittleEndian.AppendUint16(w.b, uint16(v)) }
func (w *writer) i32(v int32)  { w.b = binary.LittleEndian.AppendUint32(w.b, uint32(v)) }
func (w *writer) i64(v int64)  { w.b = binary.LittleEndian.AppendUint64(w.b, uint64(v)) }
func (w *writer) raw(p []byte) { w.b = append(w.b, p...) }
func (w *writer) str(s string) {
	if s == "" {
		w.u8(0)
		return
	}
	w.u8(0x0b)
	n := uint(len(s))
	for {
		c := byte(n & 0x7f)
		n >>= 7
		if n != 0 {
			c |= 0x80
		}
		w.u8(c)
		if n == 0 {
			break
		}
	}
	w.raw([]byte(s))
}

// EncodeReplay builds a .osr file (for tests and tools).
func EncodeReplay(h ReplayHeader, frames []Frame) ([]byte, error) {
	var sb strings.Builder
	sb.WriteString("0|256|-500|0,-1|256|-500|0,")
	last := -1.0
	for _, f := range frames {
		d := f.T - last
		last = f.T
		fmt.Fprintf(&sb, "%d|%.4f|%.4f|%d,", int64(d+0.5), f.X, f.Y, f.Keys)
	}
	sb.WriteString("-12345|0|0|0,")
	var cb bytes.Buffer
	zw, err := lzma.NewWriter(&cb)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write([]byte(sb.String())); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	w := &writer{}
	w.u8(h.Mode)
	w.i32(h.Version)
	w.str(h.BeatmapMD5)
	w.str(h.Player)
	w.str(h.ReplayMD5)
	w.i16(int16(h.N300))
	w.i16(int16(h.N100))
	w.i16(int16(h.N50))
	w.i16(int16(h.NGeki))
	w.i16(int16(h.NKatu))
	w.i16(int16(h.NMiss))
	w.i32(int32(h.Score))
	w.i16(int16(h.MaxCombo))
	if h.Perfect {
		w.u8(1)
	} else {
		w.u8(0)
	}
	w.i32(int32(h.Mods))
	w.str("")
	w.i64(TimeToTicks(h.Timestamp))
	w.i32(int32(cb.Len()))
	w.raw(cb.Bytes())
	w.i64(h.OnlineID)
	if h.Version >= LazerVersion {
		js, _ := json.Marshal(map[string]any{"mods": h.LazerMods, "client_version": "test"})
		var lb bytes.Buffer
		lw, err := lzma.NewWriter(&lb)
		if err != nil {
			return nil, err
		}
		_, _ = lw.Write(js)
		if err := lw.Close(); err != nil {
			return nil, err
		}
		w.i32(int32(lb.Len()))
		w.raw(lb.Bytes())
	}
	return w.b, nil
}
