package dsp

// applyGain multiplies samples in-place by g, saturating at ±1.0.
// A gain of 1.0 is a no-op fast-path.
func applyGain(samples []float32, g float32) {
	if g == 1.0 {
		return
	}
	for i, s := range samples {
		v := s * g
		if v > 1 {
			v = 1
		} else if v < -1 {
			v = -1
		}
		samples[i] = v
	}
}
