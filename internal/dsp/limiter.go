package dsp

// dynLimiter is a per-band rolling-max normaliser. Each band keeps a circular
// history of recent values; the band is scaled so its window-max reaches the
// target. Unlike the global-peak AGC this evens out loudness *between* bands,
// which makes quiet bass visible when treble dominates.
type dynLimiter struct {
	hist     [][]float64 // [band][slot] recent unscaled values
	pos      int
	size     int
	maxCache []float64 // scratch
}

// newDynLimiter sizes the per-band ring buffer to cover windowSeconds at
// frameSeconds per frame. Minimum of 2 slots so the comparison is meaningful.
func newDynLimiter(numBands int, windowSeconds, frameSeconds float32) *dynLimiter {
	n := int(float64(windowSeconds) / float64(frameSeconds))
	if n < 2 {
		n = 2
	}
	l := &dynLimiter{
		hist:     make([][]float64, numBands),
		size:     n,
		maxCache: make([]float64, numBands),
	}
	for i := range l.hist {
		l.hist[i] = make([]float64, n)
	}
	return l
}

// resize rebuilds the limiter for a new window length, preserving as much
// history as fits.
func (l *dynLimiter) resize(numBands int, windowSeconds, frameSeconds float32) {
	n := int(float64(windowSeconds) / float64(frameSeconds))
	if n < 2 {
		n = 2
	}
	if n == l.size && len(l.hist) == numBands {
		return
	}
	*l = *newDynLimiter(numBands, windowSeconds, frameSeconds)
}

// update appends bands to each history ring and writes the per-band-normalised
// values (each clamped to [0, target]) to out. target is typically 255.
func (l *dynLimiter) update(bands []float64, target float64, out []float64) {
	for b, v := range bands {
		l.hist[b][l.pos] = v
	}
	l.pos = (l.pos + 1) % l.size
	for b := range bands {
		var max float64
		for _, v := range l.hist[b] {
			if v > max {
				max = v
			}
		}
		l.maxCache[b] = max
		if max < 1e-9 {
			out[b] = 0
			continue
		}
		scaled := bands[b] / max * target
		if scaled > target {
			scaled = target
		}
		out[b] = scaled
	}
}
