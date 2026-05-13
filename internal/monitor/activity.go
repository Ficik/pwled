// Package monitor watches the audio pipeline for higher-level state changes
// the per-frame DSP doesn't surface on its own — currently just "is there any
// real audio activity?", used to gate the UDP sender so other senders on the
// LAN can take over during silence.
package monitor

// Activity tracks whether audio has crossed the configured loudness threshold
// recently enough to count as "active". It is reconfigurable in place so live
// config edits take effect on the next Update.
//
// It is not safe for concurrent use — the DSP processor is the sole caller.
type Activity struct {
	enabled        bool
	threshold      float32
	timeoutSeconds float32
	frameSeconds   float32

	silentSeconds float32
	active        bool
}

// Configure (re-)applies the activity parameters. frameSeconds is the expected
// gap between Update calls. If monitoring is disabled, Active always reports
// true so the sender keeps streaming unconditionally.
func (a *Activity) Configure(enabled bool, threshold, timeoutSeconds, frameSeconds float32) {
	a.enabled = enabled
	a.threshold = threshold
	a.timeoutSeconds = timeoutSeconds
	a.frameSeconds = frameSeconds
	if !enabled {
		a.active = true
		a.silentSeconds = 0
	}
}

// Update integrates one frame of pre-AGC envelope and returns whether the
// pipeline is currently active. Crossing the threshold resets the silence
// timer immediately; falling below it for at least TimeoutSeconds flips the
// state to inactive.
func (a *Activity) Update(rawEnvelope float32) bool {
	if !a.enabled {
		return true
	}
	if rawEnvelope >= a.threshold {
		a.silentSeconds = 0
		a.active = true
		return true
	}
	a.silentSeconds += a.frameSeconds
	if a.silentSeconds >= a.timeoutSeconds {
		a.active = false
	}
	return a.active
}

// Active returns the cached state without integrating a new sample.
func (a *Activity) Active() bool {
	if !a.enabled {
		return true
	}
	return a.active
}
