package osu

// EncodeOsuDB writes a minimal osu!.db (format 20250107+) for the given
// entries. It is used to build test and demo folders.
func EncodeOsuDB(version int32, player string, entries []DBEntry) []byte {
	w := &writer{}
	w.i32(version)
	w.i32(int32(len(entries)))
	w.u8(1)
	w.i64(0)
	w.str(player)
	w.i32(int32(len(entries)))
	for _, e := range entries {
		if version < 20191106 {
			w.i32(0)
		}
		w.str(e.Artist)
		w.str(e.Artist)
		w.str(e.Title)
		w.str(e.Title)
		w.str(e.Creator)
		w.str(e.Version)
		w.str("audio.mp3")
		w.str(e.MD5)
		w.str(e.File)
		w.u8(4)
		w.i16(100)
		w.i16(50)
		w.i16(1)
		w.i64(0)
		if version < 20140609 {
			w.raw([]byte{5, 5, 5, 5})
		} else {
			for i := 0; i < 4; i++ {
				w.i32(0x40a00000) // 5.0f
			}
		}
		w.raw(make([]byte, 8)) // slider velocity
		for m := 0; m < 4 && version >= 20140609; m++ {
			if m == 0 {
				w.i32(1)
				w.u8(0x08)
				w.i32(0)
				if version >= 20250107 {
					w.u8(0x0c)
					w.i32(0x40a00000)
				} else {
					w.u8(0x0d)
					w.raw(make([]byte, 8))
				}
			} else {
				w.i32(0)
			}
		}
		w.i32(120)
		w.i32(120000)
		w.i32(0)
		w.i32(1)
		w.raw(make([]byte, 17))
		w.i32(0)
		w.i32(0)
		w.i32(0)
		w.raw([]byte{9, 9, 9, 9})
		w.i16(0)
		w.i32(0x3f333333) // 0.7f
		w.u8(e.Mode)
		w.str("")
		w.str("")
		w.i16(0)
		w.str("")
		w.u8(0)
		w.i64(0)
		w.u8(0)
		w.str(e.Folder)
		w.i64(0)
		w.raw([]byte{0, 0, 0, 0, 0})
		if version < 20140609 {
			w.i16(0)
		}
		w.i32(0)
		w.u8(0)
	}
	w.i32(0)
	return w.b
}
