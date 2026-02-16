# Architecture

This document describes the current `algo-piano` architecture as implemented in this repository, including both string-core modes:

- `dwg` (digital waveguide)
- `modal` (modal resonator bank)

## 1. System Overview

`algo-piano` is a physically informed piano synthesizer with a hybrid architecture:

1. Event/control layer (note on/off, pedal state, model selection)
2. Excitation layer (nonlinear hammer model + optional attack noise)
3. String-bank layer (`dwg` or `modal`, selectable at runtime)
4. Sympathetic resonance injection layer
5. Linear body/room rendering layer (partitioned convolution IRs)
6. Stereo output mix layer

Core render path per audio block:

```text
MIDI/UI Events
  -> HammerExciter + RingingState(StringBank[dwg|modal])
  -> mono bridge/string mix
  -> optional ResonanceEngine injection into undamped notes
  -> BodyConvolver (mono->mono)
  -> Room/SoundboardConvolver (mono->stereo)
  -> output mix/gain
```

## 2. Main Runtime Components

### 2.1 `piano.Piano` (top-level engine)

Main struct owns:

- `keyStateTracker` (key-down + last velocity per MIDI note)
- `HammerExciter`
- `RingingState` (contains `StringBank`)
- `BodyConvolver` (mono IR)
- `SoundboardConvolver` (stereo IR)
- optional `ResonanceEngine`
- pedal states (`sustainPedal`, `softPedal`)

Important behavior:

- `maxPolyphony` in `NewPiano` is currently retained for API compatibility but ignored internally.
- `SetStringModel("dwg"|"modal")` rebuilds key/runtime state and preserves:
  - held keys
  - last velocities
  - sustain/soft pedal state
- Model switch does **not** preserve existing string internal energy; it reinitializes the ringing engine.

### 2.2 `RingingState` and `StringBank`

`RingingState` is a thin wrapper around `StringBank`.

`StringBank` contains one persistent group per MIDI note in the configured inclusive range (`min_note..max_note`, default `21..108`) and can host:

- `RingingStringGroup` for `dwg`
- `ModalStringGroup` for `modal`

Each group implements a shared interface with:

- key/sustain damping control
- hammer force injection
- coupling force injection
- per-sample processing
- block-end activity gating

Active-note optimization:

- Bank tracks `activeNotes` and only processes active groups.
- Inactive notes stay allocated but are skipped.

## 3. String-Core Modes

## 3.1 DWG Mode (`string_model = "dwg"`)

Implementation type: `StringWaveguide`.

Per string state:

- fractional delay line (pitch via `delayLength = fs/f0`)
- loop reflection gain (`baseReflection`)
- damper reflection override (`damperReflection`)
- one-pole loop lowpass (`lowpassCoeff`, `loopState`)
- simple dispersion allpass chain (`dispersionCoeff`, 2-stage state)

Per-sample update:

1. Read delayed sample with linear interpolation.
2. Apply dispersion allpass stages.
3. Apply loop-loss one-pole lowpass.
4. Multiply by reflection gain (damper-aware).
5. Write back to delay line.
6. Output delayed sample.

Per-note behavior:

- Notes use unison allocation by register:
  - `< 40`: 1 string
  - `40..69`: 2 strings
  - `>= 70`: 3 strings
- Detune/gain defaults are applied per unison string.
- Each note group can apply per-note overrides (`loss`, `inharmonicity`, `strike_position`).

## 3.2 Modal Mode (`string_model = "modal"`)

Implementation type: `ModalStringGroup`.

Each unison string contains multiple damped modes (`modalMode`), each with:

- oscillator rotation terms (`cosW`, `sinW`)
- gain by order (`1 / order^ModalGainExponent`)
- undamped and damped decay coefficients
- complex state (`re`, `im`)

Per-sample update for each mode:

```text
nx = decay * (re*cosW - im*sinW)
ny = decay * (re*sinW + im*cosW)
re, im = nx, ny
sample += nx * modeGain
```

Mode generation:

- Partial frequency from base `f0`, order, and inharmonicity.
- Truncated at Nyquist safety margin (`< 0.95 * Nyquist`).
- Fallback mode is created if no partial survives.

Key knobs:

- `modal_partials`
- `modal_gain_exponent`
- `modal_excitation`
- `modal_undamped_loss`
- `modal_damped_loss`

Damper semantics:

- Key up + no sustain -> use damped decay
- Key down or sustain down -> use undamped decay

