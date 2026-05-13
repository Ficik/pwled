package audio

// Frame holds a chunk of mono PCM samples from a single capture window.
type Frame struct {
	Samples    []float32
	SampleRate int
}

// Source is the interface for audio capture backends.
// Implementations must be safe to call Close from any goroutine.
type Source interface {
	// Frames returns a channel of captured audio frames.
	// The channel is closed when the source is exhausted or after Close.
	Frames() <-chan Frame
	// Err returns any non-EOF error that caused the source to stop.
	Err() error
	// Close shuts down the source and releases its resources.
	Close() error
}
