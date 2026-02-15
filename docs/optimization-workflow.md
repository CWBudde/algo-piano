# Optimization Workflow: Systematic Piano Fitting

## Goal

Get progressively closer to a reference recording without regressions, using alternating stages of piano parameter optimization (`piano-fit-fast`) and IR synthesis optimization (`piano-fit-ir`).

## Architecture Overview

Two tools serve complementary purposes:

| Tool             | What it optimizes                                                     | IR handling                 |
| ---------------- | --------------------------------------------------------------------- | --------------------------- |
| `piano-fit-fast` | Piano synthesis knobs (hammer, unison, per-note, output gain, IR mix) | Fixed IR loaded from preset |
| `piano-fit-ir`   | IR synthesis knobs (body shape, room reverb, mix levels)              | Generates body + room IRs   |

The idea: alternate between them so each stage builds on the previous best result.

## Current Compatibility Gaps

Before running multi-stage workflows, these issues must be addressed:

### Gap 1: `piano-fit-fast` output drops dual-IR fields

`piano-fit-fast` writes only legacy IR fields (`ir_wav_path`, `ir_wet_mix`, `ir_dry_mix`, `ir_gain`). If its input preset had `body_ir_wav_path` and `room_ir_wav_path`, those are **lost** in the output. A subsequent `piano-fit-ir --preset=<fast-output>` would not see the previous best IRs.

**Fix needed:** `piano-fit-fast/output.go` `writePresetJSON` must preserve dual-IR fields when present.

### Gap 2: `piano-fit-fast` optimizes dead mix knobs with dual IR

When dual-IR paths are set, the engine ignores legacy `ir_wet_mix`/`ir_dry_mix`/`ir_gain` (the legacy compat path at `engine.go:150` only triggers when `RoomIRWavPath == "" && BodyIRWavPath == ""`). But `piano-fit-fast` still optimizes those legacy knobs, wasting 3 of its 16 optimization dimensions.

**Fix needed:** When dual-IR paths are present, `piano-fit-fast` should optimize `body_dry`, `body_gain`, `room_wet`, `room_gain` instead of the legacy mix knobs.

### Gap 3: No cross-tool report resumption

`piano-fit-fast` stores `best_knobs` in reports; `piano-fit-ir` stores `best_ir_knobs`. Neither reads the other's format. This isn't critical (preset files carry the state forward), but means `--resume` only works within the same tool across runs.

## Target Workflow (After Fixes)

```
Stage 1: piano-fit-fast    (piano knobs, default IR)
    ↓ output preset
Stage 2: piano-fit-ir      (body IR only, fixed piano knobs from Stage 1)
    ↓ output preset + body IR WAV
Stage 3: piano-fit-fast    (piano knobs, fixed body IR from Stage 2)
    ↓ output preset (preserves body IR)
Stage 4: piano-fit-ir      (body + room IR, fixed piano knobs from Stage 3)
    ↓ output preset + body IR WAV + room IR WAV
Stage 5: piano-fit-fast    (piano knobs, fixed dual IR from Stage 4)
    ↓ final preset
```

Each stage's output preset becomes the next stage's input. The preset carries forward all previously optimized parameters.

### Stage 1: Initial Piano Fit

Optimize piano synthesis against reference with the default IR.

```bash
go run --tags asm ./cmd/piano-fit-fast \
    --reference reference/c4.wav \
    --preset assets/presets/default.json \
    --output-preset out/stages/stage1-fast.json \
    --note 60 \
    --time-budget 300 \
    --max-evals 5000 \
    --workers auto
```

**What this does:** Finds best hammer parameters, per-note tuning (loss, inharmonicity, strike position), output gain, and IR mix levels using the default IR.

**Expected outcome:** Score ~0.38, similarity ~21%.

### Stage 2: Body IR Fit

Optimize a short mono body IR using the piano knobs from Stage 1.

```bash
go run --tags asm ./cmd/piano-fit-ir \
    --reference reference/c4.wav \
    --preset out/stages/stage1-fast.json \
    --output-ir out/stages/stage2-body-ir.wav \
    --output-preset out/stages/stage2-ir.json \
    --note 60 \
    --time-budget 300 \
    --max-evals 2000 \
    --workers auto \
    --resume=false
```

**What this does:** Synthesizes body coloration IR (short, mono) and room reverb IR (stereo) while keeping piano knobs fixed from Stage 1.