## 4. Shared Subsystems (Both Modes)

### 4.1 Hammer / excitation

`HammerExciter` manages short-lived note strike events:

- nonlinear felt hammer contact (`Hammer.Step`)
- per-note strike position
- soft pedal influence:
  - strike position offset
  - reduced hammer hardness
- optional attack-noise burst:
  - short decaying noise injection
  - optional spectral color tilt

Injected force enters the currently active string model through common injection methods.

### 4.2 Coupling (inter-note energy transfer)

`StringBank` supports sparse coupling modes:

- `off`
- `static`: fixed octave/fifth edges
- `physical`: graph from partial alignment + detune penalty + keyboard distance penalty

Coupling is applied blockwise:

- source drive computed from block-level note output stats
- edges inject bounded force into target notes
- scaled by active polyphony to avoid overload

### 4.3 Sympathetic resonance

`ResonanceEngine` processes bridge mono signal:

- DC removal + lowpass band-limiting
- scaled injection into undamped note targets
- optional per-note resonance filter before injection

Targets are the currently selected string groups (DWG or modal), so resonance works with both modes.

### 4.4 Body and room convolution

Two-stage linear rendering:

1. `BodyConvolver`: mono input -> mono output
2. `SoundboardConvolver` (room stage): mono input -> stereo output

Both are partitioned overlap-add convolution using `algo-dsp`.

IR loading behavior:

- Body IR can load from `BodyIRWavPath`
- Room IR uses `RoomIRWavPath`, fallback to legacy `IRWavPath`
- WAV IRs are resampled to runtime sample rate if needed

### 4.5 Final output mix

Final stereo sample uses:

- body-dry contribution
- room-wet contribution
- per-stage gains
- final output gain

Legacy single-IR fields are mapped for backward compatibility when dual-IR paths are not set.

## 5. Runtime Mode Selection (`dwg` vs `modal`)

Mode can be chosen by:

1. Preset field: `"string_model": "dwg" | "modal"`
2. Runtime API: `Piano.SetStringModel(...)`
3. Web UI selector (`DWG` / `Modal`) -> WASM `wasmSetStringModel`

Defaults:

- Engine default (`NewDefaultParams`) is `dwg`.
- Web demo initializes with `dwg`, but sets tuned modal knob defaults so switching to `modal` is immediately usable.

## 6. Configuration Surface

Preset loader (`preset/json.go`) validates and applies:

- global gains/mix
- IR paths
- hammer scales
- string model and modal knobs
- coupling mode and parameters
- per-note overrides:
  - `f0`
  - `inharmonicity`
  - `loss`
  - `strike_position`

Invalid `string_model` values are rejected (`must be one of dwg|modal`).

## 7. WebAssembly + Web Frontend Architecture

`cmd/piano-wasm` exports JS-callable functions:

- init and render: `wasmInit`, `wasmProcessBlock`
- note and pedal control: `wasmNoteOn`, `wasmKeyDown`, `wasmNoteOff`, `wasmSetSustain`
- model/coupling control: `wasmSetStringModel`, `wasmSetCouplingMode`

Frontend (`web/main.js`) responsibilities:

- load WASM and initialize engine with browser sample rate
- UI control wiring (keyboard, mouse, pedals, mode selectors)
- audio callback rendering in chunks (128-frame synth chunks)
- runtime calls for coupling/mode changes

Current limitation:

- `wasmLoadIR` receives IR bytes but runtime IR application is marked TODO in WASM entrypoint.

## 8. Offline Tooling Around the Core Architecture

Key commands:

- `cmd/piano-render`: offline note rendering
- `cmd/piano-distance`: objective reference/candidate comparison (`analysis.Compare`)
- `cmd/piano-modal-fit`: calibrates modal knobs against DWG reference renders and writes modal preset/report
- `cmd/piano-fit`: broader optimization workflow
- `cmd/ir-synth`: synthetic IR generation (body/room style IR assets)

This supports a practical workflow:

1. Build a high-quality DWG reference preset.
2. Fit/calibrate modal parameters to match DWG behavior for low-CPU profile.
3. Compare with objective metrics.

## 9. Design Intent

The architecture keeps excitation, resonance, coupling, and linear body/room rendering shared between both cores, while isolating the string-core choice (`dwg` vs `modal`) behind a common group interface. This enables:

- runtime model switching
- shared control surface/preset format
- consistent pedal and resonance behavior across modes
- a DWG-quality reference path and a modal low-CPU path in the same engine
