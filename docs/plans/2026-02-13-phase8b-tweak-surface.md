# Phase 8B Tweak Surface (Initial)

This is the first optimization parameter surface for distance-guided fitting against `reference/c4.wav`.

## Hammer Influence

Preset fields:
- `hammer_stiffness_scale` (default `1.0`)
- `hammer_exponent_scale` (default `1.0`)
- `hammer_damping_scale` (default `1.0`)
- `hammer_initial_velocity_scale` (default `1.0`)
- `hammer_contact_time_scale` (default `1.0`)

Suggested search bounds for Mayfly (C4 fit):
- stiffness scale: `[0.6, 1.8]`
- exponent scale: `[0.8, 1.2]`
- damping scale: `[0.6, 1.8]`
- initial velocity scale: `[0.7, 1.4]`
- contact time scale: `[0.7, 1.5]`

Primary perceptual impact:
- attack brightness and hardness
- onset transient shape
- early decay behavior

## Detuning / Unison Influence

Preset fields:
- `unison_detune_scale` (default `1.0`)
- `unison_crossfeed` (default `0.0008`)

Suggested search bounds:
- detune scale: `[0.0, 2.0]`
- crossfeed: `[0.0, 0.005]`

Primary perceptual impact:
- beating rate and width
- chorus/bloom thickness
- sustain texture

## IR / Body Influence

Preset fields:
- `ir_wet_mix` (default `1.0`)
- `ir_dry_mix` (default `0.0`)
- `ir_gain` (default `1.0`)
- `ir_wav_path` (discrete choice)

Suggested search bounds:
- wet mix: `[0.4, 1.4]`
- dry mix: `[0.0, 0.7]`
- ir gain: `[0.6, 1.8]`

Primary perceptual impact:
- body coloration and stereo field
- direct-vs-room balance
- global tonal tilt and tail color

## Additional Phase 8B controls already available

- `output_gain`
- `soft_pedal_strike_offset`
- `soft_pedal_hardness`
- per-note overrides: `loss`, `inharmonicity`, `strike_position`

## Recommended optimization order

1. Render controls: `release-after`, `output_gain`
2. IR controls: `ir_wet_mix`, `ir_dry_mix`, `ir_gain`
3. Hammer controls
4. Unison controls
5. Per-note controls for C4 (`loss`, `inharmonicity`, `strike_position`)

## Objective

Use `cmd/piano-distance` score as the primary objective and track the sub-metrics:
- `envelope_rmse_db`
- `spectral_rmse_db`
- `decay_diff_db_per_s`

