package live

import (
	"runtime"
	"sync"
	"time"
)

// Input reads the cursor and the keys (the real one is the system's).
type Input interface {
	Cursor() (x, y int)
	KeyDown(vk int) bool
	FineTimer(on bool)
}

// RawPress is a key going down: when (ms on the sampler clock) and where
// the cursor was (screen pixels).
type RawPress struct {
	At   float64
	X, Y int
}

// Sampler looks at the keys a thousand times a second and notes every press
// with the cursor position. It only reads their state, as key overlays do.
type Sampler struct {
	in    Input
	start time.Time
	mu    sync.Mutex
	keys  []int
	out   []RawPress
	stop  chan struct{}
	done  chan struct{}
}

// StartSampler starts looking at the keys.
func StartSampler(in Input, keys []int) *Sampler {
	s := &Sampler{in: in, start: time.Now(), keys: keys, stop: make(chan struct{}), done: make(chan struct{})}
	go s.run()
	return s
}

// Now is the sampler clock (ms).
func (s *Sampler) Now() float64 { return float64(time.Since(s.start).Microseconds()) / 1000 }

// SetKeys changes the keys looked at.
func (s *Sampler) SetKeys(keys []int) {
	s.mu.Lock()
	s.keys = append([]int{}, keys...)
	s.mu.Unlock()
}

// Drain returns the presses seen since the last call.
func (s *Sampler) Drain() []RawPress {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.out
	s.out = nil
	return out
}

// Stop ends the sampling.
func (s *Sampler) Stop() {
	close(s.stop)
	<-s.done
}

func (s *Sampler) run() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	s.in.FineTimer(true)
	defer s.in.FineTimer(false)
	defer close(s.done)
	held := map[int]bool{}
	for {
		select {
		case <-s.stop:
			return
		default:
		}
		s.mu.Lock()
		keys := s.keys
		s.mu.Unlock()
		for _, k := range keys {
			down := s.in.KeyDown(k)
			if down && !held[k] {
				x, y := s.in.Cursor()
				p := RawPress{At: s.Now(), X: x, Y: y}
				s.mu.Lock()
				if len(s.out) < 100000 {
					s.out = append(s.out, p)
				}
				s.mu.Unlock()
			}
			held[k] = down
		}
		time.Sleep(time.Millisecond)
	}
}
