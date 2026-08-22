set shell := ["bash", "-uc"]

export GOPRIVATE := "github.com/cwbudde"

# Default recipe - show available commands
default:
    @just --list

# Format all code using treefmt
fmt:
    treefmt --allow-missing-formatter

# Check if code is formatted correctly
check-formatted:
    treefmt --allow-missing-formatter --fail-on-change

# Run linters
lint:
    GOCACHE="${GOCACHE:-/tmp/gocache}" GOMODCACHE="${GOMODCACHE:-/tmp/gomodcache}" GOLANGCI_LINT_CACHE="${GOLANGCI_LINT_CACHE:-/tmp/golangci-lint-cache}" golangci-lint run --timeout=2m ./...

# Run linters with auto-fix
lint-fix:
    GOCACHE="${GOCACHE:-/tmp/gocache}" GOMODCACHE="${GOMODCACHE:-/tmp/gomodcache}" GOLANGCI_LINT_CACHE="${GOLANGCI_LINT_CACHE:-/tmp/golangci-lint-cache}" golangci-lint run --fix --timeout=2m ./...

# Ensure go.mod is tidy
check-tidy:
    go mod tidy
    git diff --exit-code go.mod go.sum

# Run all tests
test:
    go test -v ./...

# Run tests with race detector
test-race:
    go test -race ./...

# Run tests with coverage
test-coverage:
    go test -v -coverprofile=coverage.out ./...
    go tool cover -html=coverage.out -o coverage.html

# Run benchmarks
bench:
    go test -run=^$ -bench=. -benchmem ./...

# Run all checks (formatting, linting, tests, tidiness)
ci: check-formatted test lint check-tidy gate-c4

# Clean build artifacts
clean:
    rm -f coverage.out coverage.html

# Render one octave of WAV files with auto-stop at decay threshold
render-octave root="60" out_dir="out/octave" preset="assets/presets/default.json" sample_rate="48000" velocity="100":
    #!/usr/bin/env bash
    set -euo pipefail
    root_raw="{{root}}"
    out_dir_raw="{{out_dir}}"
    preset_raw="{{preset}}"
    sample_rate_raw="{{sample_rate}}"
    velocity_raw="{{velocity}}"
    root="${root_raw#root=}"
    out_dir="${out_dir_raw#out_dir=}"
    preset="${preset_raw#preset=}"
    sample_rate="${sample_rate_raw#sample_rate=}"
    velocity="${velocity_raw#velocity=}"
    start="$root"
    mkdir -p "$out_dir"
    end=$((start + 11))
    for note in $(seq "$start" "$end"); do
        out="$out_dir/note_$note.wav"
        echo "Rendering $out"
        go run ./cmd/piano-render \
            --preset "$preset" \
            --note "$note" \
            --velocity "$velocity" \
            --sample-rate "$sample_rate" \
            --decay-dbfs -90 \
            --decay-hold-blocks 6 \
            --min-duration 2.0 \
            --max-duration 60 \
            --release-after 2.0 \
            --output "$out"
    done

# Compare model C4 against reference/c4.wav
distance-c4 reference="reference/c4.wav" preset="assets/presets/default.json" output="out/C4.wav" velocity="118" release_after="3.5":
    #!/usr/bin/env bash
    set -euo pipefail
    reference_raw="{{reference}}"
    preset_raw="{{preset}}"
    output_raw="{{output}}"
    velocity_raw="{{velocity}}"
    release_after_raw="{{release_after}}"
    reference="${reference_raw#reference=}"
    preset="${preset_raw#preset=}"
    output="${output_raw#output=}"
    velocity="${velocity_raw#velocity=}"
    release_after="${release_after_raw#release_after=}"
    extra_write_candidate=()
    if [ -n "$output" ]; then
        mkdir -p "$(dirname "$output")"
        extra_write_candidate=(--write-candidate "$output")
    fi
    GOCACHE="${GOCACHE:-/tmp/gocache}" go run ./cmd/piano-distance \
        --reference "$reference" \
        --preset "$preset" \
        --note 60 \
        --velocity "$velocity" \
        --sample-rate 48000 \
        --decay-dbfs -90 \
        --decay-hold-blocks 6 \
        --min-duration 2.0 \
        --release-after "$release_after" \
        --max-duration 30 \
        "${extra_write_candidate[@]}"

