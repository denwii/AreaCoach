package server

import "testing"

func TestParseRange(t *testing.T) {
	for _, tc := range []struct {
		in     string
		lo, hi float64
		err    bool
	}{
		{"", 0, 0, false},
		{"110 - 150", 110, 150, false},
		{"110-150", 110, 150, false},
		{"110 150", 110, 150, false},
		{"110, 150", 110, 150, false},
		{"110,150", 110, 150, false},
		{"from 110 to 150", 110, 150, false},
		{"110.5 - 150", 110.5, 150, false},
		{"110,5 - 150", 110.5, 150, false},
		{"120", 120, 0, false},
		{"150 - 110", 110, 150, false},
		{"110 - 112", 0, 0, true},
		{"1 2 3", 0, 0, true},
	} {
		lo, hi, err := parseRange(tc.in)
		if (err != nil) != tc.err || (!tc.err && (lo != tc.lo || hi != tc.hi)) {
			t.Errorf("%q: %v %v %v, want %v %v (error %v)", tc.in, lo, hi, err, tc.lo, tc.hi, tc.err)
		}
	}
}
