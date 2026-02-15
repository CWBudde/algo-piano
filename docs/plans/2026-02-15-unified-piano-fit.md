# Unified piano-fit Tool Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Merge `piano-fit-fast` and `piano-fit-ir` into a single `piano-fit` command with `--optimize` group selection.

**Architecture:** Single command with `--optimize=piano,mix` (default) selecting knob groups. When IR groups are active, IR is synthesized per evaluation; otherwise loaded from disk. Unified report format with `best_knobs` enables cross-mode resume.

**Tech Stack:** Go, mayfly optimizer, irsynth, piano engine

---

### Task 1: Create cmd/piano-fit/ with utility files

**Files:**

- Create: `cmd/piano-fit/utils.go`
- Create: `cmd/piano-fit/wav.go`

**Step 1: Create utils.go**

Copy from `cmd/piano-fit-ir/utils.go` (identical to fast version):

```go
package main

import (
	"fmt"
	"os"

	fitcommon "github.com/cwbudde/algo-piano/internal/fitcommon"
)

func clamp(v, lo, hi float64) float64 {
	return fitcommon.Clamp(v, lo, hi)
}

func minInt(a, b int) int {
	return fitcommon.MinInt(a, b)
}

func maxInt(a, b int) int {
	return fitcommon.MaxInt(a, b)
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
```

**Step 2: Create wav.go**

Merge from both tools (fast has `writeStereoWAV` with interleaved, ir has separate L/R + mono):

```go
package main

import fitcommon "github.com/cwbudde/algo-piano/internal/fitcommon"

func readWAVMono(path string) ([]float64, int, error) {
	return fitcommon.ReadWAVMono(path)
}

func resampleIfNeeded(in []float64, fromRate int, toRate int) ([]float64, error) {
	return fitcommon.ResampleIfNeeded(in, fromRate, toRate)
}

func writeStereoWAV(path string, left []float32, right []float32, sampleRate int) error {
	return fitcommon.WriteStereoWAVLR(path, left, right, sampleRate)
}

func writeMonoWAV(path string, data []float32, sampleRate int) error {
	return fitcommon.WriteStereoWAVLR(path, data, data, sampleRate)
}

func stereoToMono64(st []float32) []float64 {
	return fitcommon.StereoToMono64(st)
}

func stereoRMS(interleaved []float32) float64 {
	return fitcommon.StereoRMS(interleaved)
}
```

**Step 3: Verify compilation**

Run: `go build --tags asm ./cmd/piano-fit/...` (will fail until main.go exists, but confirms no syntax errors in these files)

---

### Task 2: Create knobs.go with group-aware initCandidate

This is the core new logic. The `initCandidate` function builds knob definitions based on which groups are active.

**Files:**

- Create: `cmd/piano-fit/knobs.go`
- Create: `cmd/piano-fit/knobs_test.go`

**Step 1: Write knobs_test.go with failing tests**