# Skips (exit 0) when the reference is absent: reference/*.wav is gitignored by
# design, so a fresh clone has no reference and `just ci` must still be green.
# C4 regression gate - fail when a distance metric exceeds its calibrated cap
gate-c4 reference="reference/c4.wav" preset="assets/presets/fitted-c4-mayfly.json" thresholds="assets/thresholds/c4.json" velocity="118" release_after="3.5":
    #!/usr/bin/env bash
    set -euo pipefail
    # just passes recipe arguments positionally, so `name=value` arrives as a raw
    # string in whatever slot it landed in. Strip the `name=` prefix and route the
    # value to the parameter it names, so arguments may be given in any order.
    names=(reference preset thresholds velocity release_after)
    raw=("{{reference}}" "{{preset}}" "{{thresholds}}" "{{velocity}}" "{{release_after}}")
    defaults=("reference/c4.wav" "assets/presets/fitted-c4-mayfly.json" "assets/thresholds/c4.json" "118" "3.5")
    declare -A arg=()
    for i in "${!names[@]}"; do
        arg["${names[$i]}"]="${defaults[$i]}"
    done
    for i in "${!raw[@]}"; do
        key="${raw[$i]%%=*}"
        if [[ "${raw[$i]}" != *=* ]] || [[ " ${names[*]} " != *" ${key} "* ]]; then
            arg["${names[$i]}"]="${raw[$i]}"
        fi
    done
    for i in "${!raw[@]}"; do
        key="${raw[$i]%%=*}"
        if [[ "${raw[$i]}" == *=* ]] && [[ " ${names[*]} " == *" ${key} "* ]]; then
            arg["$key"]="${raw[$i]#*=}"
        fi
    done
    reference="${arg[reference]}"
    preset="${arg[preset]}"
    thresholds="${arg[thresholds]}"
    velocity="${arg[velocity]}"
    release_after="${arg[release_after]}"
    if [ ! -f "$reference" ]; then
        echo "gate: SKIP - reference \"$reference\" not found (reference WAVs are gitignored; supply one to enable the gate)"
        exit 0
    fi
    if [ ! -f "$thresholds" ]; then
        echo "gate: SKIP - thresholds \"$thresholds\" not found"
        exit 0
    fi
    # Build rather than `go run`: go run collapses any non-zero exit into 1, and
    # the gate's exit 2 is the whole point.
    bin="$(mktemp -d)/piano-distance"
    trap 'rm -rf "$(dirname "$bin")"' EXIT
    GOCACHE="${GOCACHE:-/tmp/gocache}" go build -o "$bin" ./cmd/piano-distance
    "$bin" \
        --reference "$reference" \
        --preset "$preset" \
        --thresholds "$thresholds" \
        --note 60 \
        --velocity "$velocity" \
        --sample-rate 48000 \
        --decay-dbfs -90 \
        --decay-hold-blocks 6 \
        --min-duration 2.0 \
        --release-after "$release_after" \
        --max-duration 30

