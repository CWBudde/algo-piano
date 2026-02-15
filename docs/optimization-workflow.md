# Optimization Workflow: Systematic Piano Fitting

## Goal

Get progressively closer to a reference recording without regressions, using alternating optimization stages with different knob groups via the unified `piano-fit` tool.

## Architecture Overview

The `piano-fit` tool optimizes different parameter groups selected via `--optimize`:

| Groups | What it optimizes | IR handling |
| ------ | ----------------- | ----------- |
| `piano,mix` | Piano synthesis knobs (hammer, unison, per-note, output gain, IR mix) | Fixed IR loaded from preset |
| `body-ir,mix` | Body IR synthesis knobs + mix levels | Generates mono body IR per eval |
| `body-ir,room-ir,mix` | Body + room IR synthesis knobs + mix | Generates body + room IRs per eval |
| `piano,body-ir,room-ir,mix` | All knobs jointly | Generates IRs + optimizes piano knobs |

The idea: alternate between piano-only and IR stages so each builds on the previous best result.

## Workflow

```
Stage 1: --optimize=piano,mix         (piano knobs, default IR)
    ↓ output preset
Stage 2: --optimize=body-ir,mix       (body IR, fixed piano from Stage 1)
    ↓ output preset + body IR WAV
Stage 3: --optimize=piano,mix         (piano knobs, fixed body IR from Stage 2)
    ↓ output preset (preserves body IR)
Stage 4: --optimize=piano,body-ir,room-ir,mix  (joint optimization)
    ↓ output preset + body IR WAV + room IR WAV
Stage 5: --optimize=piano,mix         (final polish with dual IR)
    ↓ final preset
```

Each stage's output preset becomes the next stage's input. The preset carries forward all previously optimized parameters.

### Stage 1: Initial Piano Fit

Optimize piano synthesis against reference with the default IR.

```bash
go run --tags asm ./cmd/piano-fit \
    --reference reference/c4.wav \
    --preset assets/presets/default.json \
    --output-preset out/stages/stage1.json \
    --optimize piano,mix \
    --note 60 \
    --time-budget 300 \
    --max-evals 5000 \
    --workers auto
```

**What this does:** Finds best hammer parameters, per-note tuning (loss, inharmonicity, strike position), output gain, and IR mix levels using the default IR.

### Stage 2: Body IR Fit

Optimize a short mono body IR using the piano knobs from Stage 1.

```bash
go run --tags asm ./cmd/piano-fit \
    --reference reference/c4.wav \
    --preset out/stages/stage1.json \
    --output-preset out/stages/stage2.json \
    --output-ir out/stages/stage2-ir.wav \
    --optimize body-ir,mix \
    --note 60 \
    --time-budget 300 \
    --max-evals 2000 \
    --workers auto \
    --resume=false
```

**What this does:** Synthesizes body coloration IR (short, mono) while keeping piano knobs fixed from Stage 1.

### Stage 3: Refine Piano with Body IR

Re-optimize piano knobs now that we have a better body IR.

```bash
go run --tags asm ./cmd/piano-fit \
    --reference reference/c4.wav \
    --preset out/stages/stage2.json \
    --output-preset out/stages/stage3.json \
    --optimize piano,mix \
    --note 60 \
    --time-budget 300 \
    --max-evals 5000 \
    --workers auto \
    --resume=false
```

**What this does:** The body IR changes the tonal character, so piano knobs that were optimal with the default IR may no longer be optimal. This stage re-tunes them.

### Stage 4: Joint IR + Piano Refinement

Joint optimization of both IR synthesis and piano parameters.

```bash
go run --tags asm ./cmd/piano-fit \
    --reference reference/c4.wav \
    --preset out/stages/stage3.json \
    --output-preset out/stages/stage4.json \
    --output-ir out/stages/stage4-ir.wav \
    --optimize piano,body-ir,room-ir,mix \
    --note 60 \
    --time-budget 600 \
    --max-evals 3000 \
    --workers auto \
    --resume=false
```

**What this does:** Jointly optimizes IR synthesis AND piano knobs (30 knobs total). This can find interactions between IR shape and piano parameters that alternating stages miss. Give it more time since the search space is larger.

### Stage 5: Final Polish

