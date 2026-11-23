package platform

import "testing"

func TestMapTitle(t *testing.T) {
	cases := map[string]string{
		"osu!  - Camellia - Exit This Earth's Atomosphere [Evolution]": "Camellia - Exit This Earth's Atomosphere [Evolution]",
		"osu! - xi - FREEDOM DiVE [FOUR DIMENSIONS]":                   "xi - FREEDOM DiVE [FOUR DIMENSIONS]",
		"osu!":                          "",
		"osu!cuttingedge b20251010":     "",
		"Area Coach":                    "",
		"osu! - not a map, no brackets": "",
	}
	for in, want := range cases {
		if got := MapTitle(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestVirtualKey(t *testing.T) {
	cases := map[string]int{"Z": 0x5A, "v": 0x56, "D1": 0x31, "7": 0x37, "LeftShift": 0xA0, "Space": 0x20, "F12": 0x7B, "NumPad3": 0x63, "OemComma": 0xBC, "MouseLeft": 1}
	for in, want := range cases {
		if got, ok := VirtualKey(in); !ok || got != want {
			t.Errorf("%q: got %#x %v, want %#x", in, got, ok, want)
		}
	}
	if _, ok := VirtualKey("NoSuchKey"); ok {
		t.Error("unknown key accepted")
	}
}