# Deterministic sensitivity + Pareto sweep over the sustain-pass knobs.
#
# Answers "is the sustain pass's decay-vs-legacy trade-off a property of the
# model, or an artifact of one stochastic run?" by scanning the five knobs
# `--pass sustain` leaves active: a one-at-a-time star plus a Halton fill of the
# 5-D box, every point rendered at the SAME final settings `distance-c4` uses so
# the legacy-v1 column is directly comparable.
#
# Deliberately NOT part of `just ci`: the default 2048-point joint stage costs
# minutes. See docs/optimization-workflow.md.
sweep-sustain-c4 preset="out/passes/attack.json" reference="reference/c4.wav" out="out/sweep/sustain-note60.json" samples="9" joint_evals="2048" workers="auto":
    #!/usr/bin/env bash
    set -euo pipefail
    # just passes recipe arguments positionally, so `name=value` arrives as a raw
    # string in whatever slot it landed in. Strip the `name=` prefix and route the
    # value to the parameter it names, so arguments may be given in any order.
    names=(preset reference out samples joint_evals workers)
    raw=("{{preset}}" "{{reference}}" "{{out}}" "{{samples}}" "{{joint_evals}}" "{{workers}}")
    defaults=("out/passes/attack.json" "reference/c4.wav" "out/sweep/sustain-note60.json" "9" "2048" "auto")
    declare -A arg=()
    for i in "${!names[@]}"; do
        arg["${names[$i]}"]="${defaults[$i]}"
    done
    for i in "${!raw[@]}"; do
        key="${raw[$i]%%=*}"
        if [[ "${raw[$i]}" != *=* ]] || [[ " ${names[*]} " != *" ${key} "* ]]; then
            arg["${names[$i]}"]="${raw[$i]}"
        fi
    done
    for i in "${!raw[@]}"; do
        key="${raw[$i]%%=*}"
        if [[ "${raw[$i]}" == *=* ]] && [[ " ${names[*]} " == *" ${key} "* ]]; then
            arg["$key"]="${raw[$i]#*=}"
        fi
    done
    preset="${arg[preset]}"
    reference="${arg[reference]}"
    out="${arg[out]}"
    samples="${arg[samples]}"
    joint_evals="${arg[joint_evals]}"
    workers="${arg[workers]}"
    # reference/*.wav is gitignored by design, so a fresh clone has no reference
    # and this recipe must still exit clean when someone runs it.
    if [ ! -f "$reference" ]; then
        echo "sweep: SKIP - reference \"$reference\" not found (reference WAVs are gitignored; supply one to enable the sweep)"
        exit 0
    fi
    # The preset is not optional: without it the sweep would silently baseline
    # on something other than the attack pass, and every number below would be
    # measured against the wrong point.
    if [ ! -f "$preset" ]; then
        echo "sweep: ERROR - preset \"$preset\" not found." >&2
        echo "  Produce it first with: just passes" >&2
        echo "  (or point the recipe elsewhere: just sweep-sustain-c4 preset=<path>)" >&2
        exit 1
    fi
    mkdir -p "$(dirname "$out")"
    GOCACHE="${GOCACHE:-/tmp/gocache}" go run ./cmd/piano-fit \
        --sweep \
        --pass sustain \
        --preset "$preset" \
        --reference "$reference" \
        --sweep-out "$out" \
        --sweep-samples "$samples" \
        --sweep-joint-evals "$joint_evals" \
        --workers "$workers" \
        --note 60 \
        --velocity 118 \
        --release-after 3.5 \
        --sample-rate 48000 \
        --decay-dbfs -90 \
        --decay-hold-blocks 6 \
        --min-duration 2.0 \
        --max-duration 30

