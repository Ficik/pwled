package dsp

import (
	"math"
	"time"

	"gonum.org/v1/gonum/dsp/fourier"

	"pwled/internal/audio"
	"pwled/internal/config"
	"pwled/internal/monitor"
)

const (
	// FFTSize is the analysis window length; must be a power of two.
	// 2048 samples at 44100 Hz ≈ 46 ms.  With a 1024-sample hop we get
	// ~50 % overlap and one packet per ~23 ms, matching WLED's 10–25 ms target.
	FFTSize = 2048
	// NumBands matches WLED's NUM_GEQ_CHANNELS.
	NumBands = 16
)

// Analysis is the result of one processed audio frame.
type Analysis struct {
	SampleRaw  float32
	SampleSmth float32
	SamplePeak bool
	FFTResult  [NumBands]uint8
	Magnitude  float32
	MajorPeak  float32
	Active     bool // true while audio energy is above the activity threshold
}

// Processor performs the audio-reactive DSP pipeline on successive Frames.
// It is not safe for concurrent use, but the underlying *config.Store is, so
// settings can be hot-swapped from any goroutine.
type Processor struct {
	sampleRate   int
	frameSeconds float32 // expected interval between Process() calls

	cfg     *config.Store
	applied configKey // last config we reconfigured filters from

	fft     *fourier.FFT
	hannWin []float64

	ring    []float32
	ringPos int

	scratch []float32 // gain/squelch work buffer the size of one frame
	input   []float64
	coeffs  []complex128
	power   []float64

	bands       [NumBands]float64 // pre-AGC band magnitudes
	postAGC     []float64
	postLimiter []float64
	bandSmIn    []float32
	bandSmOut   []float32

	bandBins [NumBands][2]int

	agc          agc
	limiter      *dynLimiter
	bandSmoother asymSmootherN
	sampleSm     asymSmoother
	beat         beat
	activity     monitor.Activity
}

// configKey tracks which config fields the filters have been derived from, so
// reconfigure() can skip work when nothing relevant changed.
type configKey struct {
	sampleAttackMs  float32
	sampleReleaseMs float32
	bandAttackMs    float32
	bandReleaseMs   float32
	agcMode         string
	agcRelease      float32
	agcTarget       float32
	bandsScale      string
	bandsLowHz      float32
	limiterEnabled  bool
	limiterWindow   float32
	beatMult        float32
	beatInterval    int
	beatMedian      bool
	activityEnabled bool
	activityThresh  float32
	activityTimeout float32
}

// New creates a Processor for audio at the given sample rate and frame length
// (in samples — used to derive time-constant coefficients). Pass the same
// hop size you feed Process() with.
func New(sampleRate, frameSamples int, store *config.Store) *Processor {
	p := &Processor{
		sampleRate:   sampleRate,
		frameSeconds: float32(frameSamples) / float32(sampleRate),
		cfg:          store,
		fft:          fourier.NewFFT(FFTSize),
		hannWin:      hannWindow(FFTSize),
		ring:         make([]float32, FFTSize),
		scratch:      make([]float32, frameSamples),
		input:        make([]float64, FFTSize),
		coeffs:       make([]complex128, FFTSize/2+1),
		power:        make([]float64, FFTSize/2+1),
		postAGC:      make([]float64, NumBands),
		postLimiter:  make([]float64, NumBands),
		bandSmIn:     make([]float32, NumBands),
		bandSmOut:    make([]float32, NumBands),
		bandSmoother: newAsymSmootherN(NumBands),
	}
	p.reconfigure(store.Load(), true)
	return p
}

// reconfigure rebuilds derived state from the current config. force=true
// runs even when nothing has changed (used at construction).
func (p *Processor) reconfigure(c *config.Config, force bool) {
	key := configKey{
		sampleAttackMs:  c.Smoothing.SampleAttackMs,
		sampleReleaseMs: c.Smoothing.SampleReleaseMs,
		bandAttackMs:    c.Smoothing.BandAttackMs,
		bandReleaseMs:   c.Smoothing.BandReleaseMs,
		agcMode:         c.AGC.Mode,
		agcRelease:      c.AGC.ReleaseSeconds,
		agcTarget:       c.AGC.TargetPeak,
		bandsScale:      c.Bands.Scale,
		bandsLowHz:      c.Bands.LowHz,
		limiterEnabled:  c.Limiter.Enabled,
		limiterWindow:   c.Limiter.WindowSeconds,
		beatMult:        c.Beat.ThresholdMult,
		beatInterval:    c.Beat.MinIntervalMs,
		beatMedian:      c.Beat.MedianGate,
		activityEnabled: c.Activity.Enabled,
		activityThresh:  c.Activity.Threshold,
		activityTimeout: c.Activity.TimeoutSeconds,
	}
	if !force && key == p.applied {
		return
	}

	if force || key.bandsScale != p.applied.bandsScale || key.bandsLowHz != p.applied.bandsLowHz {
		ranges := bandRangesFor(key.bandsScale, float64(key.bandsLowHz), float64(p.sampleRate))
		p.bandBins = computeBandBins(ranges, p.sampleRate, FFTSize)
	}
	p.agc.configure(key.agcMode, key.agcRelease, key.agcTarget, p.frameSeconds)
	if key.limiterEnabled {
		if p.limiter == nil {
			p.limiter = newDynLimiter(NumBands, key.limiterWindow, p.frameSeconds)
		} else {
			p.limiter.resize(NumBands, key.limiterWindow, p.frameSeconds)
		}
	}
	p.sampleSm.configure(key.sampleAttackMs, key.sampleReleaseMs, p.frameSeconds)
	p.bandSmoother.configure(key.bandAttackMs, key.bandReleaseMs, p.frameSeconds)
	p.beat.configure(key.beatMult, time.Duration(key.beatInterval)*time.Millisecond, key.beatMedian)
	p.activity.Configure(key.activityEnabled, key.activityThresh, key.activityTimeout, p.frameSeconds)

	p.applied = key
}

