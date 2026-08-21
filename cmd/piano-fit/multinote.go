package main

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// aggregate modes for combining per-note scores into a single objective value.
const (
	aggregateMean    = "mean"
	aggregateMax     = "max"
	aggregateMeanMax = "mean-max"
)

// meanMaxBlend is the weight given to the (max-mean) spread in "mean-max".
const meanMaxBlend = 0.25

// noteTarget is one note to fit: the MIDI note, its reference WAV path, that
// reference resampled for the optimization loop and for the final pass, and the
// weight it carries in the aggregate objective.
type noteTarget struct {
	note          int
	referencePath string
	optRef        []float64
	finalRef      []float64
	weight        float64
}

// parseNotesFlag parses a comma-separated MIDI note list. An empty string falls
// back to the single note given by --note.
func parseNotesFlag(raw string, fallback int) ([]int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []int{fallback}, nil
	}
	notes := make([]int, 0, 4)
	seen := make(map[int]bool)
	for _, field := range strings.Split(raw, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		n, err := strconv.Atoi(field)
		if err != nil {
			return nil, fmt.Errorf("invalid MIDI note %q", field)
		}
		if n < 0 || n > 127 {
			return nil, fmt.Errorf("MIDI note %d out of range [0,127]", n)
		}
		if seen[n] {
			return nil, fmt.Errorf("duplicate note %d", n)
		}
		seen[n] = true
		notes = append(notes, n)
	}
	if len(notes) == 0 {
		return nil, fmt.Errorf("no notes specified")
	}
	return notes, nil
}

// parseNoteReferenceMap parses "48=reference/c3.wav,60=reference/c4.wav".
func parseNoteReferenceMap(raw string) (map[int]string, error) {
	out := make(map[int]string)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return out, nil
	}
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			return nil, fmt.Errorf("invalid reference-map entry %q (want note=path)", entry)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		note, err := strconv.Atoi(key)
		if err != nil {
			return nil, fmt.Errorf("invalid MIDI note %q in reference-map", key)
		}
		if value == "" {
			return nil, fmt.Errorf("empty reference path for note %d", note)
		}
		if _, dup := out[note]; dup {
			return nil, fmt.Errorf("duplicate reference-map entry for note %d", note)
		}
		out[note] = value
	}
	return out, nil
}

// parseNoteWeights parses "48=1,60=2". An empty string yields an empty map,
// which resolveNoteWeights turns into uniform weights.
func parseNoteWeights(raw string) (map[int]float64, error) {
	out := make(map[int]float64)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return out, nil
	}
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			return nil, fmt.Errorf("invalid note-weights entry %q (want note=weight)", entry)
		}
		note, err := strconv.Atoi(strings.TrimSpace(key))
		if err != nil {
			return nil, fmt.Errorf("invalid MIDI note %q in note-weights", key)
		}
		w, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return nil, fmt.Errorf("invalid weight %q for note %d", value, note)
		}
		if w <= 0 || math.IsNaN(w) || math.IsInf(w, 0) {
			return nil, fmt.Errorf("weight for note %d must be a finite positive number", note)
		}
		if _, dup := out[note]; dup {
			return nil, fmt.Errorf("duplicate note-weights entry for note %d", note)
		}
		out[note] = w
	}
	return out, nil
}

// resolveReferences maps every note to a reference WAV path. The explicit
// reference map wins; a single note with no mapping falls back to
// --reference. Notes left without a reference produce an error listing them.
func resolveReferences(notes []int, refMap map[int]string, singleReference string) ([]string, error) {
	paths := make([]string, len(notes))
	var missing []int
	for i, n := range notes {
		if p, ok := refMap[n]; ok {
			paths[i] = p
			continue
		}
		if len(notes) == 1 && singleReference != "" {
			paths[i] = singleReference
			continue
		}
		missing = append(missing, n)
	}
	if len(missing) > 0 {
		parts := make([]string, len(missing))
		for i, n := range missing {
			parts[i] = strconv.Itoa(n)
		}
		return nil, fmt.Errorf("no reference for note(s) %s: pass --reference-map \"%s=path.wav\"", strings.Join(parts, ","), parts[0])
	}
	return paths, nil
}

// resolveNoteWeights returns the per-note weights in note order, defaulting to
// 1.0 for notes absent from the map.
func resolveNoteWeights(notes []int, weights map[int]float64) []float64 {
	out := make([]float64, len(notes))
	for i, n := range notes {
		if w, ok := weights[n]; ok {
			out[i] = w
			continue
		}
		out[i] = 1.0
	}
	return out
}