```go
package main

import (
	"testing"

	"github.com/cwbudde/algo-piano/piano"
)

func TestParseOptimizeGroups(t *testing.T) {
	tests := []struct {
		in      string
		want    map[string]bool
		wantErr bool
	}{
		{in: "piano,mix", want: map[string]bool{"piano": true, "mix": true}},
		{in: "body-ir,room-ir,mix", want: map[string]bool{"body-ir": true, "room-ir": true, "mix": true}},
		{in: "piano,body-ir,room-ir,mix", want: map[string]bool{"piano": true, "body-ir": true, "room-ir": true, "mix": true}},
		{in: "piano", want: map[string]bool{"piano": true}},
		{in: "body-ir", want: map[string]bool{"body-ir": true}},
		{in: "", wantErr: true},
		{in: "invalid", wantErr: true},
		{in: "piano,invalid", wantErr: true},
	}
	for _, tt := range tests {
		got, err := parseOptimizeGroups(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("parseOptimizeGroups(%q) expected error", tt.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("parseOptimizeGroups(%q) unexpected error: %v", tt.in, err)
		}
		for k := range tt.want {
			if !got[k] {
				t.Fatalf("parseOptimizeGroups(%q) missing group %q", tt.in, k)
			}
		}
		for k := range got {
			if !tt.want[k] {
				t.Fatalf("parseOptimizeGroups(%q) unexpected group %q", tt.in, k)
			}
		}
	}
}

func TestNeedsIRSynthesis(t *testing.T) {
	if needsIRSynthesis(map[string]bool{"piano": true, "mix": true}) {
		t.Fatal("piano,mix should not need IR synthesis")
	}
	if !needsIRSynthesis(map[string]bool{"body-ir": true, "mix": true}) {
		t.Fatal("body-ir,mix should need IR synthesis")
	}
	if !needsIRSynthesis(map[string]bool{"room-ir": true}) {
		t.Fatal("room-ir should need IR synthesis")
	}
	if !needsIRSynthesis(map[string]bool{"piano": true, "body-ir": true, "room-ir": true, "mix": true}) {
		t.Fatal("full joint should need IR synthesis")
	}
}

func TestInitCandidatePianoMixOnly(t *testing.T) {
	base := piano.NewDefaultParams()
	groups := map[string]bool{"piano": true, "mix": true}
	defs, cand := initCandidate(base, 48000, 60, 100, 2.0, groups)

	// piano: output_gain + 6 hammer/unison + 3 per_note + velocity + release_after = 12
	// mix (legacy, no dual-IR paths): ir_wet_mix + ir_dry_mix + ir_gain = 3
	// Total: 15
	// But with dual-IR paths set, mix would be 4 knobs instead of 3.
	// Default params have no dual-IR paths, so legacy mix = 3.
	wantLen := 15
	if len(defs) != wantLen {
		t.Fatalf("piano,mix defs len = %d, want %d", len(defs), wantLen)
	}
	if len(cand.Vals) != len(defs) {
		t.Fatalf("vals len = %d, want %d", len(cand.Vals), len(defs))
	}
	// Should have piano knobs
	knobNames := knobNameSet(defs)
	if !knobNames["output_gain"] {
		t.Fatal("missing output_gain")
	}
	if !knobNames["hammer_stiffness_scale"] {
		t.Fatal("missing hammer_stiffness_scale")
	}
	// Should not have IR synthesis knobs
	if knobNames["body_modes"] {
		t.Fatal("unexpected body_modes in piano-only mode")
	}
}

func TestInitCandidateBodyIRMix(t *testing.T) {
	base := piano.NewDefaultParams()
	groups := map[string]bool{"body-ir": true, "mix": true}
	defs, cand := initCandidate(base, 48000, 60, 100, 2.0, groups)

	// body-ir: 6 knobs
	// mix (dual-IR mode since body-ir is active): body_dry + body_gain + room_wet + room_gain = 4
	// Total: 10
	wantLen := 10
	if len(defs) != wantLen {
		t.Fatalf("body-ir,mix defs len = %d, want %d", len(defs), wantLen)
	}
	if len(cand.Vals) != len(defs) {
		t.Fatalf("vals len = %d, want %d", len(cand.Vals), len(defs))
	}
	knobNames := knobNameSet(defs)
	if !knobNames["body_modes"] {
		t.Fatal("missing body_modes")
	}
	if !knobNames["body_dry"] {
		t.Fatal("missing body_dry (dual-IR mix)")
	}
	if knobNames["ir_wet_mix"] {
		t.Fatal("unexpected legacy ir_wet_mix in IR mode")
	}
	if knobNames["output_gain"] {
		t.Fatal("unexpected output_gain without piano group")
	}
}

func TestInitCandidateFullJoint(t *testing.T) {
	base := piano.NewDefaultParams()
	groups := map[string]bool{"piano": true, "body-ir": true, "room-ir": true, "mix": true}
	defs, cand := initCandidate(base, 48000, 60, 100, 2.0, groups)

	// piano: 12, body-ir: 6, room-ir: 7, mix (dual): 4 = 29
	wantLen := 29
	if len(defs) != wantLen {
		t.Fatalf("full joint defs len = %d, want %d", len(defs), wantLen)
	}
	if len(cand.Vals) != len(defs) {
		t.Fatalf("vals len = %d, want %d", len(cand.Vals), len(defs))
	}
}

func TestInitCandidateDualIRMixWithPresetPaths(t *testing.T) {
	base := piano.NewDefaultParams()
	base.BodyIRWavPath = "body.wav"
	base.RoomIRWavPath = "room.wav"
	groups := map[string]bool{"piano": true, "mix": true}
	defs, _ := initCandidate(base, 48000, 60, 100, 2.0, groups)

	// piano: 12, mix (dual-IR because preset has paths): 4 = 16
	wantLen := 16
	if len(defs) != wantLen {
		t.Fatalf("piano+dual-IR-mix defs len = %d, want %d", len(defs), wantLen)
	}
	knobNames := knobNameSet(defs)
	if !knobNames["body_dry"] {
		t.Fatal("missing body_dry")
	}
	if knobNames["ir_wet_mix"] {
		t.Fatal("unexpected ir_wet_mix with dual-IR preset")
	}
}

func knobNameSet(defs []knobDef) map[string]bool {
	m := make(map[string]bool, len(defs))
	for _, d := range defs {
		m[d.Name] = true
	}
	return m
}
```

