package dsp

import "math"

// agc tracks a decaying global peak across all bands and rescales them to
// reach targetPeak. Modes choose a default release time constant matching
// WLED's three AGC presets.
type agc struct {
	peak         float64
	decayPerStep float64
	target       float64
}

// configure recomputes the per-frame decay multiplier from a release time
// constant in seconds. mode is consulted only when releaseSeconds <= 0.
func (a *agc) configure(mode string, releaseSeconds, targetPeak, frameSeconds float32) {
	if releaseSeconds <= 0 {
		// WLED tunings, mapped to seconds-to-decay-by-1/e:
		//   normal = 8 s, vivid = 4 s, lazy = 16 s, off = no decay.
		switch mode {
		case "vivid":
			releaseSeconds = 4
		case "lazy":
			releaseSeconds = 16
		case "off":
			releaseSeconds = 0
		default:
			releaseSeconds = 8
		}
	}
	if releaseSeconds <= 0 {
		a.decayPerStep = 1.0 // no decay
	} else {
		a.decayPerStep = math.Exp(-float64(frameSeconds) / float64(releaseSeconds))
	}
	a.target = float64(targetPeak)
	if a.target <= 0 {
		a.target = 220
	}
}

// updateBands rescales bands so that the tracked peak reaches a.target,
// writing into out. When disabled (passthrough=true) the bands are clamped to
// [0, target] and returned unchanged otherwise.
func (a *agc) updateBands(bands []float64, out []float64, passthrough bool) {
	var max float64
	for _, v := range bands {
		if v > max {
			max = v
		}
	}
	if passthrough {
		for i, v := range bands {
			if v > a.target {
				v = a.target
			}
			out[i] = v
		}
		return
	}
	if max > a.peak {
		a.peak = max
	} else {
		a.peak *= a.decayPerStep
	}
	if a.peak < 1e-9 {
		for i := range out {
			out[i] = 0
		}
		return
	}
	for i, v := range bands {
		s := v / a.peak * a.target
		if s > a.target {
			s = a.target
		}
		out[i] = s
	}
}

// normMag scales a raw FFT bin magnitude using the same peak as updateBands so
// the magnitude field stays consistent with band scaling. Caps at ~4080 (the
// WLED-side ceiling effects assume).
func (a *agc) normMag(mag float64, passthrough bool) float32 {
	if passthrough {
		if mag > 4080 {
			return 4080
		}
		return float32(mag)
	}
	if a.peak < 1e-9 {
		return 0
	}
	v := mag / a.peak * 4080
	if v > 4080 {
		v = 4080
	}
	return float32(v)
}