// Process analyses one Frame and returns the computed metrics.
func (p *Processor) Process(frame audio.Frame) Analysis {
	cfg := p.cfg.Load()
	p.reconfigure(cfg, false)

	// Resize scratch if the source ever changes hop size between frames.
	if cap(p.scratch) < len(frame.Samples) {
		p.scratch = make([]float32, len(frame.Samples))
	}
	work := p.scratch[:len(frame.Samples)]
	copy(work, frame.Samples)

	// Stage 1: gain.
	applyGain(work, cfg.Gain.Linear)

	// Stage 2: squelch (decision only — we still feed samples to the FFT so
	// the ring buffer doesn't get a discontinuity, but we zero the output).
	silent := cfg.Squelch.Enabled && belowSquelch(work, cfg.Squelch.Threshold)

	// Append into the ring.
	for _, s := range work {
		p.ring[p.ringPos] = s
		p.ringPos = (p.ringPos + 1) % FFTSize
	}

	// Hann-windowed read-out.
	for i := range p.input {
		idx := (p.ringPos + i) % FFTSize
		p.input[i] = float64(p.ring[idx]) * p.hannWin[i]
	}

	// FFT → power spectrum.
	p.fft.Coefficients(p.coeffs, p.input)
	for i, c := range p.coeffs {
		p.power[i] = real(c)*real(c) + imag(c)*imag(c)
	}

	// 16-band GEQ from configured bin ranges.
	for b, r := range p.bandBins {
		var sum float64
		for i := r[0]; i < r[1]; i++ {
			sum += p.power[i]
		}
		p.bands[b] = math.Sqrt(sum)
	}

	if silent {
		for i := range p.bands {
			p.bands[i] = 0
		}
	}

	// AGC (global) → optional per-band dynamics limiter.
	agcPassthrough := !cfg.AGC.Enabled
	p.agc.updateBands(p.bands[:], p.postAGC, agcPassthrough)
	scaled := p.postAGC
	if cfg.Limiter.Enabled {
		p.limiter.update(p.postAGC, float64(cfg.AGC.TargetPeak), p.postLimiter)
		scaled = p.postLimiter
	}

	// Per-band asymmetric smoother (rise/fall).
	for i, v := range scaled {
		p.bandSmIn[i] = float32(v)
	}
	p.bandSmoother.update(p.bandSmIn, p.bandSmOut)

	// Volume: avg(band) / max(band) * 255 — same metric the Windows server uses.
	var sumB, maxB float64
	for _, v := range p.bands {
		sumB += v
		if v > maxB {
			maxB = v
		}
	}
	var raw float32
	if maxB > 0 && !silent {
		raw = float32(sumB / NumBands / maxB * 255)
	}
	smoothed := p.sampleSm.update(raw)

	// Strongest FFT bin → freq + magnitude.
	var peakPow float64
	var peakBin int
	for i, pw := range p.power {
		if pw > peakPow {
			peakPow = pw
			peakBin = i
		}
	}
	peakMag := math.Sqrt(peakPow)
	peakFreq := float32(peakBin) * float32(p.sampleRate) / float32(FFTSize)
	if silent {
		peakMag = 0
		peakFreq = 0
	}

	peaked := false
	if !silent {
		peaked = p.beat.detect(work)
	}

	var fftResult [NumBands]uint8
	for i, v := range p.bandSmOut {
		if v < 0 {
			v = 0
		} else if v > 255 {
			v = 255
		}
		fftResult[i] = uint8(math.Round(float64(v)))
	}

	active := p.activity.Update(raw)

	return Analysis{
		SampleRaw:  raw,
		SampleSmth: smoothed,
		SamplePeak: peaked,
		FFTResult:  fftResult,
		Magnitude:  p.agc.normMag(peakMag, agcPassthrough),
		MajorPeak:  peakFreq,
		Active:     active,
	}
}

// FrameSeconds reports the frame interval the processor was built for.
func (p *Processor) FrameSeconds() float32 {
	return p.frameSeconds
}

func hannWindow(n int) []float64 {
	w := make([]float64, n)
	for i := range w {
		w[i] = 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(n-1)))
	}
	return w
}

func freqToBin(hz float64, sampleRate, fftSize int) int {
	bin := int(math.Round(hz * float64(fftSize) / float64(sampleRate)))
	if bin < 0 {
		return 0
	}
	return bin
}