**Step 2: Write knobs.go**

```go
package main

import (
	"fmt"
	"math"
	"strings"

	"github.com/cwbudde/algo-piano/irsynth"
	"github.com/cwbudde/algo-piano/piano"
)

type knobDef struct {
	Name  string
	Min   float64
	Max   float64
	IsInt bool
}

type candidate struct {
	Vals []float64
}

var validGroups = map[string]bool{
	"piano":   true,
	"body-ir": true,
	"room-ir": true,
	"mix":     true,
}

func parseOptimizeGroups(raw string) (map[string]bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("empty optimize groups")
	}
	groups := map[string]bool{}
	for _, g := range strings.Split(raw, ",") {
		g = strings.TrimSpace(g)
		if !validGroups[g] {
			return nil, fmt.Errorf("unknown optimize group %q (valid: piano, body-ir, room-ir, mix)", g)
		}
		groups[g] = true
	}
	return groups, nil
}

func needsIRSynthesis(groups map[string]bool) bool {
	return groups["body-ir"] || groups["room-ir"]
}

func initCandidate(
	base *piano.Params,
	sampleRate int,
	note int,
	baseVelocity int,
	baseReleaseAfter float64,
	groups map[string]bool,
) ([]knobDef, candidate) {
	defs := make([]knobDef, 0, 32)
	vals := make([]float64, 0, 32)
	addKnob := func(def knobDef, val float64) {
		for _, d := range defs {
			if d.Name == def.Name {
				return
			}
		}
		defs = append(defs, def)
		vals = append(vals, val)
	}

	// Determine if we're in dual-IR mode.
	// Dual-IR if: IR groups are active OR preset already has dual-IR paths.
	dualIR := needsIRSynthesis(groups) || base.BodyIRWavPath != "" || base.RoomIRWavPath != ""

	if groups["piano"] {
		np := base.PerNote[note]
		if np == nil {
			np = &piano.NoteParams{Loss: 0.9990, Inharmonicity: 0.12, StrikePosition: 0.18}
		}
		addKnob(knobDef{Name: "output_gain", Min: 0.4, Max: 1.8}, float64(base.OutputGain))
		addKnob(knobDef{Name: "hammer_stiffness_scale", Min: 0.6, Max: 1.8}, float64(base.HammerStiffnessScale))
		addKnob(knobDef{Name: "hammer_exponent_scale", Min: 0.8, Max: 1.2}, float64(base.HammerExponentScale))
		addKnob(knobDef{Name: "hammer_damping_scale", Min: 0.6, Max: 1.8}, float64(base.HammerDampingScale))
		addKnob(knobDef{Name: "hammer_initial_velocity_scale", Min: 0.7, Max: 1.4}, float64(base.HammerInitialVelocityScale))
		addKnob(knobDef{Name: "hammer_contact_time_scale", Min: 0.7, Max: 1.6}, float64(base.HammerContactTimeScale))
		addKnob(knobDef{Name: "unison_detune_scale", Min: 0.0, Max: 2.0}, float64(base.UnisonDetuneScale))
		addKnob(knobDef{Name: "unison_crossfeed", Min: 0.0, Max: 0.005}, float64(base.UnisonCrossfeed))
		addKnob(knobDef{Name: fmt.Sprintf("per_note.%d.loss", note), Min: 0.985, Max: 0.99995}, float64(np.Loss))
		addKnob(knobDef{Name: fmt.Sprintf("per_note.%d.inharmonicity", note), Min: 0.0, Max: 0.6}, float64(np.Inharmonicity))
		addKnob(knobDef{Name: fmt.Sprintf("per_note.%d.strike_position", note), Min: 0.08, Max: 0.45}, float64(np.StrikePosition))
		addKnob(knobDef{Name: "render.velocity", Min: 40, Max: 127, IsInt: true}, float64(baseVelocity))
		addKnob(knobDef{Name: "render.release_after", Min: 0.2, Max: 3.5}, baseReleaseAfter)
	}

	if groups["body-ir"] {
		bodyCfg := irsynth.DefaultBodyConfig()
		bodyCfg.SampleRate = sampleRate
		addKnob(knobDef{Name: "body_modes", Min: 8, Max: 96, IsInt: true}, float64(bodyCfg.Modes))
		addKnob(knobDef{Name: "body_brightness", Min: 0.5, Max: 2.5}, bodyCfg.Brightness)
		addKnob(knobDef{Name: "body_density", Min: 0.5, Max: 4.0}, bodyCfg.Density)
		addKnob(knobDef{Name: "body_direct", Min: 0.1, Max: 1.2}, bodyCfg.DirectLevel)
		addKnob(knobDef{Name: "body_decay", Min: 0.01, Max: 0.5}, bodyCfg.DecayS)
		addKnob(knobDef{Name: "body_duration", Min: 0.02, Max: 0.3}, bodyCfg.DurationS)
	}

	if groups["room-ir"] {
		roomCfg := irsynth.DefaultRoomConfig()
		roomCfg.SampleRate = sampleRate
		addKnob(knobDef{Name: "room_early", Min: 0, Max: 64, IsInt: true}, float64(roomCfg.EarlyCount))
		addKnob(knobDef{Name: "room_late", Min: 0.0, Max: 0.15}, roomCfg.LateLevel)
		addKnob(knobDef{Name: "room_stereo_width", Min: 0.0, Max: 1.0}, roomCfg.StereoWidth)
		addKnob(knobDef{Name: "room_brightness", Min: 0.3, Max: 2.0}, roomCfg.Brightness)
		addKnob(knobDef{Name: "room_low_decay", Min: 0.3, Max: 3.0}, roomCfg.LowDecayS)
		addKnob(knobDef{Name: "room_high_decay", Min: 0.05, Max: 0.8}, roomCfg.HighDecayS)
		addKnob(knobDef{Name: "room_duration", Min: 0.3, Max: 2.0}, roomCfg.DurationS)
	}

	if groups["mix"] {
		if dualIR {
			addKnob(knobDef{Name: "body_dry", Min: 0.2, Max: 1.5}, float64(base.BodyDryMix))
			addKnob(knobDef{Name: "body_gain", Min: 0.3, Max: 2.0}, float64(base.BodyIRGain))
			addKnob(knobDef{Name: "room_wet", Min: 0.0, Max: 1.0}, float64(base.RoomWetMix))
			addKnob(knobDef{Name: "room_gain", Min: 0.3, Max: 2.0}, float64(base.RoomGain))
		} else {
			addKnob(knobDef{Name: "ir_wet_mix", Min: 0.2, Max: 1.6}, float64(base.IRWetMix))
			addKnob(knobDef{Name: "ir_dry_mix", Min: 0.0, Max: 0.8}, float64(base.IRDryMix))
			addKnob(knobDef{Name: "ir_gain", Min: 0.4, Max: 2.2}, float64(base.IRGain))
		}
	}

	for i := range vals {
		vals[i] = clamp(vals[i], defs[i].Min, defs[i].Max)
		if defs[i].IsInt {
			vals[i] = math.Round(vals[i])
		}
	}
	return defs, candidate{Vals: vals}
}

type irConfigs struct {
	body irsynth.BodyConfig
	room irsynth.RoomConfig
}

func applyCandidate(
	base *piano.Params,
	sampleRate int,
	note int,
	baseVelocity int,
	baseReleaseAfter float64,
	defs []knobDef,
	c candidate,
) (irConfigs, *piano.Params, int, float64) {
	bodyCfg := irsynth.DefaultBodyConfig()
	bodyCfg.SampleRate = sampleRate
	roomCfg := irsynth.DefaultRoomConfig()
	roomCfg.SampleRate = sampleRate
	params := cloneParams(base)
	if params.PerNote == nil {
		params.PerNote = make(map[int]*piano.NoteParams)
	}
	np := params.PerNote[note]
	if np == nil {
		np = &piano.NoteParams{}
		params.PerNote[note] = np
	}
	velocity := baseVelocity
	releaseAfter := baseReleaseAfter

	for i, def := range defs {
		v := c.Vals[i]
		switch def.Name {
		// Piano knobs.
		case "output_gain":
			params.OutputGain = float32(v)
		case "hammer_stiffness_scale":
			params.HammerStiffnessScale = float32(v)
		case "hammer_exponent_scale":
			params.HammerExponentScale = float32(v)
		case "hammer_damping_scale":
			params.HammerDampingScale = float32(v)
		case "hammer_initial_velocity_scale":
			params.HammerInitialVelocityScale = float32(v)
		case "hammer_contact_time_scale":
			params.HammerContactTimeScale = float32(v)
		case "unison_detune_scale":
			params.UnisonDetuneScale = float32(v)
		case "unison_crossfeed":
			params.UnisonCrossfeed = float32(v)
		case fmt.Sprintf("per_note.%d.loss", note):
			np.Loss = float32(v)
		case fmt.Sprintf("per_note.%d.inharmonicity", note):
			np.Inharmonicity = float32(v)
		case fmt.Sprintf("per_note.%d.strike_position", note):
			np.StrikePosition = float32(v)
		case "render.velocity":
			velocity = int(math.Round(v))
		case "render.release_after":
			releaseAfter = v
		// Body IR knobs.
		case "body_modes":
			bodyCfg.Modes = int(math.Round(v))
		case "body_brightness":
			bodyCfg.Brightness = v
		case "body_density":
			bodyCfg.Density = v
		case "body_direct":
			bodyCfg.DirectLevel = v
		case "body_decay":
			bodyCfg.DecayS = v
		case "body_duration":
			bodyCfg.DurationS = v
		// Room IR knobs.
		case "room_early":
			roomCfg.EarlyCount = int(math.Round(v))
		case "room_late":
			roomCfg.LateLevel = v
		case "room_stereo_width":
			roomCfg.StereoWidth = v
		case "room_brightness":
			roomCfg.Brightness = v
		case "room_low_decay":
			roomCfg.LowDecayS = v
		case "room_high_decay":
			roomCfg.HighDecayS = v
		case "room_duration":
			roomCfg.DurationS = v
		// Mix knobs (dual-IR).
		case "body_dry":
			params.BodyDryMix = float32(v)
		case "body_gain":
			params.BodyIRGain = float32(v)
		case "room_wet":
			params.RoomWetMix = float32(v)
		case "room_gain":
			params.RoomGain = float32(v)
		// Mix knobs (legacy).
		case "ir_wet_mix":
			params.IRWetMix = float32(v)
		case "ir_dry_mix":
			params.IRDryMix = float32(v)
		case "ir_gain":
			params.IRGain = float32(v)
		}
	}

	if bodyCfg.Modes < 1 {
		bodyCfg.Modes = 1
	}
	if roomCfg.EarlyCount < 0 {
		roomCfg.EarlyCount = 0
	}
	if velocity < 1 {
		velocity = 1
	}
	if velocity > 127 {
		velocity = 127
	}
	if releaseAfter < 0.05 {
		releaseAfter = 0.05
	}
	return irConfigs{body: bodyCfg, room: roomCfg}, params, velocity, releaseAfter
}

func fromNormalized(pos []float64, defs []knobDef) candidate {
	vals := make([]float64, len(defs))
	for i := range defs {
		x := 0.0
		if i < len(pos) {
			x = clamp(pos[i], 0, 1)
		}
		v := defs[i].Min + x*(defs[i].Max-defs[i].Min)
		if defs[i].IsInt {
			v = math.Round(v)
		}
		vals[i] = v
	}
	return candidate{Vals: vals}
}
```

