# algo-piano

A physically-modeled piano synthesizer written in Go, using digital waveguide synthesis and commuted convolution.

## Status: Through Phase 6 (core voice path) ✓

Implemented through the core instrument path:

- Waveguide strings with loop loss + dispersion
- Nonlinear hammer with bounded contact
- Unison strings (1-3) with detune and coupling
- Partitioned stereo soundboard convolver with reset
- WAV IR loading (mono/stereo) with runtime sample-rate conversion
- Damper and sustain pedal behavior

### What's Working

- ✓ Go module structure
- ✓ Core API types (Piano, Voice, StringWaveguide)
- ✓ WAV file writer
- ✓ Basic DSP utilities (Biquad, DelayLine, Lagrange interpolator)
- ✓ **Digital waveguide string model** with fractional delay tuning
- ✓ **Stable pitched tones** across the keyboard
- ✓ Loss, dispersion, strike-position, hammer, unison, convolver, and sustain tests
- ✓ `piano-render` command produces pitched WAV output

### Quick Start

```bash
# Render a test tone (A4 = 440 Hz) with default 96 kHz IR asset
go run ./cmd/piano-render --note 69 --duration 2.0 --output output.wav

# Try a different runtime sample-rate (IR is resampled automatically)
go run ./cmd/piano-render --note 69 --sample-rate 44100 --output a4_44k.wav

# Select a custom IR WAV
go run ./cmd/piano-render --note 60 --ir assets/ir/default_96k.wav --output middle-c.wav
```

### Project Structure

```
algo-piano/
├── cmd/
│   ├── piano-render/    # Offline WAV renderer
│   └── piano-play/      # (TODO) Realtime playback
├── piano/               # Public engine API
├── dsp/                 # DSP utilities and WAV I/O
├── conv/                # Partitioned convolution (TODO)
├── preset/              # Parameter schema (TODO)
├── assets/
│   ├── ir/              # Impulse responses (TODO)
│   └── presets/         # Preset files (TODO)
└── examples/            # Example code (TODO)
```

## Implementation Plan

See [PLAN.md](PLAN.md) for the full phase-by-phase implementation plan.

**Completed:** Phase 0 through Phase 6 (except optional items)

**Next up:** Phase 7 - sympathetic resonance

## Dependencies

- [github.com/cwbudde/algo-approx](https://github.com/cwbudde/algo-approx) - Fast math approximations
- [github.com/cwbudde/algo-dsp](https://github.com/cwbudde/algo-dsp) - Overlap-add convolution and resampling
- [github.com/cwbudde/wav](https://github.com/cwbudde/wav) - WAV encode/decode for IR assets

## Design Goals

From [goal.md](goal.md):

- Digital waveguide strings with nonlinear hammer model
- Commuted synthesis (IR convolution for soundboard/body)
- Pure Go implementation
- Realtime capable with good polyphony
- WebAssembly target for browser demo

## References

- [goal.md](goal.md) - High-level design and algorithm choices
- [research.md](research.md) - Literature review and implementation notes
- [PLAN.md](PLAN.md) - Detailed implementation roadmap
