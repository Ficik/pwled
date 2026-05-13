// Package config holds the runtime-tunable settings for pwled's DSP pipeline.
//
// Everything here corresponds to a WLED-side knob that the audioreactive
// usermod bypasses when running in network-receive mode (see
// docs/audio-sync-protocol.md, "What the receiver does NOT do"). The sender
// owns these.
//
// The whole struct is meant to be replaced atomically: callers hold an
// *atomic.Pointer[Config] and Load() once per audio frame.
package config

import (
	"fmt"
	"sort"
	"strings"
)

// Config is the live DSP-tunable settings (embedded DSPSettings) plus a list
// of named user-saved presets that can swap the DSP settings in one shot.
//
// JSON serialisation flattens the embedded DSPSettings, so the wire format
// looks like {squelch:{...}, gain:{...}, ..., presets:[...]}.
type Config struct {
	DSPSettings
	Presets []Preset `toml:"presets" json:"presets"`
}

// DSPSettings is the full set of DSP knobs. A snapshot of this struct is what
// each Preset stores.
type DSPSettings struct {
	Squelch   SquelchConfig   `toml:"squelch"   json:"squelch"`
	Gain      GainConfig      `toml:"gain"      json:"gain"`
	AGC       AGCConfig       `toml:"agc"       json:"agc"`
	Bands     BandsConfig     `toml:"bands"     json:"bands"`
	Limiter   LimiterConfig   `toml:"limiter"   json:"limiter"`
	Smoothing SmoothingConfig `toml:"smoothing" json:"smoothing"`
	Beat      BeatConfig      `toml:"beat"      json:"beat"`
	Watchdog  WatchdogConfig  `toml:"watchdog"  json:"watchdog"`
	Activity  ActivityConfig  `toml:"activity"  json:"activity"`
}

// Preset is a named snapshot of DSP settings. Applying a preset replaces the
// embedded DSPSettings of the live Config with Preset.Settings.
type Preset struct {
	Name        string      `toml:"name"        json:"name"`
	Description string      `toml:"description" json:"description"`
	Settings    DSPSettings `toml:"settings"    json:"settings"`
}

// SquelchConfig is a noise gate applied to the time-domain signal before FFT.
// Threshold is in units of full-scale (i.e. 0.005 = -46 dBFS).
type SquelchConfig struct {
	Enabled   bool    `toml:"enabled"   json:"enabled"`
	Threshold float32 `toml:"threshold" json:"threshold"`
}

// GainConfig is a linear multiplier applied before AGC. Use this to bake the
// equivalent of WLED's "Gain" / "Input Level" sliders, since AGC alone can
// take a while to track quiet sources.
type GainConfig struct {
	Linear float32 `toml:"linear" json:"linear"`
}

// AGCMode names the three WLED AGC presets, plus an off state.
const (
	AGCOff    = "off"
	AGCNormal = "normal"
	AGCVivid  = "vivid"
	AGCLazy   = "lazy"
)

// AGCConfig controls the automatic gain controller. ReleaseSeconds sets how
// quickly the peak estimate decays toward zero when the signal gets quieter.
// Mode is the WLED preset name; it picks a default release if set.
type AGCConfig struct {
	Enabled        bool    `toml:"enabled"         json:"enabled"`
	Mode           string  `toml:"mode"            json:"mode"`
	TargetPeak     float32 `toml:"target_peak"     json:"target_peak"`     // band value the loudest band should reach (0..255)
	ReleaseSeconds float32 `toml:"release_seconds" json:"release_seconds"` // explicit time constant, overrides mode default if > 0
}

// BandsScale picks the FFT bin → 16-band mapping. "log" matches the
// WLED-MM defaults (table in the protocol doc); "sqrt" and "linear"
// rebuild the edges from [LowHz .. sampleRate/2] at runtime.
const (
	ScaleLog    = "log"
	ScaleSqrt   = "sqrt"
	ScaleLinear = "linear"
)

