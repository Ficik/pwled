package dsp

import (
	"sort"
	"time"
)

// beat detects transients using short-time energy compared to a local average.
// Optional median gate makes detection more conservative — kills hi-hat
// over-trigger by also requiring energy > 1.4 × median(history).
type beat struct {
	history       [43]float64 // ~1 s of history at 23 ms/frame
	pos           int
	lastDetect    time.Time
	thresholdMult float64
	minInterval   time.Duration
	medianGate    bool

	medianScratch [43]float64
}

// configure updates detector settings. Safe to call between frames.
func (b *beat) configure(thresholdMult float32, minInterval time.Duration, medianGate bool) {
	b.thresholdMult = float64(thresholdMult)
	b.minInterval = minInterval
	b.medianGate = medianGate
}

func (b *beat) detect(samples []float32) bool {
	var energy float64
	for _, s := range samples {
		energy += float64(s) * float64(s)
	}
	if len(samples) > 0 {
		energy /= float64(len(samples))
	}

	var avg float64
	for _, e := range b.history {
		avg += e
	}
	avg /= float64(len(b.history))

	b.history[b.pos] = energy
	b.pos = (b.pos + 1) % len(b.history)

	if avg < 1e-10 {
		return false
	}
	if energy <= b.thresholdMult*avg {
		return false
	}
	if b.medianGate {
		copy(b.medianScratch[:], b.history[:])
		sort.Float64s(b.medianScratch[:])
		median := b.medianScratch[len(b.medianScratch)/2]
		if energy <= 1.4*median {
			return false
		}
	}
	now := time.Now()
	if now.Sub(b.lastDetect) < b.minInterval {
		return false
	}
	b.lastDetect = now
	return true
}
