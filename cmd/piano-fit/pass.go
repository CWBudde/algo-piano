package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/cwbudde/algo-piano/analysis"
)

// Pass names.
const (
	passNone           = "none"
	passAttack         = "attack"
	passSustain        = "sustain"
	passInharmonicity  = "inharmonicity"
	perNoteWildcardSep = "*"
)

// windowSpec restricts a comparison to a time window. The zero value means the
// whole signal.
type windowSpec struct {
	StartSec float64 `json:"start_sec"`
	EndSec   float64 `json:"end_sec"`
}

func (w windowSpec) isZero() bool {
	return w.StartSec == 0 && w.EndSec == 0
}

// slice cuts x down to the window. Out-of-range bounds are clamped; an empty
// or inverted window returns the untouched signal so a mis-specified window
// degrades to a full compare rather than to a zero-length one.
func (w windowSpec) slice(x []float64, sampleRate int) []float64 {
	if w.isZero() || sampleRate <= 0 || len(x) == 0 {
		return x
	}
	startIdx := 0
	if w.StartSec > 0 {
		startIdx = int(math.Round(w.StartSec * float64(sampleRate)))
	}
	endIdx := len(x)
	if w.EndSec > 0 {
		endIdx = int(math.Round(w.EndSec * float64(sampleRate)))
	}
	if startIdx < 0 {
		startIdx = 0
	}
	if endIdx > len(x) {
		endIdx = len(x)
	}
	if startIdx >= endIdx {
		return x
	}
	return x[startIdx:endIdx]
}

// parseWindow parses "start:end" in seconds. An empty string is the zero
// window (whole signal); an empty side means "from the start" / "to the end".
func parseWindow(raw string) (windowSpec, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return windowSpec{}, nil
	}
	startRaw, endRaw, ok := strings.Cut(raw, ":")
	if !ok {
		return windowSpec{}, fmt.Errorf("invalid window %q (want start:end in seconds)", raw)
	}
	var w windowSpec
	var err error
	if s := strings.TrimSpace(startRaw); s != "" {
		if w.StartSec, err = strconv.ParseFloat(s, 64); err != nil {
			return windowSpec{}, fmt.Errorf("invalid window start %q", s)
		}
	}
	if s := strings.TrimSpace(endRaw); s != "" {
		if w.EndSec, err = strconv.ParseFloat(s, 64); err != nil {
			return windowSpec{}, fmt.Errorf("invalid window end %q", s)
		}
	}
	if w.StartSec < 0 || w.EndSec < 0 {
		return windowSpec{}, fmt.Errorf("window bounds must be >= 0")
	}
	if w.EndSec > 0 && w.EndSec <= w.StartSec {
		return windowSpec{}, fmt.Errorf("window end (%g) must be greater than start (%g)", w.EndSec, w.StartSec)
	}
	return w, nil
}

// passSpec restricts a run to one aspect of the sound: a knob allowlist, a
// metric emphasis, and optionally a time window.
//
// A pass is orthogonal to --optimize: groups are additive knob sets, a pass is
// a restriction applied on top of whatever the groups produced.
type passSpec struct {
	Name   string
	Allow  []string
	Window windowSpec
}

// isNone reports whether the pass leaves the run unrestricted.
func (s passSpec) isNone() bool {
	return s.Name == "" || s.Name == passNone
}

// parsePass resolves a pass name into its knob allowlist.
func parsePass(raw string) (passSpec, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", passNone:
		return passSpec{Name: passNone}, nil
	case passAttack:
		return passSpec{Name: passAttack, Allow: []string{
			"hammer_stiffness_scale",
			"hammer_exponent_scale",
			"hammer_damping_scale",
			"hammer_contact_time_scale",
			"hammer_initial_velocity_scale",
			"attack_noise_level",
			"attack_noise_duration_ms",
			"attack_noise_color",
			"render.velocity",
		}}, nil
	case passSustain:
		return passSpec{Name: passSustain, Allow: []string{
			"per_note.*.loss",
			"high_freq_damping",
			"unison_detune_scale",
			"unison_crossfeed",
			"render.release_after",
		}}, nil
	case passInharmonicity:
		return passSpec{Name: passInharmonicity, Allow: []string{
			"per_note.*.inharmonicity",
			"per_note.*.strike_position",
			"unison_detune_scale",
		}}, nil
	default:
		return passSpec{}, fmt.Errorf("unknown pass %q (valid: none, attack, sustain, inharmonicity)", raw)
	}
}

