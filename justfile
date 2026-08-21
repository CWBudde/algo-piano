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
ci: check-formatted test lint check-tidy

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

fix:
    just lint-fix
    just fmt