**Step 3: Run tests**

Run: `go test --tags asm ./cmd/piano-fit/ -run TestParseOptimizeGroups -v`
Run: `go test --tags asm ./cmd/piano-fit/ -run TestNeedsIRSynthesis -v`
Run: `go test --tags asm ./cmd/piano-fit/ -run TestInitCandidate -v`
Expected: All PASS

**Step 4: Commit**

```bash
git add cmd/piano-fit/knobs.go cmd/piano-fit/knobs_test.go cmd/piano-fit/utils.go cmd/piano-fit/wav.go
git commit -m "feat(piano-fit): add knobs.go with group-aware initCandidate/applyCandidate"
```

---

### Task 3: Create optimize.go (unified optimization loop)

Based on `piano-fit-ir/optimize.go` (the superset), with conditional IR synthesis.

**Files:**

- Create: `cmd/piano-fit/optimize.go`

**Step 1: Write optimize.go**

Port from `cmd/piano-fit-ir/optimize.go` with these changes:

1. `optimizationConfig` gains `groups map[string]bool` field.
2. `evaluateCandidate` checks `needsIRSynthesis(cfg.groups)`:
   - If true: generate body/room IR via irsynth, clear IR paths, use `renderCandidateWithDualIR`.
   - If false: use `renderCandidateFromParams` (loads IR from disk via NewPiano).
