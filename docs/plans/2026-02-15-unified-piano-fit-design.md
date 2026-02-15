# Unified piano-fit Tool

## Problem

Two separate commands (`piano-fit-fast`, `piano-fit-ir`) optimize different parameter groups with incompatible report formats. This creates friction in multi-stage workflows:

- Gap 3: `--resume` only works within the same tool since `best_knobs` vs `best_ir_knobs` keys differ.
- Users must remember which tool to call for each stage.
- Shared code is duplicated across both commands (knob definitions, mayfly config, rendering, output writing).

## Solution

Merge both tools into a single `piano-fit` command with a `--optimize` flag that selects which knob groups to include.

## Knob Groups

| Group     | Knobs                                                                                                                  | Count |
| --------- | ---------------------------------------------------------------------------------------------------------------------- | ----- |
| `piano`   | output*gain, hammer*_, unison\__, per_note.N.loss/inharmonicity/strike_position, render.velocity, render.release_after | 12    |
| `body-ir` | body_modes, body_brightness, body_density, body_direct, body_decay, body_duration                                      | 6     |
| `room-ir` | room_early, room_late, room_stereo_width, room_brightness, room_low_decay, room_high_decay, room_duration              | 7     |
| `mix`     | body_dry, body_gain, room_wet, room_gain (or legacy ir_wet_mix, ir_dry_mix, ir_gain)                                   | 3-4   |

Default: `--optimize=piano,mix` (equivalent to old `piano-fit-fast`).

## Evaluation Logic

When `body-ir` or `room-ir` is in the optimize set:

1. Generate body/room IR from candidate knobs via `irsynth.GenerateBody`/`irsynth.GenerateRoom`.
2. Clear IR WAV paths on params so `NewPiano` doesn't load from disk.
3. Set IR buffers directly via `Piano.SetBodyIR()`/`Piano.SetRoomIR()`.
4. Render and compare.

When neither IR group is active:

1. Apply piano/mix knobs to params.
2. `NewPiano` loads IR from disk via paths in the preset.
3. Render and compare.

## CLI Flags

All flags from both tools, unified:

**Core:**

- `--reference` (default: `reference/c4.wav`)
- `--preset` (default: `assets/presets/default.json`)
- `--output-preset` (required)
- `--optimize` (default: `piano,mix`)
- `--report` (default: `<output-preset>.report.json`)
- `--note` (default: 60)
- `--sample-rate` (default: 48000)
- `--seed` (default: 1)
- `--time-budget` (default: 120)
- `--max-evals` (default: 10000)
- `--workers` (default: `1`)
- `--resume` (default: true)
- `--resume-report`

**IR-specific (required when body-ir/room-ir active):**

- `--output-ir` (base path; body and room WAVs derived as `*-body.wav`, `*-room.wav`)
- `--work-dir` (default: `out/piano-fit`)

**Rendering:**

- `--velocity` (default: 118)
- `--release-after` (default: 3.5)
- `--decay-dbfs` (default: -90)
- `--decay-hold-blocks` (default: 6)
- `--min-duration` (default: 2.0)
- `--max-duration` (default: 30.0)
- `--opt-sample-rate` (default: 0, uses --sample-rate)
- `--opt-min-duration` (default: -1, uses --min-duration)
- `--opt-max-duration` (default: -1, uses --max-duration)
- `--render-block-size` (default: 128)

**Refinement:**

- `--refine-top-k` (default: 3)
- `--top-k` (default: 5)

**Output:**

- `--write-best-candidate` (optional WAV snapshot)

**Mayfly:**

- `--mayfly-variant` (default: desma)
- `--mayfly-pop` (default: 10)
- `--mayfly-round-evals` (default: 240)

## Report Format (unified)

```json
{
  "reference_path": "...",
  "preset_path": "...",
  "output_preset": "...",
  "output_ir": "...",
  "sample_rate": 48000,
  "note": 60,
  "velocity": 118,
  "release_after_seconds": 3.5,
  "elapsed_seconds": 120.5,
  "evaluations": 5000,
  "mayfly_variant": "desma",
  "optimize_groups": ["piano", "mix"],
  "best_score": 0.38,
  "best_similarity": 0.21,
  "best_metrics": {},
  "best_knobs": {},
  "checkpoint_count": 5,
  "top_candidates": []
}
```

Single `best_knobs` map. Resume reads any matching knob name regardless of which groups produced them.

## File Layout

```
cmd/piano-fit/
  main.go           # CLI parsing, unified entry point
  knobs.go          # knobDef, initCandidate, applyCandidate (group-aware)
  optimize.go       # unified optimization loop
  output.go         # preset/report/WAV writing
  utils.go          # die, clamp, minInt, maxInt
  wav.go            # WAV I/O wrappers
  knobs_test.go     # knob group tests
  main_test.go      # integration tests
  optimize_test.go  # optimization config tests
```

## Deletions

- `cmd/piano-fit-fast/` (entire directory)
- `cmd/piano-fit-ir/` (entire directory)

## Updated Workflow

```bash
# Stage 1: piano knobs + mix (was piano-fit-fast)
go run --tags asm ./cmd/piano-fit \
    --reference reference/c4.wav \
    --preset assets/presets/default.json \
    --output-preset out/stages/stage1.json \
    --optimize piano,mix \
    --note 60 --time-budget 300 --max-evals 5000 --workers auto

# Stage 2: body IR (was piano-fit-ir)
go run --tags asm ./cmd/piano-fit \
    --reference reference/c4.wav \
    --preset out/stages/stage1.json \
    --output-preset out/stages/stage2.json \
    --output-ir out/stages/stage2-ir.wav \
    --optimize body-ir,mix \
    --note 60 --time-budget 300 --max-evals 2000 --workers auto --resume=false

# Stage 3: refine piano with body IR
go run --tags asm ./cmd/piano-fit \
    --reference reference/c4.wav \
    --preset out/stages/stage2.json \
    --output-preset out/stages/stage3.json \
    --optimize piano,mix \
    --note 60 --time-budget 300 --max-evals 5000 --workers auto --resume=false

# Stage 4: joint optimization
go run --tags asm ./cmd/piano-fit \
    --reference reference/c4.wav \
    --preset out/stages/stage3.json \
    --output-preset out/stages/stage4.json \
    --output-ir out/stages/stage4-ir.wav \
    --optimize piano,body-ir,room-ir,mix \
    --note 60 --time-budget 600 --max-evals 3000 --workers auto --resume=false
```
