// Package osu reads osu! stable files: replays (.osr), beatmaps (.osu),
// the beatmap database (osu!.db) and the user configuration.
package osu

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
	"time"
)

// reader is a small little-endian reader with error latching, used for the
// binary formats of osu! stable (.osr, osu!.db, scores.db).
type reader struct {
	b   []byte
	pos int
	err error
}

var errShort = errors.New("truncated file")

func (r *reader) need(n int) bool {
	if r.err != nil {
		return false
	}
	if n < 0 || r.pos+n > len(r.b) {
		r.err = errShort
		return false
	}
	return true
}

func (r *reader) u8() byte {
	if !r.need(1) {
		return 0
	}
	v := r.b[r.pos]
	r.pos++
	return v
}

func (r *reader) boolean() bool { return r.u8() != 0 }

func (r *reader) i16() int16 {
	if !r.need(2) {
		return 0
	}
	v := int16(binary.LittleEndian.Uint16(r.b[r.pos:]))
	r.pos += 2
	return v
}

func (r *reader) i32() int32 {
	if !r.need(4) {
		return 0
	}
	v := int32(binary.LittleEndian.Uint32(r.b[r.pos:]))
	r.pos += 4
	return v
}

func (r *reader) i64() int64 {
	if !r.need(8) {
		return 0
	}
	v := int64(binary.LittleEndian.Uint64(r.b[r.pos:]))
	r.pos += 8
	return v
}

func (r *reader) f64() float64 {
	if !r.need(8) {
		return 0
	}
	v := math.Float64frombits(binary.LittleEndian.Uint64(r.b[r.pos:]))
	r.pos += 8
	return v
}

func (r *reader) uleb() int {
	var res, shift uint
	for {
		if !r.need(1) {
			return 0
		}
		c := r.b[r.pos]
		r.pos++
		res |= uint(c&0x7f) << shift
		if c&0x80 == 0 {
			break
		}
		shift += 7
		if shift > 35 {
			r.err = errors.New("invalid ULEB128")
			return 0
		}
	}
	return int(res)
}

// str reads an osu! string: 0x00 = empty, 0x0b + ULEB128 length + UTF-8.
func (r *reader) str() string {
	t := r.u8()
	if r.err != nil {
		return ""
	}
	switch t {
	case 0x00:
		return ""
	case 0x0b:
		n := r.uleb()
		if !r.need(n) {
			return ""
		}
		s := string(r.b[r.pos : r.pos+n])
		r.pos += n
		return s
	default:
		r.err = errors.New("invalid string")
		return ""
	}
}

func (r *reader) bytes(n int) []byte {
	if !r.need(n) {
		return nil
	}
	v := r.b[r.pos : r.pos+n]
	r.pos += n
	return v
}

func (r *reader) skip(n int) {
	if r.need(n) {
		r.pos += n
	}
}

// TicksToTime converts .NET DateTime ticks (100 ns since 0001-01-01) to time.Time.
func TicksToTime(ticks int64) time.Time {
	const unixEpochTicks = 621355968000000000
	if ticks <= unixEpochTicks {
		return time.Time{}
	}
	d := ticks - unixEpochTicks
	return time.Unix(d/10000000, (d%10000000)*100).UTC()
}

// TimeToTicks is the inverse of TicksToTime.
func TimeToTicks(t time.Time) int64 {
	const unixEpochTicks = 621355968000000000
	return unixEpochTicks + t.UnixNano()/100
}

// writer helpers used to synthesize files (tests and the calibration tools).
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

var _ = io.EOF