One more piano-only pass to fine-tune with the joint-optimized IR.

```bash
go run --tags asm ./cmd/piano-fit \
    --reference reference/c4.wav \
    --preset out/stages/stage4.json \
    --output-preset out/stages/final.json \
    --optimize piano,mix \
    --note 60 \
    --time-budget 300 \
    --max-evals 5000 \
    --workers auto \
    --resume=false
```

## Preventing Regressions

### Principle: Each stage must not regress the previous best score

Currently there is no automatic regression check. To verify manually:

```bash
# Render each stage's preset and compute distance
for stage in stage1 stage2 stage3 stage4 final; do
    go run --tags asm ./cmd/piano-render \
        --preset "out/stages/${stage}.json" \
        --output "out/stages/${stage}.wav" \
        --note 60 --velocity 118 --duration 5.0

    go run --tags asm ./cmd/piano-distance \
        --reference reference/c4.wav \
        --candidate "out/stages/${stage}.wav"
done
```

If a stage regresses, discard it and re-run with different seed or longer budget. The previous stage's output is always intact.

### Why regressions can happen

1. **Piano-only after IR change:** The new IR changes what's "optimal" for piano knobs. The optimizer explores from defaults, not from the previous best in the old IR space. The fix is ensuring `--preset` carries forward the previous piano knobs as starting points.

2. **Joint optimization (all groups):** Joint optimization has 30 dimensions. With limited budget, it may not find solutions as good as the separated stages. Use longer budgets for joint runs.

3. **Different mix knob semantics:** Legacy (`ir_wet_mix`) vs dual-IR (`body_dry`, `room_wet`) use different mix logic. A preset switching between formats can produce different audio even with "equivalent" values.

## File Layout

```
out/stages/
├── stage1.json                        # Piano knobs + default IR
├── stage1.json.report.json            # Resume data
├── stage2-ir-body.wav                 # Mono body IR
├── stage2-ir-room.wav                 # Stereo room IR
├── stage2.json                        # Piano knobs + body IR path
├── stage2.json.report.json            # Resume data
├── stage3.json                        # Refined piano knobs + body IR
├── stage4-ir-body.wav                 # Refined body IR
├── stage4-ir-room.wav                 # Refined room IR
├── stage4.json                        # Joint-optimized everything
└── final.json                         # Final polished preset
```

## Resuming Within a Stage

`piano-fit` supports `--resume` (default: true). To continue a run that was interrupted:

```bash
# Just re-run the same command — it reads the report and continues
go run --tags asm ./cmd/piano-fit \
    --preset out/stages/stage1.json \
    --output-preset out/stages/stage1.json \
    --optimize piano,mix \
    --time-budget 600   # more time
    # --resume is true by default
```

The report file (`*.report.json`) stores the best knob values under `best_knobs`. On resume, these become the initial candidate, so the optimizer starts from the previous best rather than from scratch.

Resume works across optimization modes: knob names are shared, so a report from `--optimize=piano,mix` can seed a `--optimize=piano,body-ir,room-ir,mix` run (the piano/mix knobs carry over, IR knobs start from defaults).

## Quick Smoke Test

To verify the full pipeline works end-to-end with minimal time:

```bash
# Stage 1: piano knobs
go run --tags asm ./cmd/piano-fit \
    --reference reference/c4.wav \
    --preset assets/presets/default.json \
    --output-preset out/smoke/s1.json \
    --optimize piano,mix \
    --note 60 --time-budget 30 --max-evals 100 --workers auto

# Stage 2: body IR
go run --tags asm ./cmd/piano-fit \
    --reference reference/c4.wav \
    --preset out/smoke/s1.json \
    --output-preset out/smoke/s2.json \
    --output-ir out/smoke/s2-ir.wav \
    --optimize body-ir,mix \
    --note 60 --time-budget 30 --max-evals 100 --workers auto --resume=false

# Stage 3: refine piano with body IR
go run --tags asm ./cmd/piano-fit \
    --reference reference/c4.wav \
    --preset out/smoke/s2.json \
    --output-preset out/smoke/s3.json \
    --optimize piano,mix \
    --note 60 --time-budget 30 --max-evals 100 --workers auto --resume=false
```