# Constrained sustain re-fit: `--pass sustain` with a legacy-v1 floor.
#
# `just sweep-sustain-c4` showed that a non-regressing region exists in the 5-D
# sustain box but covers only ~1% of the sampled points, which is why every
# unconstrained sustain run wandered out of it and regressed the comparable
# score. This recipe re-fits inside that region: the search still optimises
# decay-v1, but any candidate whose FULL-SIGNAL legacy-v1 score exceeds `floor`
# is rejected outright.
#
# `floor` must be the legacy-v1 score of `preset` on the CURRENT renderer -
# measure it with `just distance-c4 reference/c4.wav <preset> ""` first. A stale
# floor from an older renderer constrains nothing.
#
# `thresholds` constrains what the gate MEASURES, which the floor cannot. The
# two are complementary, not alternatives: the floor fences the comparable
# legacy-v1 SCORE, while the threshold file fences the RAW metrics `just gate-c4`
# checks - above all `spectral_rmse_db`, which legacy-v1 saturates (clamp01 pins
# its spectral component at 1.0 above analysis.NormSpectral = 30.0, and every
# preset in the repo measures 47.8-68.6 dB) and therefore cannot see at all.
# Pass thresholds="" to drop the raw fence and keep only the floor.
#
# The gate stays a separate post-hoc check too: run `just gate-c4` on the output.
#
# Deliberately NOT part of `just ci`: it costs minutes.
fit-sustain-constrained-c4 preset="out/passes/attack-sample17.json" reference="reference/c4.wav" output_preset="out/passes/sustain-constrained.json" floor="0.5183" thresholds="assets/thresholds/c4.json" time_budget="180" workers="auto" seed="1":
    #!/usr/bin/env bash
    set -euo pipefail
    # just passes recipe arguments positionally, so `name=value` arrives as a raw
    # string in whatever slot it landed in. Strip the `name=` prefix and route the
    # value to the parameter it names, so arguments may be given in any order.
    names=(preset reference output_preset floor thresholds time_budget workers seed)
    raw=("{{preset}}" "{{reference}}" "{{output_preset}}" "{{floor}}" "{{thresholds}}" "{{time_budget}}" "{{workers}}" "{{seed}}")
    defaults=("out/passes/attack-sample17.json" "reference/c4.wav" "out/passes/sustain-constrained.json" "0.5183" "assets/thresholds/c4.json" "180" "auto" "1")
    declare -A arg=()
    for i in "${!names[@]}"; do
        arg["${names[$i]}"]="${defaults[$i]}"
    done
    for i in "${!raw[@]}"; do
        key="${raw[$i]%%=*}"
        if [[ "${raw[$i]}" != *=* ]] || [[ " ${names[*]} " != *" ${key} "* ]]; then
            arg["${names[$i]}"]="${raw[$i]}"
        fi
    done
    for i in "${!raw[@]}"; do
        key="${raw[$i]%%=*}"
        if [[ "${raw[$i]}" == *=* ]] && [[ " ${names[*]} " == *" ${key} "* ]]; then
            arg["$key"]="${raw[$i]#*=}"
        fi
    done
    preset="${arg[preset]}"
    reference="${arg[reference]}"
    output_preset="${arg[output_preset]}"
    floor="${arg[floor]}"
    thresholds="${arg[thresholds]}"
    time_budget="${arg[time_budget]}"
    workers="${arg[workers]}"
    seed="${arg[seed]}"
    # reference/*.wav is gitignored by design, so a fresh clone has no reference
    # and this recipe must still exit clean when someone runs it.
    if [ ! -f "$reference" ]; then
        echo "fit: SKIP - reference \"$reference\" not found (reference WAVs are gitignored; supply one to enable the fit)"
        exit 0
    fi
    # The preset is not optional: without it the run would seed from something
    # other than the sweep's constrained best, and the floor would be measured
    # against the wrong point.
    if [ ! -f "$preset" ]; then
        echo "fit: preset \"$preset\" not found - it is sample #17 of \`just sweep-sustain-c4\`," >&2
        echo "  i.e. out/passes/attack.json with unison_detune_scale set to 1.75" >&2
        echo "  (or point the recipe elsewhere: just fit-sustain-constrained-c4 preset=<path>)" >&2
        exit 1
    fi
    extra_thresholds=()
    if [ -n "$thresholds" ]; then
        extra_thresholds=(--gate-thresholds "$thresholds")
    fi
    mkdir -p "$(dirname "$output_preset")"
    GOCACHE="${GOCACHE:-/tmp/gocache}" go run -tags asm ./cmd/piano-fit \
        --pass sustain \
        --preset "$preset" \
        --reference "$reference" \
        --output-preset "$output_preset" \
        --score-constraint "legacy-v1:$floor" \
        "${extra_thresholds[@]}" \
        --work-dir out/fit-sustain-constrained \
        --optimize piano,mix \
        --note 60 \
        --velocity 118 \
        --release-after 3.5 \
        --sample-rate 48000 \
        --time-budget "$time_budget" \
        --max-evals 5000 \
        --workers "$workers" \
        --seed "$seed" \
        --resume=false
    echo "=== Legacy-v1 distance of the constrained sustain pass ==="
    just distance-c4 "$reference" "$output_preset" ""
    echo "=== Gate ==="
    just gate-c4 "reference=$reference" "preset=$output_preset"