// BandsConfig configures the band layout.
type BandsConfig struct {
	Scale string  `toml:"scale"  json:"scale"`
	LowHz float32 `toml:"low_hz" json:"low_hz"` // first band's low edge; ignored for "log" (uses MM table)
}

// LimiterConfig is the dynamics limiter — a per-band rolling-max normaliser.
// Unlike AGC (single global peak across all bands), this evens out loudness
// between bands so quiet bass still shows up when treble dominates.
type LimiterConfig struct {
	Enabled       bool    `toml:"enabled"        json:"enabled"`
	WindowSeconds float32 `toml:"window_seconds" json:"window_seconds"`
}

// SmoothingConfig holds the asymmetric one-pole time constants for the
// sample envelope and each FFT band. Attack governs rise (signal getting
// louder), release governs fall — the "Rise" and "Fall" sliders in WLED.
type SmoothingConfig struct {
	SampleAttackMs  float32 `toml:"sample_attack_ms"  json:"sample_attack_ms"`
	SampleReleaseMs float32 `toml:"sample_release_ms" json:"sample_release_ms"`
	BandAttackMs    float32 `toml:"band_attack_ms"    json:"band_attack_ms"`
	BandReleaseMs   float32 `toml:"band_release_ms"   json:"band_release_ms"`
}

// BeatConfig governs the simple onset detector.
type BeatConfig struct {
	ThresholdMult float32 `toml:"threshold_mult"  json:"threshold_mult"`  // energy must exceed this × mean(history) to trigger
	MinIntervalMs int     `toml:"min_interval_ms" json:"min_interval_ms"` // refractory period after a detection
	MedianGate    bool    `toml:"median_gate"     json:"median_gate"`     // additionally require energy > 1.4 × median(history)
}

// WatchdogConfig governs the silence-decay packet emitted every 450 ms when
// audio stalls.
type WatchdogConfig struct {
	DecayPerFrame float32 `toml:"decay_per_frame" json:"decay_per_frame"`
}

// ActivityConfig governs the longer-window "is anyone playing audio?"
// decision. While Active is false the UDP sender pauses entirely (no frame
// packets and no watchdog decay), so another sender on the LAN can take over
// after WLED's ~500 ms receive timeout.
//
// Threshold is compared against the pre-AGC raw envelope (Analysis.SampleRaw,
// roughly 0..255), so it's stable against the AGC pumping up the noise floor.
type ActivityConfig struct {
	Enabled        bool    `toml:"enabled"         json:"enabled"`
	Threshold      float32 `toml:"threshold"       json:"threshold"`        // pre-AGC raw envelope (0..255)
	TimeoutSeconds float32 `toml:"timeout_seconds" json:"timeout_seconds"`  // silence-to-inactive delay
}

// DefaultDSP returns the "Practical sender defaults" from
// docs/audio-sync-protocol.md as a DSPSettings snapshot.
func DefaultDSP() DSPSettings {
	return DSPSettings{
		Squelch: SquelchConfig{
			Enabled:   true,
			Threshold: 0.005,
		},
		Gain: GainConfig{Linear: 1.0},
		AGC: AGCConfig{
			Enabled:        true,
			Mode:           AGCNormal,
			TargetPeak:     220,
			ReleaseSeconds: 0,
		},
		Bands: BandsConfig{
			Scale: ScaleLog,
			LowHz: 60,
		},
		Limiter: LimiterConfig{
			Enabled:       false,
			WindowSeconds: 4.0,
		},
		Smoothing: SmoothingConfig{
			SampleAttackMs:  50,
			SampleReleaseMs: 300,
			BandAttackMs:    80,
			BandReleaseMs:   1400,
		},
		Beat: BeatConfig{
			ThresholdMult: 1.5,
			MinIntervalMs: 250,
			MedianGate:    false,
		},
		Watchdog: WatchdogConfig{
			DecayPerFrame: 0.85,
		},
		Activity: ActivityConfig{
			Enabled:        true,
			Threshold:      4,
			TimeoutSeconds: 5,
		},
	}
}