### Stage 3: Refine Piano with Body IR

Re-optimize piano knobs now that we have a better body IR.

```bash
go run --tags asm ./cmd/piano-fit-fast \
    --reference reference/c4.wav \
    --preset out/stages/stage2-ir.json \
    --output-preset out/stages/stage3-fast.json \
    --note 60 \
    --time-budget 300 \
    --max-evals 5000 \
    --workers auto \
    --resume=false
```

**What this does:** The body IR changes the tonal character, so piano knobs that were optimal with the default IR may no longer be optimal. This stage re-tunes them.

### Stage 4: Joint IR + Piano Refinement

Final joint optimization of both IR and piano parameters.

```bash
go run --tags asm ./cmd/piano-fit-ir \
    --reference reference/c4.wav \
    --preset out/stages/stage3-fast.json \
    --output-ir out/stages/stage4-ir.wav \
    --output-preset out/stages/stage4-joint.json \
    --note 60 \
    --time-budget 600 \
    --max-evals 3000 \
    --workers auto \
    --optimize-joint \
    --resume=false
```

**What this does:** Jointly optimizes IR synthesis AND piano knobs (29 knobs total). This can find interactions between IR shape and piano parameters that alternating stages miss. Give it more time since the search space is larger.

### Stage 5: Final Polish

One more piano-fit-fast pass to fine-tune with the joint-optimized IR.

```bash
go run --tags asm ./cmd/piano-fit-fast \
    --reference reference/c4.wav \
    --preset out/stages/stage4-joint.json \
    --output-preset out/stages/final.json \
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
for stage in stage1-fast stage2-ir stage3-fast stage4-joint final; do
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

1. **`piano-fit-fast` after IR change:** The new IR changes what's "optimal" for piano knobs. The optimizer explores from defaults, not from the previous best in the old IR space. The fix is ensuring `--preset` carries forward the previous piano knobs as starting points.

2. **`piano-fit-ir` with `--optimize-joint`:** Joint optimization has 29 dimensions. With limited budget, it may not find solutions as good as the separated stages. Use longer budgets for joint runs.

3. **Different mix knob semantics:** Legacy (`ir_wet_mix`) vs dual-IR (`body_dry`, `room_wet`) use different mix logic. A preset switching between formats can produce different audio even with "equivalent" values.

## File Layout

```
out/stages/
├── stage1-fast.json                    # Piano knobs + default IR
├── stage1-fast.json.report.json        # Resume data for piano-fit-fast
├── stage2-body-ir-body.wav             # Mono body IR
├── stage2-body-ir-room.wav             # Stereo room IR
├── stage2-ir.json                      # Piano knobs + dual IR paths
├── stage2-ir.json.report.json          # Resume data for piano-fit-ir
├── stage3-fast.json                    # Refined piano knobs + dual IR
├── stage4-ir-body.wav                  # Refined body IR
├── stage4-ir-room.wav                  # Refined room IR
├── stage4-joint.json                   # Joint-optimized everything
└── final.json                          # Final polished preset
```

## Resuming Within a Stage

Both tools support `--resume` (default: true). To continue a run that was interrupted:

```bash
# Just re-run the same command — it reads the report and continues
go run --tags asm ./cmd/piano-fit-fast \
    --preset out/stages/stage1-fast.json \
    --output-preset out/stages/stage1-fast.json \
    --time-budget 600   # more time
    # --resume is true by default
```

The report file (`*.report.json`) stores the best knob values. On resume, these become the initial candidate, so the optimizer starts from the previous best rather than from scratch.

## Quick Smoke Test

To verify the full pipeline works end-to-end with minimal time:

```bash
# 30-second smoke test per stage
for stage_cmd in \
    "piano-fit-fast --preset assets/presets/default.json --output-preset out/smoke/s1.json" \
    "piano-fit-ir --preset out/smoke/s1.json --output-ir out/smoke/s2-ir.wav --output-preset out/smoke/s2.json --resume=false" \
    "piano-fit-fast --preset out/smoke/s2.json --output-preset out/smoke/s3.json --resume=false"
do
    go run --tags asm ./cmd/${stage_cmd} \
        --reference reference/c4.wav \
        --note 60 \
        --time-budget 30 \
        --max-evals 100 \
        --workers auto
done
```