# Synthesize a stereo IR WAV for soundboard/body convolution
ir-synth output="assets/ir/synth_96k.wav" sample_rate="96000" duration="2.0" modes="128" seed="1":
    #!/usr/bin/env bash
    set -euo pipefail
    output_raw="{{output}}"
    sample_rate_raw="{{sample_rate}}"
    duration_raw="{{duration}}"
    modes_raw="{{modes}}"
    seed_raw="{{seed}}"
    output="${output_raw#output=}"
    sample_rate="${sample_rate_raw#sample_rate=}"
    duration="${duration_raw#duration=}"
    modes="${modes_raw#modes=}"
    seed="${seed_raw#seed=}"
    GOCACHE="${GOCACHE:-/tmp/gocache}" go run ./cmd/ir-synth \
        --output "$output" \
        --sample-rate "$sample_rate" \
        --duration "$duration" \
        --modes "$modes" \
        --seed "$seed"

# Fit C4 with the unified piano-fit tool (see docs/optimization-workflow.md)
fit-c4 reference="reference/c4.wav" preset="assets/presets/default.json" output_preset="assets/presets/fitted-c4.json" output_ir="" work_dir="out/fit" optimize="piano,mix" note="60" time_budget="300" max_evals="5000" workers="auto" resume="true" seed="1" sample_rate="48000" extra="":
    #!/usr/bin/env bash
    set -euo pipefail
    # just passes recipe arguments positionally, so `name=value` arrives as a raw
    # string in whatever slot it landed in. Strip the `name=` prefix and route the
    # value to the parameter it names, so arguments may be given in any order.
    names=(reference preset output_preset output_ir work_dir optimize note time_budget max_evals workers resume seed sample_rate extra)
    raw=("{{reference}}" "{{preset}}" "{{output_preset}}" "{{output_ir}}" "{{work_dir}}" "{{optimize}}" "{{note}}" "{{time_budget}}" "{{max_evals}}" "{{workers}}" "{{resume}}" "{{seed}}" "{{sample_rate}}" "{{extra}}")
    defaults=("reference/c4.wav" "assets/presets/default.json" "assets/presets/fitted-c4.json" "" "out/fit" "piano,mix" "60" "300" "5000" "auto" "true" "1" "48000" "")
    declare -A arg=()
    for i in "${!names[@]}"; do
        arg["${names[$i]}"]="${defaults[$i]}"
    done
    for i in "${!raw[@]}"; do
        key="${raw[$i]%%=*}"
        if [[ "${raw[$i]}" != *=* ]] || [[ " ${names[*]} " != *" ${key} "* ]]; then
            arg["${names[$i]}"]="${raw[$i]}"
        fi
    done
    for i in "${!raw[@]}"; do
        key="${raw[$i]%%=*}"
        if [[ "${raw[$i]}" == *=* ]] && [[ " ${names[*]} " == *" ${key} "* ]]; then
            arg["$key"]="${raw[$i]#*=}"
        fi
    done
    reference="${arg[reference]}"
    preset="${arg[preset]}"
    output_preset="${arg[output_preset]}"
    output_ir="${arg[output_ir]}"
    work_dir="${arg[work_dir]}"
    optimize="${arg[optimize]}"
    note="${arg[note]}"
    time_budget="${arg[time_budget]}"
    max_evals="${arg[max_evals]}"
    workers="${arg[workers]}"
    resume="${arg[resume]}"
    seed="${arg[seed]}"
    sample_rate="${arg[sample_rate]}"
    extra="${arg[extra]}"
    mkdir -p "$(dirname "$output_preset")"
    mkdir -p "$work_dir"
    # piano-fit hard-errors without --output-ir when the body-ir/room-ir groups are
    # active, and ignores it otherwise, so only pass it when set.
    extra_output_ir=()
    if [ -n "$output_ir" ]; then
        mkdir -p "$(dirname "$output_ir")"
        extra_output_ir=(--output-ir "$output_ir")
    fi
    extra_args=()
    if [ -n "$extra" ]; then
        read -r -a extra_args <<< "$extra"
    fi
    GOCACHE="${GOCACHE:-/tmp/gocache}" go run -tags asm ./cmd/piano-fit \
        --reference "$reference" \
        --preset "$preset" \
        --output-preset "$output_preset" \
        --work-dir "$work_dir" \
        --optimize "$optimize" \
        --note "$note" \
        --sample-rate "$sample_rate" \
        --time-budget "$time_budget" \
        --max-evals "$max_evals" \
        --workers "$workers" \
        --seed "$seed" \
        --resume="$resume" \
        "${extra_output_ir[@]}" \
        "${extra_args[@]}"

