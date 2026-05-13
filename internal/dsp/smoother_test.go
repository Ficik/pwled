package dsp

import (
	"math"
	"testing"
)

// TestSmootherStepResponse drives a step input and checks that the smoother
// reaches ~63% of the step within `attackMs` (the time-constant definition).
func TestSmootherStepResponse(t *testing.T) {
	const frameMs = 10.0
	const attackMs = 100.0
	const releaseMs = 1000.0
	const target float32 = 100.0

	s := asymSmoother{}
	s.configure(attackMs, releaseMs, frameMs/1000)

	// Push step input until time = attackMs; expect state to cross ~63% of target.
	steps := int(attackMs / frameMs)
	var v float32
	for i := 0; i < steps; i++ {
		v = s.update(target)
	}
	want := float32(0.63) * target
	if math.Abs(float64(v-want)) > 5 {
		t.Errorf("attack: at t=%vms expected ~%.1f, got %.2f", attackMs, want, v)
	}
}

// TestSmootherReleaseSlowerThanAttack ensures the asymmetric branch is wired
// the right way around — release decays slower than attack rises.
func TestSmootherReleaseSlowerThanAttack(t *testing.T) {
	s := asymSmoother{}
	s.configure(50, 500, 0.01) // 10 ms frame
	for i := 0; i < 20; i++ {
		s.update(255)
	}
	// Now drop input to 0 and watch release.
	first := s.update(0)
	for i := 0; i < 4; i++ {
		s.update(0)
	}
	fifth := s.state
	// Release with τ=500ms means ~10% drop per 50ms = 5 frames.
	// Should still be > 200 after 50ms.
	if fifth < 200 {
		t.Errorf("release too fast: state after 50ms = %.1f, expected > 200", fifth)
	}
	// Sanity: state should have moved at all.
	if first == 255 {
		t.Errorf("release should have dropped state below 255, got %.1f", first)
	}
}

func TestAlphaFromTau(t *testing.T) {
	// τ=Δt: 1 - exp(-1) ≈ 0.632.
	got := alphaFromTau(10, 0.01)
	if math.Abs(float64(got)-0.632) > 0.01 {
		t.Errorf("α(τ=10ms, Δt=10ms): got %.3f, want ≈0.632", got)
	}
	// Tiny τ → near 1.
	if alphaFromTau(0.001, 0.01) < 0.99 {
		t.Error("very small tau should give α ≈ 1")
	}
	// Zero τ → 1 (no smoothing).
	if alphaFromTau(0, 0.01) != 1 {
		t.Error("zero tau should give α=1")
	}
}