3. All helper functions (`cloneCandidate`, `cloneOptimizationEval`, `newMayflyConfig`, `runMayfly`, `renderPiano`, `renderCandidateWithDualIR`, `renderCandidateFromParams`, `cloneParams`, `reserveEval`, `currentBestScore`, `cloneTopCandidates`, `candidateFromTop`, `candidateKey`, `updateTopCandidates`) are included.

Key change in `evaluateCandidate`:

```go
func evaluateCandidate(cfg *optimizationConfig, cand candidate, _ string, settings evalSettings) (optimizationEval, error) {
	irCfgs, params, evalVelocity, evalReleaseAfter := applyCandidate(
		cfg.baseParams, settings.sampleRate, cfg.note,
		cfg.baseVelocity, cfg.baseReleaseAfter, cfg.defs, cand,
	)

	var mono []float64
	var bodyIR, roomIRL, roomIRR []float32

	if needsIRSynthesis(cfg.groups) {
		var err error
		bodyIR, err = irsynth.GenerateBody(irCfgs.body)
		if err != nil {
			return optimizationEval{}, fmt.Errorf("body IR: %w", err)
		}
		roomL, roomR, err := irsynth.GenerateRoom(irCfgs.room)
		if err != nil {
			return optimizationEval{}, fmt.Errorf("room IR: %w", err)
		}
		roomIRL, roomIRR = roomL, roomR
		params.IRWavPath = ""
		params.BodyIRWavPath = ""
		params.RoomIRWavPath = ""
		mono, _, err = renderCandidateWithDualIR(
			params, bodyIR, roomIRL, roomIRR,
			cfg.note, evalVelocity, settings.sampleRate,
			settings.decayDBFS, settings.decayHoldBlocks,
			settings.minDuration, settings.maxDuration,
			settings.renderBlockSize, evalReleaseAfter,
		)
		if err != nil {
			return optimizationEval{}, err
		}
	} else {
		var err error
		mono, _, err = renderCandidateFromParams(
			params, cfg.note, evalVelocity, settings.sampleRate,
			settings.decayDBFS, settings.decayHoldBlocks,
			settings.minDuration, settings.maxDuration,
			settings.renderBlockSize, evalReleaseAfter,
		)
		if err != nil {
			return optimizationEval{}, err
		}
	}

	return optimizationEval{
		metrics:      analysis.Compare(settings.reference, mono, settings.sampleRate),
		params:       params,
		bodyIR:       bodyIR,
		roomIRL:      roomIRL,
		roomIRR:      roomIRR,
		velocity:     evalVelocity,
		releaseAfter: evalReleaseAfter,
	}, nil
}
```

