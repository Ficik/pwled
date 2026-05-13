package dsp

import "testing"

func TestSquelchBelowThreshold(t *testing.T) {
	s := []float32{0.001, -0.001, 0.0005}
	if !belowSquelch(s, 0.01) {
		t.Error("expected silent")
	}
}

func TestSquelchAboveThreshold(t *testing.T) {
	s := []float32{0.001, 0.05, -0.001}
	if belowSquelch(s, 0.01) {
		t.Error("expected non-silent (one sample exceeds threshold)")
	}
}

func TestSquelchZeroThresholdDisabled(t *testing.T) {
	s := []float32{0, 0, 0}
	if belowSquelch(s, 0) {
		t.Error("threshold=0 should disable squelch (returns false)")
	}
}

func TestGainSaturates(t *testing.T) {
	s := []float32{0.5, -0.5, 0.1}
	applyGain(s, 4)
	for _, v := range s {
		if v > 1 || v < -1 {
			t.Errorf("sample escaped clamp: %v", v)
		}
	}
	if s[0] != 1 || s[1] != -1 {
		t.Errorf("expected saturation: got %v", s)
	}
}

func TestGainPassthrough(t *testing.T) {
	s := []float32{0.5, -0.5, 0.1}
	orig := append([]float32(nil), s...)
	applyGain(s, 1.0)
	for i := range s {
		if s[i] != orig[i] {
			t.Errorf("gain=1 should be no-op; got %v want %v", s, orig)
		}
	}
}
