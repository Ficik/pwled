# WLED Audio Sync Protocol (v2)

Specification of the **audio-reactive UDP sync** protocol pwled emits and the
WLED AudioReactive usermod consumes. It covers only this protocol — not the
WLED realtime LED protocols (DDP/E1.31/WARLS/etc.) or WLED's JSON API.

Verified against the upstream sources:

- Sender (Windows reference): [SR-WLED-audio-server-win — `AudioSyncPacket.cs`
  + `NetworkManager.cs`](https://github.com/MoonModules/SR-WLED-audio-server-win).
- Receiver: [WLED — `usermods/audioreactive/audio_reactive.cpp`](https://github.com/wled/WLED/blob/main/usermods/audioreactive/audio_reactive.cpp)
  (see `audioSyncPacket`, `transmitAudioData`, `decodeAudioData`).
- Sender (this project): [`internal/wled/packet.go`](../internal/wled/packet.go)
  and [`internal/wled/sender.go`](../internal/wled/sender.go).

## Transport

- **Protocol:** UDP, IPv4.
- **Port:** `11988` (configurable on the WLED side; must match on the sender).
- **Destination:** any of:
  - `255.255.255.255` (LAN broadcast) — default in the Windows server.
  - Subnet-directed broadcast (e.g. `192.168.1.255`).
  - Multicast group `239.0.0.1`. WLED's `transmitAudioData()` uses
    `beginMulticastPacket()`, so multicast is the "native" mode if your network
    supports IGMP snooping.
  - Unicast to each WLED node.
- **Cadence:**
  - Send one packet for every processed audio frame. WLED reads at ~10 ms
    intervals; the Windows sender uses a 10 ms WASAPI buffer and emits per
    frame.
  - Also send a heartbeat packet **at least every 500 ms** when audio is silent
    or stalled, so receivers don't time out. The Windows server does this with
    a 500 ms watchdog timer; the packet payload may be a decayed copy of the
    last frame.
- **One-way:** the protocol is fire-and-forget. There is no ACK, no handshake,
  no discovery — WLED simply listens on the port.

## Packet format — v2 (44 bytes, packed)

All multi-byte fields are **little-endian** (x86/ESP32 native). The struct is
declared `__attribute__((packed))` on the WLED side; do not insert padding.

| Offset | Size | Type        | Field              | Notes                                                              |
| -----: | ---: | ----------- | ------------------ | ------------------------------------------------------------------ |
|      0 |    6 | `char[6]`   | `header`           | ASCII `"00002"` + `\0` terminator. Identifies protocol version 2.  |
|      6 |    2 | `uint8[2]`  | `pressure`         | Sound pressure as Q8.8 fixed point (integer, fraction). MM-only.   |
|      8 |    4 | `float32`   | `sampleRaw`        | Instantaneous volume sample, range ~0..255.                        |
|     12 |    4 | `float32`   | `sampleSmth`       | Smoothed/AGC volume sample, range ~0..255.                         |
|     16 |    1 | `uint8`     | `samplePeak`       | `0` = no peak this frame, `>=1` = peak/beat detected.              |
|     17 |    1 | `uint8`     | `frameCounter`     | Rolling counter; receivers may use it to detect dupes/out-of-order.|
|     18 |   16 | `uint8[16]` | `fftResult`        | 16-band GEQ, one byte per band (0..255). Band 0 = lowest.          |
|     34 |    2 | `uint16`    | `zeroCrossingCount`| Zero crossings over the last analysis window. MM-only.            |
|     36 |    4 | `float32`   | `FFT_Magnitude`    | Magnitude of strongest FFT bin (raw, can exceed 4096).             |
|     40 |    4 | `float32`   | `FFT_MajorPeak`    | Frequency in Hz of strongest FFT bin (clamped 1..11025 on RX).     |

**Total: 44 bytes.** WLED rejects packets where `packetSize != sizeof(audioSyncPacket)`
even if the header matches.

### Fields the mainline WLED ignores

`pressure`, `frameCounter`, and `zeroCrossingCount` sit in regions the mainline
WLED firmware treats as reserved padding (`reserved1[2]`, `reserved2`,
`reserved3`). They are read by the **WLED-MM** (MoonModules) fork and by some
effects. Always write them — set unknown fields to `0`. Never leave random
stack bytes there: WLED's TX path explicitly `memset`s the whole struct first
to keep the wire format deterministic.

### Header byte-for-byte

```
0x30 0x30 0x30 0x30 0x32 0x00     "00002\0"
```

A legacy v1 format (`"00001"`, 83-byte payload) also exists and is still
decoded by WLED. New senders should only emit v2.

## Field semantics — what to put in each value

These are the conventions the Windows server uses; matching them gives the
closest visual parity with a WLED-internal mic.

### `sampleRaw` / `sampleSmth` (volume)

Range: roughly `0..255`. WLED applies `fmaxf(..., 0)` on receive, so don't send
negatives. The Windows server computes:

```
raw = avg(|bucket|) / max(|bucket|) * 255
sampleRaw = sampleSmth = raw
```

That is, a relative loudness measure across the 16 bands, not absolute SPL.
A simpler valid approximation is `clamp(rms * gain, 0, 255)` where `gain` is
set by your AGC. WLED effects that drive brightness from volume expect this
~0..255 envelope.

### `samplePeak` (beat / transient)

`1` for the single packet on which a beat is detected, `0` otherwise. WLED
latches it for a short time internally (`autoResetPeak`). The Windows server
uses a band-energy + threshold beat detector (`BeatDetector.cs`) with a
configurable min/max BPM window (defaults 100..500 ms between peaks). A simple
spectral-flux or energy-onset detector works fine; over-triggering is more
visually disruptive than missing beats.

### `fftResult[16]` (GEQ)

16-band graphic-EQ-style spectrum. Each band 0..255. Bands are roughly
log-spaced; the Windows server defaults to the WLED-MM band layout:

| Band | Approx low Hz | Approx high Hz |
| ---: | ------------: | -------------: |
|    0 |            86 |            129 |
|    1 |           129 |            216 |
|    2 |           216 |            301 |
|    3 |           301 |            430 |
|    4 |           430 |            560 |
|    5 |           560 |            818 |
|    6 |           818 |           1120 |
|    7 |          1120 |           1421 |
|    8 |          1421 |           1895 |
|    9 |          1895 |           2412 |
|   10 |          2412 |           3015 |
|   11 |          3015 |           3704 |
|   12 |          3704 |           4479 |
|   13 |          4479 |           7180 |
|   14 |          7180 |          10100 |
|   15 |         10100 |          17200 |

(These are the values the WLED-MM `audio_reactive` mod targets; pick whatever
mapping you like but log-space them and keep 16 bands.)

The Windows server sums FFT bin magnitudes into each band, applies AGC
(`BucketGainControl`) so the loudest bucket maps near 255, and writes the
result. WLED is hardcoded to 16 bands (`NUM_GEQ_CHANNELS`).

### `FFT_Magnitude` and `FFT_MajorPeak`

The strongest FFT bin from this analysis frame:

- `FFT_MajorPeak` — its center frequency in Hz. WLED clamps to `1..11025`.
- `FFT_Magnitude` — its magnitude. WLED stores this raw; some effects divide
  by 2, 4, 8, or 16 and cast to `uint8_t`, so values can run up to ~4096
  (`255 * 16`) without overflow. The Windows server scales to `bucketPeak /
  agcSpan * 4080`.

These drive frequency-following effects (Freqmap, Rocktaves, Waterfall).

### `pressure` (Q8.8, MM-only)

Integer-byte at offset 6, fractional-byte at offset 7. The conversion the
Windows server uses is:

```
clamped = clamp(value, 0, 255) * 256
integer  = clamped / 256
fraction = clamped % 256
```

Decoded value: `integer + fraction/255` (note: not `/256` — matches the
sender). MM uses this as a coarse SPL proxy; if unused, send zeros.

### `frameCounter`

Increment per sent packet; wraps at 256. The Windows server bumps this in the
UDP-send path, not in audio processing — i.e. it counts wire packets, not
analysis frames. Used by some receivers to drop duplicates.

### `zeroCrossingCount` (MM-only)

Count of sign changes in the time-domain signal over the analysis window
(~23 ms in WLED-MM). The Windows server stores
`zeroCrossings / sampleLength * 255` (so it's normalized, not a raw count,
despite the field being a `uint16`). If unused, zero.

## Silence handling

When there's no audio, do **not** simply stop sending — receivers will keep
displaying the last frame. The Windows server's silence handler:

1. Multiplies all numeric fields by `0.85` per silent frame (exponential decay).
2. Force-clears `FFT_MajorPeak`, `samplePeak`, `zeroCrossingCount`,
   `frameCounter`.
3. Keeps emitting at the 500 ms watchdog cadence.

This leaves effects with a clean zero state without a hard cut.

## What the receiver does NOT do (RX-mode pipeline)

When the WLED AudioReactive usermod is set to **Receive**, the entire local
audio pipeline is bypassed. The FFT task short-circuits in `audio_reactive.cpp`:

```cpp
if (disableSoundProcessing || (audioSyncEnabled & 0x02)) {
  vTaskDelayUntil(...);
  continue;       // skip mic read, FFT, post-process
}
```

The only RX path is `decodeAudioData()`, which copies the wire bytes straight
into the variables that effects read, applying just two transforms:

- `fmaxf(..., 0.0f)` floor on `sampleRaw`, `sampleSmth`, `FFT_Magnitude` (no
  negatives reach the effects).
- `constrain(FFT_MajorPeak, 1.0f, 11025.0f)` clamp.

Everything else the local-mic path normally does is **inert in receive mode**.
That means none of the following WLED settings have any effect on what
effects see — the sender owns each one:

| WLED setting (RX-inert)         | Where it would normally apply (mic mode)                                                       | pwled equivalent (where it lives)                                                                 |
| ------------------------------- | ---------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------- |
| **Squelch**                     | `getSample()` noise gate via `expAdjF`                                                          | Pre-FFT silence gate — [`internal/dsp/squelch.go`](../internal/dsp/squelch.go).                   |
| **Gain** (`sampleGain`, `inputLevel`) | `fftCalc[i] *= sampleGain/40 * inputLevel/128` and amplification in `getSample()`         | Linear scale on the time-domain signal — [`internal/dsp/gain.go`](../internal/dsp/gain.go).       |
| **AGC** (`soundAgc`, `agcSampleSmooth`) | `agcAvg()` / `multAgc` envelope tracker                                                | Global envelope tracker over band peaks — [`internal/dsp/agc.go`](../internal/dsp/agc.go).        |
| **Frequency scale** (lin / sqrt / log) | `postProcessFFTResults()` band assignment                                               | FFT-bin → 16-band mapping — [`internal/dsp/bands.go`](../internal/dsp/bands.go).                  |
| **Dynamics limiter**            | `postProcessFFTResults()` per-band normalisation                                                | Per-band rolling max → normalise — [`internal/dsp/limiter.go`](../internal/dsp/limiter.go).       |
| **Rise / Fall** (per-band smoothing) | `fftAvg[i] = α·fftCalc[i] + (1-α)·fftAvg[i]` rise-fast/fall-slow, `α` from **Decay Time** | Asymmetric one-pole filters — [`internal/dsp/smoother.go`](../internal/dsp/smoother.go).          |
| **Attack / Decay Time**         | Drives `agcSampleSmooth` and `fftAvg` α selection                                              | Same — exposed as `smoothing.{sample,band}_{attack,release}_ms` in the config.                    |
| (write-back) `samplePeak` latch | `autoResetPeak()` runs on RX too — one-frame latched ~50 ms                                    | Sender owns peak decisions — [`internal/dsp/beat.go`](../internal/dsp/beat.go).                   |

Put another way: in receive mode, the WLED node is a dumb LED renderer that
trusts the sender to deliver display-ready values. Loudness, smoothing, and
dynamics shaping are entirely the sender's job. See
[audio-effects.md](audio-effects.md) for what each effect does with the
delivered values, and why "Rise/Fall" smoothing in the sender is the most
useful single knob for an ambient setup.

### Practical sender defaults

Numbers that produce the closest visual parity with a well-tuned mic path:

- `sampleSmth`: one-pole envelope, attack ~50 ms, release ~250–400 ms.
- `fftResult[i]`: per-band one-pole, attack ~50–100 ms, release ~600–2000 ms
  (roughly equivalent to WLED's "Decay Time" 1400 ms default; raise toward
  2000–3000 ms for ambient).
- `samplePeak`: minimum 200–400 ms between flagged peaks; require the energy
  onset to exceed a recent median, not just an absolute threshold.
- AGC: slow window (5–10 s), targeting "loudest band ≈ 220" so headroom
  remains for transients.

## Reference layout in C

```c
#include <stdint.h>

struct __attribute__((packed)) audio_sync_packet_v2 {
    char     header[6];           // "00002\0"
    uint8_t  pressure[2];         // Q8.8
    float    sampleRaw;
    float    sampleSmth;
    uint8_t  samplePeak;
    uint8_t  frameCounter;
    uint8_t  fftResult[16];
    uint16_t zeroCrossingCount;
    float    FFT_Magnitude;
    float    FFT_MajorPeak;
};
_Static_assert(sizeof(struct audio_sync_packet_v2) == 44, "wire size");
```

## How pwled implements this

| Step                          | pwled location                                                                                 |
| ----------------------------- | ---------------------------------------------------------------------------------------------- |
| PipeWire capture (mono, f32)  | [`internal/audio/pwcat.go`](../internal/audio/pwcat.go) — spawns `pw-cat --record` and links output ports to a private node. |
| 2048-point FFT, 1024 hop, Hann | [`internal/dsp/processor.go`](../internal/dsp/processor.go) (`FFTSize`, `hannWindow`).         |
| 16-band log/sqrt/linear mapping | [`internal/dsp/bands.go`](../internal/dsp/bands.go).                                          |
| AGC + dynamics limiter        | [`internal/dsp/agc.go`](../internal/dsp/agc.go), [`internal/dsp/limiter.go`](../internal/dsp/limiter.go). |
| Asymmetric rise/fall smoothing | [`internal/dsp/smoother.go`](../internal/dsp/smoother.go).                                    |
| Beat detector                  | [`internal/dsp/beat.go`](../internal/dsp/beat.go).                                            |
| 44-byte v2 pack + UDP         | [`internal/wled/packet.go`](../internal/wled/packet.go), [`internal/wled/sender.go`](../internal/wled/sender.go). |
| 450 ms silence watchdog        | [`internal/wled/sender.go`](../internal/wled/sender.go) (`onWatchdog`).                       |
| Activity gate (pause sender)   | [`internal/monitor/activity.go`](../internal/monitor/activity.go).                            |

On the WLED node: enable the AudioReactive usermod, set **Sync Mode → Receive**,
and match the port and group/broadcast settings (see the README).
