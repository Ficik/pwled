package dsp

import "testing"

// TestLimiterPerBandIndependence verifies the per-band rolling-max evens out
// loudness between bands: a quiet band stays at its own scale even when a
// neighbour blows past it.
func TestLimiterPerBandIndependence(t *testing.T) {
	l := newDynLimiter(NumBands, 1.0, 0.01) // 1 s window, 10 ms frames
	out := make([]float64, NumBands)

	loud := make([]float64, NumBands)
	loud[0] = 1000 // band 0 dominates
	loud[5] = 10   // band 5 is much quieter
	for i := 0; i < 50; i++ {
		l.update(loud, 255, out)
	}
	if out[0] < 250 {
		t.Errorf("loud band should saturate near target: got %.1f", out[0])
	}
	// Without per-band logic, band 5 would be (10/1000)*255 ≈ 2.5.
	// With per-band logic, band 5's max is 10, so it normalises to 255.
	if out[5] < 250 {
		t.Errorf("quiet band should also reach target via per-band normalisation: got %.1f", out[5])
	}
}

func TestLimiterClampsToTarget(t *testing.T) {
	l := newDynLimiter(NumBands, 0.5, 0.01)
	out := make([]float64, NumBands)
	in := make([]float64, NumBands)
	in[3] = 1
	l.update(in, 255, out)
	for _, v := range out {
		if v > 255 {
			t.Errorf("output exceeded target: %.1f", v)
		}
	}
}

func TestLimiterResize(t *testing.T) {
	l := newDynLimiter(NumBands, 1.0, 0.01)
	if l.size != 100 {
		t.Errorf("expected size 100, got %d", l.size)
	}
	l.resize(NumBands, 2.0, 0.01)
	if l.size != 200 {
		t.Errorf("after resize expected size 200, got %d", l.size)
	}
	// No-op resize must not mutate.
	prev := l
	l.resize(NumBands, 2.0, 0.01)
	if l.size != prev.size {
		t.Errorf("no-op resize changed size: %d -> %d", prev.size, l.size)
	}
}
