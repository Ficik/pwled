package dsp

import "math"

// asymSmoother is a one-pole filter with separate attack and release time
// constants. It tracks a single scalar state. α values are derived from time
// constants and the audio frame interval via 1 - exp(-Δt/τ).
type asymSmoother struct {
	state    float32
	attackA  float32 // smoothing coefficient when input rises
	releaseA float32 // smoothing coefficient when input falls
}

// configure recomputes the α coefficients for the given time constants.
// frameSeconds is the interval between successive update() calls.
func (s *asymSmoother) configure(attackMs, releaseMs, frameSeconds float32) {
	s.attackA = alphaFromTau(attackMs, frameSeconds)
	s.releaseA = alphaFromTau(releaseMs, frameSeconds)
}

// update advances the filter by one frame and returns the new state.
func (s *asymSmoother) update(in float32) float32 {
	a := s.releaseA
	if in >= s.state {
		a = s.attackA
	}
	s.state = s.state + a*(in-s.state)
	return s.state
}

// asymSmootherN is a vector version of asymSmoother sharing one (attack,
// release) pair across N independent state variables.
type asymSmootherN struct {
	state    []float32
	attackA  float32
	releaseA float32
}

func newAsymSmootherN(n int) asymSmootherN {
	return asymSmootherN{state: make([]float32, n)}
}

func (s *asymSmootherN) configure(attackMs, releaseMs, frameSeconds float32) {
	s.attackA = alphaFromTau(attackMs, frameSeconds)
	s.releaseA = alphaFromTau(releaseMs, frameSeconds)
}

// update advances every channel by one frame, writing into out (which must be
// the same length as the internal state).
func (s *asymSmootherN) update(in, out []float32) {
	for i, v := range in {
		a := s.releaseA
		if v >= s.state[i] {
			a = s.attackA
		}
		s.state[i] = s.state[i] + a*(v-s.state[i])
		out[i] = s.state[i]
	}
}

// alphaFromTau converts a one-pole time constant in milliseconds to the
// per-frame mixing coefficient α such that the filter reaches ~63 % of a step
// in tau ms. Clamped to (0, 1].
func alphaFromTau(tauMs, frameSeconds float32) float32 {
	if tauMs <= 0 {
		return 1
	}
	a := float32(1 - math.Exp(-float64(frameSeconds)/(float64(tauMs)/1000)))
	if a <= 0 {
		return 1e-4
	}
	if a > 1 {
		return 1
	}
	return a
}
