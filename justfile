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

# Fast inner-loop fitting (preset/model params) against C4 reference
fit-c4-fast reference="reference/c4.wav" preset="assets/presets/default.json" output_preset="assets/presets/fitted-c4.json" time_budget="120" max_evals="10000" mayfly_variant="desma" mayfly_pop="10" mayfly_round_evals="240" report_every="10" checkpoint_every="1" resume="true" write_best_candidate="" seed="1" decay_dbfs="-90" decay_hold_blocks="6" min_duration="2.0" max_duration="30" note="60" sample_rate="48000" resume_report="":
    #!/usr/bin/env bash
    set -euo pipefail
    reference_raw="{{reference}}"
    preset_raw="{{preset}}"
    output_raw="{{output_preset}}"
    budget_raw="{{time_budget}}"
    max_evals_raw="{{max_evals}}"
    mayfly_variant_raw="{{mayfly_variant}}"
    mayfly_pop_raw="{{mayfly_pop}}"
    mayfly_round_evals_raw="{{mayfly_round_evals}}"
    report_every_raw="{{report_every}}"
    checkpoint_every_raw="{{checkpoint_every}}"
    resume_raw="{{resume}}"
    write_best_candidate_raw="{{write_best_candidate}}"
    seed_raw="{{seed}}"
    decay_dbfs_raw="{{decay_dbfs}}"
    decay_hold_blocks_raw="{{decay_hold_blocks}}"
    min_duration_raw="{{min_duration}}"
    max_duration_raw="{{max_duration}}"
    note_raw="{{note}}"
    sample_rate_raw="{{sample_rate}}"
    resume_report_raw="{{resume_report}}"
    reference="${reference_raw#reference=}"
    preset="${preset_raw#preset=}"
    output_preset="${output_raw#output_preset=}"
    time_budget="${budget_raw#time_budget=}"
    max_evals="${max_evals_raw#max_evals=}"
    mayfly_variant="${mayfly_variant_raw#mayfly_variant=}"
    mayfly_pop="${mayfly_pop_raw#mayfly_pop=}"
    mayfly_round_evals="${mayfly_round_evals_raw#mayfly_round_evals=}"
    report_every="${report_every_raw#report_every=}"
    checkpoint_every="${checkpoint_every_raw#checkpoint_every=}"
    resume="${resume_raw#resume=}"
    write_best_candidate="${write_best_candidate_raw#write_best_candidate=}"
    seed="${seed_raw#seed=}"
    decay_dbfs="${decay_dbfs_raw#decay_dbfs=}"
    decay_hold_blocks="${decay_hold_blocks_raw#decay_hold_blocks=}"
    min_duration="${min_duration_raw#min_duration=}"
    max_duration="${max_duration_raw#max_duration=}"
    note="${note_raw#note=}"
    sample_rate="${sample_rate_raw#sample_rate=}"
    resume_report="${resume_report_raw#resume_report=}"
    extra_write_best=()
    extra_resume_report=()
    if [ -n "$write_best_candidate" ]; then
        extra_write_best=(--write-best-candidate "$write_best_candidate")
    fi
    if [ -n "$resume_report" ]; then
        extra_resume_report=(--resume-report "$resume_report")
    fi
    GOCACHE="${GOCACHE:-/tmp/gocache}" go run ./cmd/piano-fit-fast \
        --reference "$reference" \
        --preset "$preset" \
        --output-preset "$output_preset" \
        --time-budget "$time_budget" \
        --max-evals "$max_evals" \
        --note "$note" \
        --sample-rate "$sample_rate" \
        --decay-dbfs "$decay_dbfs" \
        --decay-hold-blocks "$decay_hold_blocks" \
        --min-duration "$min_duration" \
        --max-duration "$max_duration" \
        --report-every "$report_every" \
        --checkpoint-every "$checkpoint_every" \
        --seed "$seed" \
        --resume="$resume" \
        --mayfly-variant "$mayfly_variant" \
        --mayfly-pop "$mayfly_pop" \
        --mayfly-round-evals "$mayfly_round_evals" \
        "${extra_resume_report[@]}" \
        "${extra_write_best[@]}"

