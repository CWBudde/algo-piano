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
distance-c4 reference="reference/c4.wav" preset="assets/presets/default.json":
    #!/usr/bin/env bash
    set -euo pipefail
    reference_raw="{{reference}}"
    preset_raw="{{preset}}"
    reference="${reference_raw#reference=}"
    preset="${preset_raw#preset=}"
    GOCACHE="${GOCACHE:-/tmp/gocache}" go run ./cmd/piano-distance \
        --reference "$reference" \
        --preset "$preset" \
        --note 60 \
        --sample-rate 48000 \
        --decay-dbfs -90 \
        --decay-hold-blocks 6 \
        --min-duration 2.0 \
        --release-after 2.0 \
        --max-duration 30