# Full 5-stage C4 fitting pipeline from docs/optimization-workflow.md
fit-c4-stages time_budget="600":
    #!/usr/bin/env bash
    set -euo pipefail
    time_budget_raw="{{time_budget}}"
    time_budget="${time_budget_raw#time_budget=}"
    out_dir="out/stages"
    mkdir -p "$out_dir"
    echo "=== Stage 1/5: piano,mix (default IR, no resonance) ==="
    just fit-c4 \
        "preset=assets/presets/default.json" \
        "output_preset=$out_dir/stage1.json" \
        "optimize=piano,mix" \
        "time_budget=$time_budget" \
        "max_evals=5000" \
        "extra=--no-resonance"
    echo "=== Stage 2/5: body-ir,mix (body IR, piano knobs fixed from stage 1) ==="
    just fit-c4 \
        "preset=$out_dir/stage1.json" \
        "output_preset=$out_dir/stage2.json" \
        "output_ir=$out_dir/stage2-ir.wav" \
        "optimize=body-ir,mix" \
        "time_budget=$time_budget" \
        "max_evals=2000" \
        "resume=false" \
        "extra=--no-resonance"
    echo "=== Stage 3/5: piano,mix (refine piano against the stage 2 body IR) ==="
    just fit-c4 \
        "preset=$out_dir/stage2.json" \
        "output_preset=$out_dir/stage3.json" \
        "optimize=piano,mix" \
        "time_budget=$time_budget" \
        "max_evals=5000" \
        "resume=false" \
        "extra=--no-resonance"
    # Stages 1-3 pass --no-resonance purely to make evaluation cheaper. That
    # override is not written into the stage presets, so dropping the flag here
    # is enough to bring resonance back for the joint and final stages.
    echo "=== Stage 4/5: piano,body-ir,room-ir,mix (joint refinement, resonance back on) ==="
    just fit-c4 \
        "preset=$out_dir/stage3.json" \
        "output_preset=$out_dir/stage4.json" \
        "output_ir=$out_dir/stage4-ir.wav" \
        "optimize=piano,body-ir,room-ir,mix" \
        "time_budget=$time_budget" \
        "max_evals=3000" \
        "resume=false"
    echo "=== Stage 5/5: piano,mix (final polish with dual IR) ==="
    just fit-c4 \
        "preset=$out_dir/stage4.json" \
        "output_preset=$out_dir/final.json" \
        "optimize=piano,mix" \
        "time_budget=$time_budget" \
        "max_evals=5000" \
        "resume=false"
    echo "Final preset: $out_dir/final.json"