Note: `renderCandidateFromParams` is ported from `piano-fit-fast/main.go` but now takes `blockSize` parameter (use the same `renderPiano` helper after creating the Piano instance).

**Step 2: Verify compilation**

Run: `go build --tags asm ./cmd/piano-fit/...` (may still need main.go stub)

---

### Task 4: Create output.go (unified output writing)

**Files:**

- Create: `cmd/piano-fit/output.go`

**Step 1: Write output.go**

Merge from both tools. Key: when IR synthesis was active, write body/room WAVs and set paths in preset. When not, preserve existing IR paths.

Port from `cmd/piano-fit-ir/output.go` with this change: only write IR WAVs when `outputIR != ""`:

```go
func writeOutputs(..., outputIR string, ...) error {
	if outputIR != "" && len(bestBodyIR) > 0 {
		// Write body and room IR WAVs, set paths in preset.
		ext := filepath.Ext(outputIR)
		base := strings.TrimSuffix(outputIR, ext)
		bodyIRPath := base + "-body" + ext
		roomIRPath := base + "-room" + ext
		if err := writeMonoWAV(bodyIRPath, bestBodyIR, sampleRate); err != nil {
			return err
		}
		if err := writeStereoWAV(roomIRPath, bestRoomIRL, bestRoomIRR, sampleRate); err != nil {
			return err
		}
		p.BodyIRWavPath = presetIRPath(outputPreset, bodyIRPath)
		p.RoomIRWavPath = presetIRPath(outputPreset, roomIRPath)
		p.IRWavPath = ""
	}
	// Otherwise preserve existing IR paths from preset.
	...
}
```