// Default returns a Config seeded with DefaultDSP and the five built-in
// presets that span the smoothing/AGC spectrum.
func Default() Config {
	return Config{
		DSPSettings: DefaultDSP(),
		Presets:     defaultPresets(),
	}
}

// defaultPresets is what gets written into a freshly-created config.toml. The
// tunings match the docs/audio-effects.md guidance — flashy → lazy.
func defaultPresets() []Preset {
	mk := func(mut func(*DSPSettings)) DSPSettings {
		s := DefaultDSP()
		mut(&s)
		return s
	}
	return []Preset{
		{
			Name:        "Flashy",
			Description: "Snappy, loud, twitchy. Good for parties.",
			Settings: mk(func(s *DSPSettings) {
				s.AGC.Mode = AGCVivid
				s.AGC.TargetPeak = 240
				s.Limiter.Enabled = true
				s.Limiter.WindowSeconds = 2.0
				s.Smoothing = SmoothingConfig{30, 120, 30, 250}
				s.Beat.ThresholdMult = 1.3
				s.Beat.MinIntervalMs = 150
				s.Watchdog.DecayPerFrame = 0.50
			}),
		},
		{
			Name:        "Punchy",
			Description: "Fast attack, slow release. Hits land then decay.",
			Settings: mk(func(s *DSPSettings) {
				s.Limiter.Enabled = true
				s.Limiter.WindowSeconds = 4.0
				s.Smoothing = SmoothingConfig{25, 500, 25, 700}
				s.Watchdog.DecayPerFrame = 0.70
			}),
		},
		{
			Name:        "Vivid",
			Description: "Bright and colourful, all bands active, moderate smoothing.",
			Settings: mk(func(s *DSPSettings) {
				s.AGC.Mode = AGCVivid
				s.AGC.TargetPeak = 240
				s.Limiter.Enabled = true
				s.Limiter.WindowSeconds = 3.0
				s.Smoothing = SmoothingConfig{40, 300, 60, 1000}
			}),
		},
		{
			Name:        "Smooth",
			Description: "Ambient. Long fall times, gentle beats, slow watchdog decay.",
			Settings: mk(func(s *DSPSettings) {
				s.Squelch.Threshold = 0.010
				s.AGC.Mode = AGCLazy
				s.AGC.TargetPeak = 200
				s.Limiter.Enabled = true
				s.Limiter.WindowSeconds = 8.0
				s.Smoothing = SmoothingConfig{100, 800, 200, 2500}
				s.Beat.ThresholdMult = 2.0
				s.Beat.MinIntervalMs = 500
				s.Beat.MedianGate = true
				s.Watchdog.DecayPerFrame = 0.95
			}),
		},
		{
			Name:        "Lazy",
			Description: "Barely reactive — slow glow following overall loudness.",
			Settings: mk(func(s *DSPSettings) {
				s.Squelch.Threshold = 0.020
				s.AGC.Mode = AGCLazy
				s.AGC.TargetPeak = 180
				s.Limiter.Enabled = false
				s.Smoothing = SmoothingConfig{200, 2000, 500, 4000}
				s.Beat.ThresholdMult = 2.5
				s.Beat.MinIntervalMs = 800
				s.Beat.MedianGate = true
				s.Watchdog.DecayPerFrame = 0.97
			}),
		},
	}
}

// ValidationError carries one or more per-field problems found in a Config.
type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string {
	if len(e.Fields) == 0 {
		return "config: validation error"
	}
	keys := make([]string, 0, len(e.Fields))
	for k := range e.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	msg := "config validation failed:"
	for _, k := range keys {
		msg += fmt.Sprintf(" %s: %s;", k, e.Fields[k])
	}
	return msg
}

