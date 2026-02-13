# algo-piano

A physically-modeled piano synthesizer written in Go, using digital waveguide synthesis and commuted convolution.

## Quick Start

**Try the live web demo:** https://cwbudde.github.io/algo-piano/

Or render offline WAV files:

```bash
# Render a test tone (A4 = 440 Hz) with default 96 kHz IR asset
go run ./cmd/piano-render --note 69 --duration 2.0 --output output.wav

# Try a different runtime sample-rate (IR is resampled automatically)
go run ./cmd/piano-render --note 69 --sample-rate 44100 --output a4_44k.wav

# Render with preset JSON (default: assets/presets/default.json)
go run ./cmd/piano-render --preset assets/presets/default.json --note 60 --output middle-c.wav

# Override IR from CLI (takes precedence over preset)
go run ./cmd/piano-render --preset assets/presets/default.json --ir assets/ir/default_96k.wav --output middle-c-ir.wav

# Render one octave (12 WAV files) with auto-stop at -90 dBFS decay
just render-octave root=60 out_dir=out/octave

# Measure objective distance to a recorded C4 reference
just distance-c4 reference=reference/c4.wav

# Synthesize a new stereo IR (modal+diffuse synthetic body IR)
just ir-synth output=assets/ir/synth_96k.wav sample_rate=96000 duration=2.0 modes=128 seed=1

# Run fast inner-loop fitting for C4 (writes fitted preset + report)
just fit-c4-fast reference=reference/c4.wav preset=assets/presets/default.json output_preset=assets/presets/fitted-c4.json time_budget=120
```

Or build the web demo locally:

```bash
./scripts/build-wasm.sh
python3 -m http.server -d web 8080
# Open http://localhost:8080
```

See [web/README.md](web/README.md) for web demo details.

### Project Structure

```
algo-piano/
├── cmd/
│   ├── piano-render/    # Offline WAV renderer
│   └── piano-play/      # (TODO) Realtime playback
├── piano/               # Public engine API
├── dsp/                 # DSP utilities and WAV I/O
├── conv/                # Partitioned convolution (TODO)
├── preset/              # Preset schema + JSON loader
├── assets/
│   ├── ir/              # Impulse responses (TODO)
│   └── presets/         # Preset files
└── examples/            # Example code (TODO)
```

## Implementation Plan

See [PLAN.md](PLAN.md) for the full phase-by-phase implementation plan.

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