# Each pass restricts which knobs may move AND scores with the weighting
# profile that describes the aspect being fitted, so the three runs cannot
# trade one aspect against another. Scores are NOT comparable across passes -
# each one is measured against a different profile - so the recipe ends with
# legacy-v1 distance reports, which are the numbers that stay comparable.
#
# The final artifact is attack -> inharmonicity. The sustain pass runs and is
# measured, but is deliberately NOT chained into it: it regresses the comparable
# score. Re-measured 2026-08-22 on the post-#14 renderer under the calibrated
# norms, which was the open question: it still regresses (0.5214 -> 0.5436), so
# the saturated norms were not the cause. See docs/optimization-workflow.md.

# Per-aspect C4 fitting passes (final artifact is attack -> inharmonicity)
fit-c4-passes time_budget="180" preset="assets/presets/fitted-c4-mayfly.json":
    #!/usr/bin/env bash
    set -euo pipefail
    time_budget_raw="{{time_budget}}"
    time_budget="${time_budget_raw#time_budget=}"
    preset_raw="{{preset}}"
    preset="${preset_raw#preset=}"
    out_dir="out/passes"
    mkdir -p "$out_dir"
    # The attack window ends well before the reference's early decay does, so
    # only onset material is scored.
    echo "=== Pass 1/3: attack (hammer + attack noise, profile attack-v1) ==="
    just fit-c4 \
        "preset=$preset" \
        "output_preset=$out_dir/attack.json" \
        "optimize=piano,mix" \
        "time_budget=$time_budget" \
        "resume=false" \
        "extra=--pass attack --pass-window 0:0.35"
    # No window: decay-v1 weights the segmented decay metric at 0.55, and its
    # late segment needs signal out to 5 s. Cutting the tail off would remove
    # the very thing the pass is fitting.
    echo "=== Pass 2/3: sustain (loss + damping, profile decay-v1) ==="
    just fit-c4 \
        "preset=$out_dir/attack.json" \
        "output_preset=$out_dir/sustain.json" \
        "optimize=piano,mix" \
        "time_budget=$time_budget" \
        "resume=false" \
        "extra=--pass sustain"
    # Chained from the ATTACK preset, not the sustain one: the sustain pass
    # regresses the comparable score (re-confirmed 2026-08-22 under the
    # calibrated norms, see docs/optimization-workflow.md), so it is measured
    # above and then left out of the line that produces the final artifact.
    echo "=== Pass 3/3: inharmonicity (dispersion + strike position, profile inharmonicity-v1) ==="
    just fit-c4 \
        "preset=$out_dir/attack.json" \
        "output_preset=$out_dir/inharmonicity.json" \
        "optimize=piano,mix" \
        "time_budget=$time_budget" \
        "resume=false" \
        "extra=--pass inharmonicity --pass-window 0.2:2.0"
    # distance-c4 routes its arguments positionally, so the reference has to be
    # named even though it is the default.
    #
    # Each piano-fit run above scores with its own per-aspect profile, so none of
    # those numbers are comparable with each other. Every pass output therefore
    # gets a legacy-v1 distance here, including the attack pass on its own: that
    # is the number the docs quote, and without this call it would not be
    # reproducible from this recipe.
    echo "=== Legacy-v1 distance of the attack pass ==="
    just distance-c4 "reference/c4.wav" "$out_dir/attack.json" ""
    echo "=== Legacy-v1 distance of the sustain pass (measured, NOT chained) ==="
    just distance-c4 "reference/c4.wav" "$out_dir/sustain.json" ""
    echo "=== Legacy-v1 distance of attack -> inharmonicity (the final artifact) ==="
    just distance-c4 "reference/c4.wav" "$out_dir/inharmonicity.json" "$out_dir/inharmonicity.wav"

fix:
    just lint-fix
    just fmt