The `writePresetJSON` should include all fields (legacy + dual-IR) from `piano-fit-fast/output.go` since that version already handles both.

Report uses unified `best_knobs` key (not `best_ir_knobs`).

**Step 2: Verify compilation**

---

### Task 5: Create main.go (unified CLI)

**Files:**

- Create: `cmd/piano-fit/main.go`

**Step 1: Write main.go**

Merge flag parsing from both tools. Add `--optimize` flag. Validate that `--output-ir` is set when IR groups are active.

Key validation:

```go
groups, err := parseOptimizeGroups(*optimize)
if err != nil {
	die("invalid --optimize: %v", err)
}
if needsIRSynthesis(groups) && *outputIR == "" {
	die("--output-ir is required when optimizing body-ir or room-ir groups")
}
```

`loadCandidateFromReport` reads `best_knobs` (unified key). Also checks `best_ir_knobs` for backwards compatibility with old reports.

**Step 2: Build and verify**

Run: `go build --tags asm ./cmd/piano-fit`
Expected: Compiles successfully

**Step 3: Commit**

```bash
git add cmd/piano-fit/
git commit -m "feat(piano-fit): unified optimization tool with --optimize group selection"
```

---

### Task 6: Port and merge tests

**Files:**

- Create: `cmd/piano-fit/main_test.go`
- Create: `cmd/piano-fit/optimize_test.go`

**Step 1: Write main_test.go**

Port tests from both old tools. Key tests:

From `piano-fit-fast/main_test.go`:

- `TestPresetIRPathRelativizesFromPresetDir`
- `TestPresetIRPathEmpty`
- `TestParseWorkersFlag`

From `piano-fit-ir/main_test.go`:

- Adapt to use `groups` parameter in `initCandidate`

New tests for unified behavior:

- `TestInitCandidateLegacyMixKnobs` (piano,mix with no dual-IR paths)
- `TestInitCandidateDualIRMixKnobs` (piano,mix with dual-IR paths in preset)
- `TestApplyCandidateDualIRMix`
- `TestApplyCandidatePianoKnobs`

**Step 2: Write optimize_test.go**

Port `TestNewMayflyConfig` from both (identical).

**Step 3: Run all tests**

Run: `go test --tags asm ./cmd/piano-fit/... -v`
Expected: All PASS

**Step 4: Commit**

```bash
git add cmd/piano-fit/*_test.go
git commit -m "test(piano-fit): port and merge tests from piano-fit-fast and piano-fit-ir"
```

---

### Task 7: Full build + test verification

**Step 1: Build all commands**

Run: `go build --tags asm ./...`
Expected: Success (old commands still compile too at this point)

**Step 2: Run all tests**

Run: `go test --tags asm ./...`
Expected: All PASS

---

### Task 8: Delete old commands

**Files:**

- Delete: `cmd/piano-fit-fast/` (entire directory)
- Delete: `cmd/piano-fit-ir/` (entire directory)

**Step 1: Remove directories**

```bash
rm -rf cmd/piano-fit-fast/ cmd/piano-fit-ir/
```

**Step 2: Verify build**

Run: `go build --tags asm ./...`
Expected: Success

**Step 3: Run remaining tests**

Run: `go test --tags asm ./...`
Expected: All PASS

**Step 4: Commit**

```bash
git add -A
git commit -m "refactor: remove piano-fit-fast and piano-fit-ir (replaced by unified piano-fit)"
```

---

### Task 9: Update documentation

**Files:**

- Modify: `docs/optimization-workflow.md`

**Step 1: Update workflow doc**

Replace all `piano-fit-fast` and `piano-fit-ir` references with `piano-fit --optimize=...`. Mark all gaps as resolved. Update CLI examples.

**Step 2: Commit**

```bash
git add docs/
git commit -m "docs: update optimization workflow for unified piano-fit tool"
```
