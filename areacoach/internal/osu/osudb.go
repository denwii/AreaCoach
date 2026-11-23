package osu

import (
	"errors"
	"fmt"
	"os"
)

// DBEntry is the information we need from osu!.db for one difficulty.
type DBEntry struct {
	MD5     string
	Folder  string
	File    string
	Artist  string
	Title   string
	ArtistU string
	TitleU  string
	Version string
	Creator string
	Mode    byte
}

// ReadOsuDB parses osu!.db and returns entries keyed by MD5.
func ReadOsuDB(path string) (map[string]DBEntry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseOsuDB(b)
}

func parseOsuDB(b []byte) (out map[string]DBEntry, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("unreadable osu!.db: %v", r)
		}
	}()
	r := &reader{b: b}
	version := r.i32()
	r.i32()     // folder count
	r.boolean() // account unlocked
	r.i64()     // unlock date
	r.str()     // player
	n := int(r.i32())
	if r.err != nil || n < 0 || n > 2_000_000 {
		return nil, errors.New("invalid osu!.db")
	}
	out = make(map[string]DBEntry, n)
	for i := 0; i < n; i++ {
		if version < 20191106 {
			r.i32()
		}
		e := DBEntry{}
		e.Artist = r.str()
		e.ArtistU = r.str()
		e.Title = r.str()
		e.TitleU = r.str()
		e.Creator = r.str()
		e.Version = r.str()
		r.str() // audio
		e.MD5 = r.str()
		e.File = r.str()
		r.u8()    // ranked
		r.skip(6) // counts
		r.i64()   // modified
		if version < 20140609 {
			r.skip(4)
		} else {
			r.skip(16)
		}
		r.f64() // slider velocity
		if version >= 20140609 {
			for m := 0; m < 4; m++ {
				pairs := int(r.i32())
				if pairs < 0 || pairs > 100000 {
					return nil, errors.New("osu!.db: invalid difficulty list")
				}
				for p := 0; p < pairs; p++ {
					r.u8() // 0x08
					r.i32()
					switch t := r.u8(); t {
					case 0x0c:
						r.skip(4)
					case 0x0d:
						r.skip(8)
					default:
						return nil, fmt.Errorf("osu!.db: unexpected type 0x%02x", t)
					}
				}
			}
		}
		r.skip(12) // drain, total, preview
		tps := int(r.i32())
		if tps < 0 || tps > 1_000_000 {
			return nil, errors.New("osu!.db: invalid timing points")
		}
		r.skip(17 * tps)
		r.skip(12) // difficulty id, beatmap id, thread id
		r.skip(4)  // grades
		r.skip(2)  // local offset
		r.skip(4)  // stack leniency
		e.Mode = r.u8()
		r.str() // source
		r.str() // tags
		r.skip(2)
		r.str() // font
		r.boolean()
		r.i64()
		r.boolean()
		e.Folder = r.str()
		r.i64()
		r.skip(5) // ignore sound/skin/storyboard/video/visual override
		if version < 20140609 {
			r.skip(2)
		}
		r.skip(4) // last modification
		r.u8()    // mania scroll
		if r.err != nil {
			return nil, fmt.Errorf("osu!.db cut short at map %d: %w", i, r.err)
		}
		if len(e.MD5) == 32 {
			out[e.MD5] = e
		}
	}
	return out, nil
}
