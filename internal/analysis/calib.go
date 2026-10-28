package analysis

import (
	"time"

	"areacoach/internal/otd"
)

// CalibPoint is one block of the automatic calibration: one test map played
// with one area.
type CalibPoint struct {
	Index   int      `json:"index"`
	Area    otd.Rect `json:"area"`
	Map     string   `json:"map"`
	Notes   int      `json:"notes"`
	Err     float64  `json:"err"` // RMS distance from the centre, in radii
	ErrX    float64  `json:"errX,omitempty"`
	ErrY    float64  `json:"errY,omitempty"`
	ErrFilt float64  `json:"errFilt"` // the same with the recommended filter (0: unknown)
	Miss    float64  `json:"miss"`    // clicks outside the circle per 1000
	DX      float64  `json:"dx"`      // mean landing offset on the tablet (mm)
	DY      float64  `json:"dy"`
	// in-game calibration: the window of a play
	Play  int     `json:"play,omitempty"`
	Speed float64 `json:"speed,omitempty"` // log mean speed of the notes (difficulty)
	Jump  float64 `json:"jump,omitempty"`  // size change from the previous area (|log ratio|)
	Move  float64 `json:"move,omitempty"`  // centre move from the previous area (mm)
	Gain  float64 `json:"gain,omitempty"`  // over/undershoot of the jumps in the window (|share|)
	kw    float64 // weight in a local fit (0 = 1)
	From  float64 `json:"from,omitempty"` // map ms
	To    float64 `json:"to,omitempty"`
	// adaptive diagnosis: the signed over/undershoot of the jumps, as the log
	// of the movement made over the one needed (+ = too long), on both axes
	// together and per axis, with standard errors; the standard errors of the
	// landing offset (mm)
	G   float64   `json:"g,omitempty"`
	SG  float64   `json:"sg,omitempty"`
	GX  float64   `json:"gx,omitempty"`
	SGX float64   `json:"sgx,omitempty"`
	GY  float64   `json:"gy,omitempty"`
	SGY float64   `json:"sgy,omitempty"`
	SDX float64   `json:"sdx,omitempty"`
	SDY float64   `json:"sdy,omitempty"`
	At  time.Time `json:"at"`            // wall time at the end of the stretch
	Src string    `json:"src,omitempty"` // live (measured while playing), replay, play (a normal play)
	Sec float64   `json:"sec,omitempty"` // length of the stretch (s)
	// clicks outside the circle and at its edge (beyond 80% of the radius)
	AimMiss int `json:"aimMiss,omitempty"`
	Edge    int `json:"edge,omitempty"`
	// which circles counted as aim misses (for the log)
	MissInfo string `json:"-"`
	// how the clicks scatter, against what the noise model expects
	NS *NoiseSum `json:"ns,omitempty"`
	// how the movements came out turned, across the tablet
	Rot *RotSum `json:"rot,omitempty"`
	// how far the paths between the circles bent from the straight line, as
	// a share of the jump, over LineN jumps of the map (see PathLines)
	Line  float64 `json:"line,omitempty"`
	LineN int     `json:"lineN,omitempty"`
}