// Validate checks every numeric and enum field against its allowed range,
// plus per-preset settings and preset-name uniqueness/non-empty constraints.
// All problems are reported together so a UI can surface them at once.
func (c *Config) Validate() error {
	v := &ValidationError{Fields: map[string]string{}}
	validateDSP(&c.DSPSettings, "", v.Fields)

	seen := map[string]int{}
	for i, p := range c.Presets {
		prefix := fmt.Sprintf("presets[%d].", i)
		if strings.TrimSpace(p.Name) == "" {
			v.Fields[prefix+"name"] = "must not be empty"
		} else if first, ok := seen[p.Name]; ok {
			v.Fields[prefix+"name"] = fmt.Sprintf("duplicate of presets[%d].name", first)
		} else {
			seen[p.Name] = i
		}
		validateDSP(&c.Presets[i].Settings, prefix, v.Fields)
	}

	if len(v.Fields) == 0 {
		return nil
	}
	return v
}

// validateDSP checks the bounds of a DSPSettings snapshot, writing failures
// into out with the given key prefix (e.g. "" for top-level or "presets[0]."
// for a preset's settings).
func validateDSP(d *DSPSettings, prefix string, out map[string]string) {
	put := func(k, msg string) { out[prefix+k] = msg }

	if d.Squelch.Threshold < 0 || d.Squelch.Threshold > 0.5 {
		put("squelch.threshold", "must be in [0, 0.5]")
	}
	if d.Gain.Linear < 0 || d.Gain.Linear > 100 {
		put("gain.linear", "must be in [0, 100]")
	}
	switch d.AGC.Mode {
	case AGCOff, AGCNormal, AGCVivid, AGCLazy:
	default:
		put("agc.mode", "must be one of: off, normal, vivid, lazy")
	}
	if d.AGC.TargetPeak < 32 || d.AGC.TargetPeak > 255 {
		put("agc.target_peak", "must be in [32, 255]")
	}
	if d.AGC.ReleaseSeconds < 0 || d.AGC.ReleaseSeconds > 60 {
		put("agc.release_seconds", "must be in [0, 60]")
	}
	switch d.Bands.Scale {
	case ScaleLog, ScaleSqrt, ScaleLinear:
	default:
		put("bands.scale", "must be one of: log, sqrt, linear")
	}
	if d.Bands.LowHz < 20 || d.Bands.LowHz > 500 {
		put("bands.low_hz", "must be in [20, 500]")
	}
	if d.Limiter.WindowSeconds < 0.1 || d.Limiter.WindowSeconds > 60 {
		put("limiter.window_seconds", "must be in [0.1, 60]")
	}
	if d.Smoothing.SampleAttackMs < 1 || d.Smoothing.SampleAttackMs > 5000 {
		put("smoothing.sample_attack_ms", "must be in [1, 5000]")
	}
	if d.Smoothing.SampleReleaseMs < 1 || d.Smoothing.SampleReleaseMs > 10000 {
		put("smoothing.sample_release_ms", "must be in [1, 10000]")
	}
	if d.Smoothing.BandAttackMs < 1 || d.Smoothing.BandAttackMs > 5000 {
		put("smoothing.band_attack_ms", "must be in [1, 5000]")
	}
	if d.Smoothing.BandReleaseMs < 1 || d.Smoothing.BandReleaseMs > 10000 {
		put("smoothing.band_release_ms", "must be in [1, 10000]")
	}
	if d.Beat.ThresholdMult < 1.0 || d.Beat.ThresholdMult > 10 {
		put("beat.threshold_mult", "must be in [1.0, 10]")
	}
	if d.Beat.MinIntervalMs < 50 || d.Beat.MinIntervalMs > 5000 {
		put("beat.min_interval_ms", "must be in [50, 5000]")
	}
	if d.Watchdog.DecayPerFrame < 0.5 || d.Watchdog.DecayPerFrame > 1.0 {
		put("watchdog.decay_per_frame", "must be in [0.5, 1.0]")
	}
	if d.Activity.Threshold < 0 || d.Activity.Threshold > 255 {
		put("activity.threshold", "must be in [0, 255]")
	}
	if d.Activity.TimeoutSeconds < 0.1 || d.Activity.TimeoutSeconds > 600 {
		put("activity.timeout_seconds", "must be in [0.1, 600]")
	}
}
