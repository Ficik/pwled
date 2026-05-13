package dsp

import "math"

// bandRangesLog is the WLED-MM 16-band layout (low Hz, high Hz pairs).
// This is the table from docs/audio-sync-protocol.md and the default scale.
var bandRangesLog = [16][2]float64{
	{86, 129},
	{129, 216},
	{216, 301},
	{301, 430},
	{430, 560},
	{560, 818},
	{818, 1120},
	{1120, 1421},
	{1421, 1895},
	{1895, 2412},
	{2412, 3015},
	{3015, 3704},
	{3704, 4479},
	{4479, 7180},
	{7180, 10100},
	{10100, 17200},
}

// bandRangesFor returns the 16 band edges for the given scale. The Nyquist
// frequency caps the top band; lowHz is the bottom edge for non-log scales.
func bandRangesFor(scale string, lowHz, sampleRate float64) [16][2]float64 {
	switch scale {
	case "linear":
		return bandRangesLinear(lowHz, sampleRate/2)
	case "sqrt":
		return bandRangesSqrt(lowHz, sampleRate/2)
	default:
		// "log" or anything unknown — keep the WLED-MM table verbatim and clip
		// the top band to Nyquist so high sample-rate edge cases don't escape.
		out := bandRangesLog
		ny := sampleRate / 2
		for i := range out {
			if out[i][1] > ny {
				out[i][1] = ny
			}
			if out[i][0] > ny {
				out[i][0] = ny
			}
		}
		return out
	}
}

func bandRangesLinear(lo, hi float64) [16][2]float64 {
	var out [16][2]float64
	step := (hi - lo) / 16
	for i := 0; i < 16; i++ {
		out[i][0] = lo + float64(i)*step
		out[i][1] = lo + float64(i+1)*step
	}
	return out
}

func bandRangesSqrt(lo, hi float64) [16][2]float64 {
	// Equal divisions in sqrt(f) space — somewhere between linear and log.
	var out [16][2]float64
	srLo := math.Sqrt(lo)
	srHi := math.Sqrt(hi)
	step := (srHi - srLo) / 16
	for i := 0; i < 16; i++ {
		l := srLo + float64(i)*step
		h := srLo + float64(i+1)*step
		out[i][0] = l * l
		out[i][1] = h * h
	}
	return out
}

// computeBandBins translates band edges into FFT bin indices for the given
// FFT size and sample rate. The high bin is exclusive.
func computeBandBins(ranges [16][2]float64, sampleRate, fftSize int) [16][2]int {
	var out [16][2]int
	nbins := fftSize/2 + 1
	for b, r := range ranges {
		lo := freqToBin(r[0], sampleRate, fftSize)
		hi := freqToBin(r[1], sampleRate, fftSize)
		if hi > nbins {
			hi = nbins
		}
		if lo > hi {
			lo = hi
		}
		out[b] = [2]int{lo, hi}
	}
	return out
}