# Slow outer-loop fitting for IR synthesis parameters against C4 reference
fit-c4-ir reference="reference/c4.wav" preset="assets/presets/default.json" output_ir="assets/ir/fitted/c4-best.wav" output_preset="assets/presets/fitted-c4-ir.json" work_dir="out/ir-fit" time_budget="300" max_evals="10000" mayfly_variant="desma" mayfly_pop="10" mayfly_round_evals="240" report_every="10" checkpoint_every="1" resume="true" seed="1" decay_dbfs="-90" decay_hold_blocks="6" min_duration="2.0" max_duration="30" note="60" sample_rate="48000" velocity="118" release_after="3.5" top_k="5" optimize_ir_mix="false" optimize_joint="false" resume_report="":
    #!/usr/bin/env bash
    set -euo pipefail
    reference_raw="{{reference}}"
    preset_raw="{{preset}}"
    output_ir_raw="{{output_ir}}"
    output_preset_raw="{{output_preset}}"
    work_dir_raw="{{work_dir}}"
    budget_raw="{{time_budget}}"
    max_evals_raw="{{max_evals}}"
    mayfly_variant_raw="{{mayfly_variant}}"
    mayfly_pop_raw="{{mayfly_pop}}"
    mayfly_round_evals_raw="{{mayfly_round_evals}}"
    report_every_raw="{{report_every}}"
    checkpoint_every_raw="{{checkpoint_every}}"
    resume_raw="{{resume}}"
    seed_raw="{{seed}}"
    decay_dbfs_raw="{{decay_dbfs}}"
    decay_hold_blocks_raw="{{decay_hold_blocks}}"
    min_duration_raw="{{min_duration}}"
    max_duration_raw="{{max_duration}}"
    note_raw="{{note}}"
    sample_rate_raw="{{sample_rate}}"
    velocity_raw="{{velocity}}"
    release_after_raw="{{release_after}}"
    top_k_raw="{{top_k}}"
    optimize_ir_mix_raw="{{optimize_ir_mix}}"
    optimize_joint_raw="{{optimize_joint}}"
    resume_report_raw="{{resume_report}}"
    reference="${reference_raw#reference=}"
    preset="${preset_raw#preset=}"
    output_ir="${output_ir_raw#output_ir=}"
    output_preset="${output_preset_raw#output_preset=}"
    work_dir="${work_dir_raw#work_dir=}"
    time_budget="${budget_raw#time_budget=}"
    max_evals="${max_evals_raw#max_evals=}"
    mayfly_variant="${mayfly_variant_raw#mayfly_variant=}"
    mayfly_pop="${mayfly_pop_raw#mayfly_pop=}"
    mayfly_round_evals="${mayfly_round_evals_raw#mayfly_round_evals=}"
    report_every="${report_every_raw#report_every=}"
    checkpoint_every="${checkpoint_every_raw#checkpoint_every=}"
    resume="${resume_raw#resume=}"
    seed="${seed_raw#seed=}"
    decay_dbfs="${decay_dbfs_raw#decay_dbfs=}"
    decay_hold_blocks="${decay_hold_blocks_raw#decay_hold_blocks=}"
    min_duration="${min_duration_raw#min_duration=}"
    max_duration="${max_duration_raw#max_duration=}"
    note="${note_raw#note=}"
    sample_rate="${sample_rate_raw#sample_rate=}"
    velocity="${velocity_raw#velocity=}"
    release_after="${release_after_raw#release_after=}"
    top_k="${top_k_raw#top_k=}"
    optimize_ir_mix="${optimize_ir_mix_raw#optimize_ir_mix=}"
    optimize_joint="${optimize_joint_raw#optimize_joint=}"
    resume_report="${resume_report_raw#resume_report=}"
    mkdir -p "$(dirname "$output_ir")"
    mkdir -p "$work_dir"
    extra_resume_report=()
    if [ -n "$resume_report" ]; then
        extra_resume_report=(--resume-report "$resume_report")
    fi
    GOCACHE="${GOCACHE:-/tmp/gocache}" go run ./cmd/piano-fit-ir \
        --reference "$reference" \
        --preset "$preset" \
        --output-ir "$output_ir" \
        --output-preset "$output_preset" \
        --work-dir "$work_dir" \
        --time-budget "$time_budget" \
        --max-evals "$max_evals" \
        --note "$note" \
        --velocity "$velocity" \
        --release-after "$release_after" \
        --sample-rate "$sample_rate" \
        --decay-dbfs "$decay_dbfs" \
        --decay-hold-blocks "$decay_hold_blocks" \
        --min-duration "$min_duration" \
        --max-duration "$max_duration" \
        --report-every "$report_every" \
        --checkpoint-every "$checkpoint_every" \
        --top-k "$top_k" \
        --seed "$seed" \
        --resume="$resume" \
        --optimize-ir-mix="$optimize_ir_mix" \
        --optimize-joint="$optimize_joint" \
        --mayfly-variant "$mayfly_variant" \
        --mayfly-pop "$mayfly_pop" \
        --mayfly-round-evals "$mayfly_round_evals" \
        "${extra_resume_report[@]}"
