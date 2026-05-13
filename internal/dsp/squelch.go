package dsp

// belowSquelch returns true if every sample's absolute value is below
// threshold. When true the caller treats the frame as silence: zeroed bands,
// zero sampleRaw, no beat. Threshold is in units of full-scale.
func belowSquelch(samples []float32, threshold float32) bool {
	if threshold <= 0 {
		return false
	}
	for _, s := range samples {
		if s > threshold || s < -threshold {
			return false
		}
	}
	return true
}
