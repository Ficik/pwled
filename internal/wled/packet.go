package wled

import (
	"encoding/binary"
	"math"
)

// header is the 6-byte protocol identifier for v2: ASCII "00002\0".
var header = [6]byte{'0', '0', '0', '0', '2', 0}

// Packet is the 44-byte WLED Audio Sync v2 payload.
// Field offsets and semantics are documented in docs/audio-sync-protocol.md.
type Packet struct {
	Pressure          [2]uint8  // Q8.8 sound pressure (WLED-MM only; send 0 if unused)
	SampleRaw         float32   // instantaneous volume 0..255
	SampleSmth        float32   // smoothed/AGC volume 0..255
	SamplePeak        uint8     // 1 = beat/peak this frame, 0 otherwise
	FrameCounter      uint8     // rolling per-packet counter; filled by Sender
	FFTResult         [16]uint8 // 16-band GEQ, each band 0..255
	ZeroCrossingCount uint16    // normalised zero-crossing rate (MM-only; send 0)
	FFTMagnitude      float32   // magnitude of strongest FFT bin (0..~4080)
	FFTMajorPeak      float32   // frequency in Hz of strongest FFT bin
}

// Marshal serialises p into the 44-byte on-wire format (little-endian, packed).
// Offsets match the C struct exactly; see the layout table in the protocol doc.
func (p *Packet) Marshal() [44]byte {
	var b [44]byte
	copy(b[0:6], header[:])
	b[6] = p.Pressure[0]
	b[7] = p.Pressure[1]
	binary.LittleEndian.PutUint32(b[8:12], math.Float32bits(p.SampleRaw))
	binary.LittleEndian.PutUint32(b[12:16], math.Float32bits(p.SampleSmth))
	b[16] = p.SamplePeak
	b[17] = p.FrameCounter
	copy(b[18:34], p.FFTResult[:])
	binary.LittleEndian.PutUint16(b[34:36], p.ZeroCrossingCount)
	binary.LittleEndian.PutUint32(b[36:40], math.Float32bits(p.FFTMagnitude))
	binary.LittleEndian.PutUint32(b[40:44], math.Float32bits(p.FFTMajorPeak))
	return b
}

// Decay returns a copy of p with numeric fields multiplied by decay
// (typically 0.85 per silent frame). Used by the silence watchdog so WLED
// effects fade cleanly instead of freezing on the last active frame.
func (p Packet) Decay(decay float64) Packet {
	p.SampleRaw = float32(float64(p.SampleRaw) * decay)
	p.SampleSmth = float32(float64(p.SampleSmth) * decay)
	p.SamplePeak = 0
	p.FFTMajorPeak = 0
	p.ZeroCrossingCount = 0
	p.FrameCounter = 0
	p.FFTMagnitude = float32(float64(p.FFTMagnitude) * decay)
	for i := range p.FFTResult {
		p.FFTResult[i] = uint8(float64(p.FFTResult[i]) * decay)
	}
	return p
}