// parseAggregate validates the --aggregate flag.
func parseAggregate(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", aggregateMean:
		return aggregateMean, nil
	case aggregateMax:
		return aggregateMax, nil
	case aggregateMeanMax:
		return aggregateMeanMax, nil
	default:
		return "", fmt.Errorf("unknown aggregate %q (valid: mean, max, mean-max)", raw)
	}
}

// loadNoteTargets reads and resamples every reference twice: once for the
// optimization sample rate and once for the final sample rate, mirroring what
// the single-note path has always done.
func loadNoteTargets(notes []int, paths []string, weights []float64, optSampleRate, finalSampleRate int) ([]noteTarget, error) {
	targets := make([]noteTarget, 0, len(notes))
	for i, n := range notes {
		raw, sr, err := readWAVMono(paths[i])
		if err != nil {
			return nil, fmt.Errorf("failed to read reference for note %d (%s): %w", n, paths[i], err)
		}
		optRef, err := resampleIfNeeded(raw, sr, optSampleRate)
		if err != nil {
			return nil, fmt.Errorf("failed to resample optimization reference for note %d: %w", n, err)
		}
		finalRef, err := resampleIfNeeded(raw, sr, finalSampleRate)
		if err != nil {
			return nil, fmt.Errorf("failed to resample full reference for note %d: %w", n, err)
		}
		targets = append(targets, noteTarget{
			note:          n,
			referencePath: paths[i],
			optRef:        optRef,
			finalRef:      finalRef,
			weight:        weights[i],
		})
	}
	return targets, nil
}

// aggregateScores combines per-note scores into the single value the optimizer
// minimises.
//
// "mean" (the default) is a weighted arithmetic mean. Every per-note score is
// already in [0,1], so the mean stays well-scaled and stays smooth in every
// knob. "max" is non-smooth: the objective becomes piecewise-defined by
// whichever note is currently worst, so improvements elsewhere give the
// optimizer zero signal. "mean-max" blends the two.
//
// For a single note the result is that note's score EXACTLY, under all three
// modes, so single-note runs are unaffected by this code path.
func aggregateScores(reports []noteReport, weights []float64, mode string) float64 {
	if len(reports) == 0 {
		return 1.0
	}
	if len(reports) == 1 {
		return reports[0].Score
	}

	sumW := 0.0
	sum := 0.0
	maxScore := math.Inf(-1)
	for i, r := range reports {
		w := 1.0
		if i < len(weights) && weights[i] > 0 {
			w = weights[i]
		}
		sumW += w
		sum += w * r.Score
		if r.Score > maxScore {
			maxScore = r.Score
		}
	}
	mean := 1.0
	if sumW > 0 {
		mean = sum / sumW
	}

	switch mode {
	case aggregateMax:
		return maxScore
	case aggregateMeanMax:
		return mean + meanMaxBlend*(maxScore-mean)
	default: // aggregateMean
		return mean
	}
}

// targetWeights extracts the weight vector from a target list.
func targetWeights(targets []noteTarget) []float64 {
	out := make([]float64, len(targets))
	for i := range targets {
		out[i] = targets[i].weight
	}
	return out
}

// targetNotes extracts the MIDI note list from a target list.
func targetNotes(targets []noteTarget) []int {
	out := make([]int, len(targets))
	for i := range targets {
		out[i] = targets[i].note
	}
	return out
}

// rmsOf returns the root-mean-square level of a signal.
func rmsOf(x []float64) float64 {
	if len(x) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range x {
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(x)))
}

// gainPair is one reference/candidate rendering used for analytic output-gain
// matching.
type gainPair struct {
	reference []float64
	candidate []float64
	weight    float64
}

// matchOutputGainRatio solves for the scalar that brings the candidate
// rendering(s) onto the reference level, in closed form.
//
// analysis.Compare RMS-normalises both signals before computing any metric and
// piano.OutputGain is a plain output multiplier, so the ratio is provably
// score-invariant: it cannot be found by search, only computed. With several
// notes the per-note ratios are combined as a weighted arithmetic mean.
func matchOutputGainRatio(pairs []gainPair) float64 {
	sum := 0.0
	sumW := 0.0
	for _, p := range pairs {
		candRMS := rmsOf(p.candidate)
		refRMS := rmsOf(p.reference)
		if candRMS <= 0 || refRMS <= 0 {
			continue
		}
		ratio := refRMS / candRMS
		if math.IsNaN(ratio) || math.IsInf(ratio, 0) {
			continue
		}
		w := p.weight
		if w <= 0 {
			w = 1.0
		}
		sum += w * ratio
		sumW += w
	}
	if sumW <= 0 {
		return 1.0
	}
	return sum / sumW
}

// sortedNotes returns a sorted copy, used only for stable log output.
func sortedNotes(notes []int) []int {
	out := append([]int(nil), notes...)
	sort.Ints(out)
	return out
}
