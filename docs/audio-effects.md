# WLED audio-reactive effects: what each one does with the sync packet

Companion to [audio-sync-protocol.md](audio-sync-protocol.md). The protocol
doc describes what's *on the wire*. This one describes what the WLED receiver
does *with* those bytes — which fields each effect reads, what visual property
the audio modulates, and how the user-facing controls interact with that. Use
it to pick effects (and pwled presets) that match the look you want.

Source of truth: [`wled00/FX.cpp`](https://github.com/wled/WLED/blob/main/wled00/FX.cpp);
audio data is exposed by [`usermods/audioreactive/audio_reactive.cpp`](https://github.com/wled/WLED/blob/main/usermods/audioreactive/audio_reactive.cpp)
through the `getUMData` interface.

## The audio surface area

The audioreactive usermod publishes 9 named values that any effect can read.
Indices match `um_data->u_data[N]`:

| idx | Name            | Type        | Source on wire     | Typical use                              |
| --: | --------------- | ----------- | ------------------ | ---------------------------------------- |
|   0 | `volumeSmth`    | float       | `sampleSmth`       | Smoothed loudness 0..255 (env follower). |
|   1 | `volumeRaw`     | int16/float | `sampleRaw`        | Instantaneous loudness 0..255.           |
|   2 | `fftResult[16]` | uint8[16]   | `fftResult`        | 16-band GEQ, log-spaced.                 |
|   3 | `samplePeak`    | uint8       | `samplePeak`       | Beat flag (1 once per beat).             |
|   4 | `FFT_MajorPeak` | float       | `FFT_MajorPeak`    | Frequency in Hz of strongest bin.        |
|   5 | `my_magnitude`  | float       | `FFT_Magnitude`    | Magnitude of strongest bin (raw).        |
|   6 | `maxVol`        | uint8*      | local UI (custom2) | Per-effect peak threshold.               |
|   7 | `binNum`        | uint8*      | local UI (custom1) | Per-effect FFT bin selector.             |
|   8 | `fftBin`        | float*      | internal           | Raw 256-bin FFT (not on the wire).       |

Indices 6 and 7 are *written by* effects (Puddlepeak, Ripplepeak, Waterfall)
so the usermod's beat detector knows which bin and threshold to compare —
they aren't on the wire and pwled can't influence them.

The standard segment controls (`speed`, `intensity`, `custom1..3`, `check1..3`,
palette, `colors[0..2]`) are *non-audio* inputs the user adjusts in WLED's UI.

## Per-effect breakdown

Notation: **reads** = which audio fields. **modulates** = the visual property
the audio drives. **palette/color** = how `colors[0..2]` and the palette get
used. Line numbers refer to `FX.cpp` upstream `main`.

### Volume-driven (1D)

| Effect      | Line | Reads                     | Modulates                                                                | Palette / color                                                                  |
| ----------- | ---: | ------------------------- | ------------------------------------------------------------------------ | -------------------------------------------------------------------------------- |
| Pixels      | 7174 | `volumeSmth`              | # of random sparkles + their brightness                                  | Palette indexed by stored sample. `colors[1]` is bg.                             |
| Juggles     | 6925 | `volumeSmth`              | Brightness of beatsin-driven dots                                        | Palette indexed by `strip.now` (time-rotating). `colors[1]` is bg.               |
| Midnoise    | 6977 | `volumeSmth`              | Half-width of perlin band around center                                  | Palette indexed by perlin noise modulated by volume.                             |
| Noisemeter  | 7033 | `volumeSmth`, `volumeRaw` | Width of bar from start                                                  | Palette indexed by perlin noise.                                                 |
| Noisefire   | 7008 | `volumeSmth`              | Brightness of fixed fire palette                                         | **Hardcoded fire palette** — user palette/colors ignored.                        |
| Plasmoid    | 7095 | `volumeSmth`              | Threshold gating — only pixels brighter than `vol*intensity/64` light up | Palette indexed by computed brightness.                                          |
| Puddles     | 7165 | `volumeRaw`               | Random "splash" size                                                     | Palette indexed by `strip.now`.                                                  |
| Puddlepeak  | 7160 | `volumeSmth`, `samplePeak`| Splash on detected beat; size ∝ volume                                   | Palette indexed by `strip.now`. Also reads `maxVol`/`binNum`.                    |
| Matripix    | 6943 | `volumeRaw`               | Brightness of newly added pixel; whole strip shifts left                 | Palette indexed by `strip.now`. `colors[1]` is bg.                               |
| Pixelwave   | 7062 | `volumeRaw`               | Brightness of center pixel; spreads outward                              | Palette indexed by `strip.now`. `colors[1]` is bg.                               |
| Gravcenter  | 6890 | `volumeSmth`              | Bar height from center; gravity peak indicator                           | Palette indexed by perlin of position+volume.                                    |
| Gravcentric | 6898 | `volumeSmth`              | Same as Gravcenter, solid blocks                                         | Palette indexed by `vol*24 + time`. Peak in gray.                                |
| Gravimeter  | 6907 | `volumeSmth`              | Bar height from start                                                    | Palette indexed by perlin. `colors[1]` blend.                                    |

### Beat- and frequency-driven (1D)

| Effect      | Line | Reads                                              | Modulates                                                | Palette / color                                                                  |
| ----------- | ---: | -------------------------------------------------- | -------------------------------------------------------- | -------------------------------------------------------------------------------- |
| Ripple Peak | 6652 | `samplePeak`, `FFT_MajorPeak`, `maxVol`, `binNum`  | Spawn a ripple on each beat                              | Palette indexed by `log10(MajorPeak)` — color encodes pitch.                     |
| Waterfall   | 7489 | `samplePeak`, `FFT_MajorPeak`, `my_magnitude`      | Adds one pixel at the end; whole strip scrolls left      | Pixel color = palette by pitch, brightness by magnitude. Beat → solid CHSV(92).  |
| Freqmap     | 7260 | `FFT_MajorPeak`, `my_magnitude`                    | Pixel position = pitch (log-mapped to length)            | Palette indexed by pitch + `intensity` offset, brightness by magnitude.          |
| Freqpixels  | 7345 | `FFT_MajorPeak`, `my_magnitude`                    | # of random pixels = `intensity/32`                      | Same color scheme as Freqmap.                                                    |
| Freqmatrix  | 7291 | `FFT_MajorPeak`, `volumeSmth`                      | Brightness of leftmost pixel; strip shifts               | **CHSV color directly from frequency** — palette ignored.                        |
| Freqwave    | 7385 | `FFT_MajorPeak`, `volumeSmth`                      | Brightness of center pixel; spreads outward              | **CHSV color directly from frequency** — palette ignored.                        |
| Rocktaves   | 7455 | `FFT_MajorPeak`, `my_magnitude`                    | Position via beatsin8; brightness ∝ magnitude            | Palette indexed by note-within-octave (same note = same color across octaves).   |
| Noisemove   | 7434 | `fftResult[0..15]`                                 | One pixel per band; position = perlin                    | Palette indexed by `band*64`.                                                    |
| Blurz       | 7200 | `fftResult[0..15]`                                 | One random pixel per tick from rotating band             | Palette indexed by `band` value × scale; whole strip blurred.                    |
| DJ Light    | 7231 | `fftResult[0,5,15]`                                | Center pixel = CRGB(highs, mids, lows); scrolls outward  | **Hardcoded R/G/B mapping** — palette ignored.                                   |

### 2D / matrix

| Effect          | Line | Reads                              | Modulates                                                  | Palette / color                                          |
| --------------- | ---: | ---------------------------------- | ---------------------------------------------------------- | -------------------------------------------------------- |
| 2D Swirl        | 6726 | `volumeSmth`, `volumeRaw`          | Saturation modulation; palette index includes `vol*4`      | Palette indexed by time + `vol*4`.                       |
| 2D Waverly      | 6763 | `volumeSmth`                       | Wave height                                                | Palette indexed by row position (0..250).                |
| 2D GEQ          | 7543 | `fftResult[0..15]`                 | Bar height per band (one bar per column)                   | Palette indexed by band; check1 toggles vertical color.  |
| 2D Funky Plank  | 7602 | `fftResult[0..15]`                 | Bar height per band                                        | **CHSV from band index** — palette ignored.              |
| 2D Akemi        | 7688 | `fftResult[0]`, `fftResult[0..15]` | Akemi "dances" if bass loud; flanking GEQ bars             | Face uses `color_wheel(time)`; arms/legs use `colors[1]`.|
| PS GEQ 2D       | 9039 | `fftResult[0..15]`                 | Spawn rate + spawn velocity per column = band loudness     | Particle hue from band; palette wraps it.                |
| PS GEQ Nova     | 9117 | `fftResult[0..15]`                 | Spawn rate per spray (16 sprays in a circle)               | Per-spray hue cycling; palette wraps it.                 |

### Particle (1D)

| Effect          |  Line | Reads                  | Modulates                                              | Palette / color                                              |
| --------------- | ----: | ---------------------- | ------------------------------------------------------ | ------------------------------------------------------------ |
| PS GEQ 1D       | 10391 | `fftResult[0..15]`     | Spawn rate per source = band loudness                  | Particle hue per source; palette wraps it.                   |
| PS Sonic Stream | 10514 | `fftResult[bin]`, mids | Beat-threshold spawn; particle size = speed slider     | `custom1` slider blends white→hue→position-based.            |
| PS Sonic Boom   | 10613 | `fftResult[bin]`, mids | Explosion size on beat; position cycle                 | `custom1` blends white→cycling hue→position.                 |
| PS Springy (AR) | 10773 | `fftResult[bin]`       | Push the center spring particle on beat                | Particle hue by speed/density (audio doesn't paint colors).  |

## How effects feel: three recurring patterns

### Pattern A: "trigger + fade" causes the flash

Almost every beat-or-volume-driven effect is structured the same way:

1. Each frame, fade the strip toward black: `SEGMENT.fade_out(N)` or
   `fadeToBlackBy(N)` (`N` is typically 224..254 and exposed as **Fade rate**
   or **Speed**).
2. On a beat (or when volume crosses a threshold), write a few bright pixels.

`fade_out(254)` at ~50 fps means a pixel halves in brightness in roughly four
frames (~80 ms) — visually a hard flash. There is no rise: a pixel goes from
black to peak in one frame.

The "Speed" slider on these effects is therefore not really speed — it's the
*decay constant of the flash*. The smaller the value, the slower the fade and
the more ambient the look; the larger, the harder the flash.

### Pattern B: audio drives the palette index, not the color

Effects fall into three buckets w.r.t. color:

- **Palette indexed by an audio quantity** (Freqmap, Waterfall, Ripple Peak,
  Noisemove, Blurz, Pixels, Gravcentric, …). The user picks the palette,
  audio picks the *index into it*. If the chosen palette is just one color
  repeated, every pixel collapses to that color — only brightness varies.
- **Palette indexed by time** (Matripix, Pixelwave, Juggles, Puddles). Audio
  drives brightness; color cycles through the palette as time passes.
- **Direct HSV/RGB synthesis** (Freqmatrix, Freqwave, DJ Light, Funky Plank,
  Noisefire). The palette is *ignored entirely* — color comes from frequency
  or band index. The user's color choice has no effect.

`SEGCOLOR(0)` ("Fx" / primary color) is rarely the driver — it usually only
shows up via palette 2/3 (which are derived from `colors[0..2]`). Many
effects use `SEGCOLOR(1)` ("Bg") only as the background to blend toward
when fading.

### Pattern C: receiver smoothing exists but is tuned for "punchy"

The usermod itself already smooths audio in three places:

- `volumeSmth` is the AGC envelope follower with smoothing factor
  `1/12 .. 1/16` — fast attack, medium release.
- `fftAvg[i]` is the per-band rise-fast/fall-slow filter, with α selected by
  the usermod's **Decay Time** setting (the default is 1400 ms → α 0.17).
- `attackTime` / `decayTime` are both exposed in the usermod settings.

This is bypassed in **Receive** mode — the sender (pwled) is in full control
of how smooth the values look. That's why the smoothing knobs in pwled are
the biggest lever for switching between "ambient" and "twitchy" without
touching WLED.

## Picking a pwled preset for the look you want

pwled ships five presets that pre-tune the sender-side smoothing/AGC chain
for different vibes (see [`internal/config/config.go`](../internal/config/config.go),
`defaultPresets`):

| Preset  | When to use                                             | What it changes vs. defaults                                                 |
| ------- | ------------------------------------------------------- | ----------------------------------------------------------------------------- |
| Flashy  | Parties; effects with high Fade rate / Speed.           | Vivid AGC, short smoothing (30/120 sample, 30/250 band), low beat interval.   |
| Punchy  | Music with strong transients you want to feel.          | Fast attack, slow release on bands (25/700) — hits land then linger.          |
| Vivid   | Mixed listening; bright but not seizure-y.              | Vivid AGC, moderate smoothing (40/300 sample, 60/1000 band).                  |
| Smooth  | Ambient; long fall times, gentle beats.                 | Lazy AGC, long releases (800 sample, 2500 band), beat median gate.            |
| Lazy    | "Slow glow" — barely reactive, follows overall loudness.| Lazy AGC, very long smoothing (2s sample, 4s band), high beat threshold.      |

You can also build your own from the web UI's **Settings** panel and save it
as a preset (it persists into `~/.config/pwled/config.toml`).

## Making colors behave on audio effects

Because audio drives the palette *index*, not the color itself, the practical
mental model is: **on audio effects, "color" lives in the palette, not in
Color 1.**

Things that actually work:

- Use palette **Color Gradient** (palette ID 5) or **Set Colors** (3/4) and
  put your desired ambient colors into Color 1/2/3. The audio is now indexing
  *your* colors instead of "Party".
- Upload a custom palette that's one hue at varying brightness — the palette
  index then only controls brightness, never hue.
- Avoid Freqmatrix / Freqwave / DJ Light / Funky Plank / Noisefire if you want
  palette respect — they synthesise color directly and ignore your palette.

pwled can't change *how* an effect maps audio to color (that's in `FX.cpp`),
but the active preset still matters: a smoother preset narrows the range of
audio values being indexed into the palette, which collapses color jitter
even on effects that index by audio.

## Making effects calmer

Three knobs, in order of effort:

1. **In pwled** — pick or build a slower preset (Smooth / Lazy). The biggest
   knob is `smoothing.band_release_ms`; values around 1500–3000 ms approach
   the upper end of WLED's own Decay Time path. Also raise
   `beat.min_interval_ms` and enable `beat.median_gate` so the receiver sees
   fewer `samplePeak=1` frames.
2. **In WLED** — on the AudioReactive usermod, raise **Decay Time** to
   ~2500–3000 ms so the receiver's own per-band filter (`fftAvg`) runs at the
   slowest α. Then on each effect lower **Fade rate** / **Speed** toward the
   bottom of its range — that's the actual decay constant on the per-frame
   `fade_out(N)`.
3. **In firmware** — the structural fix is replacing one-shot `fade_out(N)`
   with a persistent buffer + lerp (the `mode_matripix` pattern), but that
   requires forking WLED and is out of scope for pwled.