// knobAllowed reports whether name matches the allowlist. Patterns containing
// "*" match any single dot-delimited segment, so "per_note.*.loss" matches
// "per_note.60.loss" for every note.
func knobAllowed(name string, allow []string) bool {
	nameParts := strings.Split(name, ".")
	for _, pattern := range allow {
		if pattern == name {
			return true
		}
		if !strings.Contains(pattern, perNoteWildcardSep) {
			continue
		}
		patternParts := strings.Split(pattern, ".")
		if len(patternParts) != len(nameParts) {
			continue
		}
		match := true
		for i := range patternParts {
			if patternParts[i] == perNoteWildcardSep {
				continue
			}
			if patternParts[i] != nameParts[i] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// filterKnobsForPass narrows defs (and the matching candidate values) to the
// knobs the pass is allowed to move. It is a POST-FILTER on whatever
// initCandidate produced, so initCandidate itself stays untouched and the defs
// order (the Mayfly dimension mapping) is preserved for the surviving knobs.
//
// Filtered-out knobs need no extra handling: applyCandidate only writes fields
// that appear in defs, so everything else simply keeps its value from the input
// preset. That is exactly the desired "freeze everything else" semantics.
func filterKnobsForPass(defs []knobDef, c candidate, spec passSpec) ([]knobDef, candidate) {
	if spec.isNone() || len(spec.Allow) == 0 {
		return defs, c
	}
	outDefs := make([]knobDef, 0, len(defs))
	outVals := make([]float64, 0, len(defs))
	for i, d := range defs {
		if !knobAllowed(d.Name, spec.Allow) {
			continue
		}
		outDefs = append(outDefs, d)
		if i < len(c.Vals) {
			outVals = append(outVals, c.Vals[i])
		} else {
			outVals = append(outVals, d.Min)
		}
	}
	return outDefs, candidate{Vals: outVals}
}

// passScorer returns the metric used while a pass is active, or nil for an
// unrestricted run (which leaves optimizationConfig.score on analysis.Compare).
//
// This closure is the single seam for the metric emphasis of a pass. Swapping
// analysis.Compare for analysis.CompareWithWeights and a named profile is a
// one-line change here.
//
// NOTE: a windowed compare slices BOTH signals first, so analysis.Compare
// re-runs trimLeadingSilence, normalizeRMS and lag estimation inside the
// window. Windowed scores are therefore NOT comparable to full-signal scores.
func passScorer(spec passSpec) scorer {
	if spec.isNone() {
		return nil
	}
	window := spec.Window
	return func(reference, cand []float64, sampleRate int) analysis.Metrics {
		return analysis.Compare(
			window.slice(reference, sampleRate),
			window.slice(cand, sampleRate),
			sampleRate,
		)
	}
}

// seedRenderControls reads render.velocity and render.release_after out of a
// candidate and returns them as the base values applyCandidate should fall back
// to.
//
// This matters for passes: render.velocity and render.release_after are not
// preset fields, they come from --velocity/--release-after. When a pass filters
// them out, the CLI defaults would otherwise silently replace the values a
// previous run fitted. Seeding them from the resumed candidate keeps the
// fitted values frozen instead.
func seedRenderControls(defs []knobDef, c candidate, velocity int, releaseAfter float64) (int, float64) {
	for i, d := range defs {
		if i >= len(c.Vals) {
			break
		}
		switch d.Name {
		case "render.velocity":
			velocity = int(math.Round(c.Vals[i]))
		case "render.release_after":
			releaseAfter = c.Vals[i]
		}
	}
	return velocity, releaseAfter
}
