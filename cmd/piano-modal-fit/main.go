package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cwbudde/algo-piano/analysis"
	"github.com/cwbudde/algo-piano/piano"
	"github.com/cwbudde/algo-piano/preset"
	"github.com/cwbudde/mayfly"
)

type knobSet struct {
	ModalPartials     int     `json:"modal_partials"`
	ModalGainExponent float64 `json:"modal_gain_exponent"`
	ModalExcitation   float64 `json:"modal_excitation"`
	ModalUndampedLoss float64 `json:"modal_undamped_loss"`
	ModalDampedLoss   float64 `json:"modal_damped_loss"`
}

type noteCalibration struct {
	Note          int              `json:"note"`
	Full          analysis.Metrics `json:"full"`
	Attack        analysis.Metrics `json:"attack"`
	EarlySustain  analysis.Metrics `json:"early_sustain"`
	Decay         analysis.Metrics `json:"decay"`
	WindowedScore float64          `json:"windowed_score"`
	CombinedScore float64          `json:"combined_score"`
}

type calibrationReport struct {
	ProfileVersion string            `json:"profile_version"`
	TimestampUTC   string            `json:"timestamp_utc"`
	BasePreset     string            `json:"base_preset"`
	OutputPreset   string            `json:"output_preset"`
	SampleRate     int               `json:"sample_rate"`
	Velocity       int               `json:"velocity"`
	ReleaseAfter   float64           `json:"release_after_seconds"`
	Notes          []int             `json:"notes"`
	Evaluations    int               `json:"evaluations"`
	BestScore      float64           `json:"best_score"`
	BestKnobs      knobSet           `json:"best_knobs"`
	PerNote        []noteCalibration `json:"per_note"`
	ElapsedSec     float64           `json:"elapsed_seconds"`
}

type renderSettings struct {
	note           int
	velocity       int
	sampleRate     int
	decayDBFS      float64
	decayHold      int
	minDurationSec float64
	maxDurationSec float64
	blockSize      int
	releaseAfter   float64
}

type windowSpec struct {
	name   string
	startS float64
	endS   float64
	weight float64
}

var matchWindows = []windowSpec{
	{name: "attack", startS: 0.0, endS: 0.06, weight: 0.45},
	{name: "early_sustain", startS: 0.06, endS: 0.45, weight: 0.30},
	{name: "decay", startS: 0.45, endS: 2.4, weight: 0.25},
}

const modalKnobDims = 5

func main() {
	basePreset := flag.String("preset", "assets/presets/default.json", "DWG reference preset JSON path")
	outputPreset := flag.String("output-preset", "assets/presets/modal-calibrated.json", "Path to write calibrated modal preset JSON")
	reportPath := flag.String("report", "", "Optional report JSON path (default: <output-preset>.report.json)")
	notesRaw := flag.String("notes", "36,48,60,72,84", "Comma-separated MIDI notes to match")
	velocity := flag.Int("velocity", 118, "Velocity used for calibration renders")
	releaseAfter := flag.Float64("release-after", 3.2, "Seconds before NoteOff during calibration renders")
	sampleRate := flag.Int("sample-rate", 48000, "Render/analysis sample rate")
	decayDBFS := flag.Float64("decay-dbfs", -90.0, "Auto-stop threshold in dBFS")
	decayHoldBlocks := flag.Int("decay-hold-blocks", 6, "Consecutive below-threshold blocks required to stop")
	minDuration := flag.Float64("min-duration", 2.0, "Minimum render duration in seconds")
	maxDuration := flag.Float64("max-duration", 14.0, "Maximum render duration in seconds")
	blockSize := flag.Int("render-block-size", 128, "Render block size")
	iters := flag.Int("iters", 120, "Evaluation budget for Mayfly objective before local refinement")
	mayflyVariant := flag.String("mayfly-variant", "desma", "Mayfly variant: ma|desma|olce|eobbma|gsasma|mpma|aoblmoa")
	mayflyPop := flag.Int("mayfly-pop", 10, "Male/female population size per Mayfly run")
	seed := flag.Int64("seed", 1, "Random seed")
	flag.Parse()

	if *sampleRate < 8000 {
		die("sample-rate must be >= 8000")
	}
	if *velocity < 1 || *velocity > 127 {
		die("velocity must be in [1,127]")
	}
	if *releaseAfter < 0.05 {
		die("release-after must be >= 0.05")
	}
	if *iters < 1 {
		die("iters must be >= 1")
	}
	if *mayflyPop < 2 {
		*mayflyPop = 2
	}
	if *decayHoldBlocks < 1 {
		*decayHoldBlocks = 1
	}
	if *minDuration <= 0 {
		die("min-duration must be > 0")
	}
	if *maxDuration < *minDuration {
		die("max-duration must be >= min-duration")
	}
	if *blockSize < 16 {
		*blockSize = 16
	}

	notes, err := parseNotes(*notesRaw)
	if err != nil {
		die("notes: %v", err)
	}

	base, err := preset.LoadJSON(*basePreset)
	if err != nil {
		die("load preset: %v", err)
	}

	rs := renderSettings{
		velocity:       *velocity,
		sampleRate:     *sampleRate,
		decayDBFS:      *decayDBFS,
		decayHold:      *decayHoldBlocks,
		minDurationSec: *minDuration,
		maxDurationSec: *maxDuration,
		blockSize:      *blockSize,
		releaseAfter:   *releaseAfter,
	}

	start := time.Now()

	// Build DWG references once.
	fmt.Printf("Rendering DWG references for notes: %v\n", notes)
	refParams := cloneParams(base)
	refParams.StringModel = piano.StringModelDWG
	references := make(map[int][]float64, len(notes))
	for _, n := range notes {
		rs.note = n
		mono, err := renderNote(refParams, rs)
		if err != nil {
			die("render DWG reference note %d: %v", n, err)
		}
		references[n] = mono
	}

	rng := rand.New(rand.NewSource(*seed))
	best := initialKnobs(base)
	bestScore, _, err := evaluateKnobs(base, best, notes, references, rs)
	if err != nil {
		die("initial evaluation failed: %v", err)
	}
	evals := 1
	fmt.Printf("Initial score=%.4f knobs=%+v\n", bestScore, best)

	variant := strings.ToLower(strings.TrimSpace(*mayflyVariant))
	mayflyBudget := *iters
	if mayflyBudget < *mayflyPop*2 {
		mayflyBudget = *mayflyPop * 2
	}
	mayflyIters := maxInt(1, mayflyBudget/(2*(*mayflyPop)))
	progressEvery := maxInt(20, 2*(*mayflyPop))
	objectiveCalls := 0
	expensiveEvals := 0
	cfg, err := newMayflyConfig(variant, *mayflyPop, modalKnobDims, mayflyIters)
	if err != nil {
		die("mayfly setup failed: %v", err)
	}
	cfg.Rand = rng
	cfg.ObjectiveFunc = func(pos []float64) float64 {
		objectiveCalls++
		if expensiveEvals >= mayflyBudget {
			return bestScore + 0.25
		}
		expensiveEvals++
		cand := knobsFromNormalized(pos)
		score, _, evalErr := evaluateKnobs(base, cand, notes, references, rs)
		if evalErr != nil || !isFiniteFloat(score) {
			if expensiveEvals%progressEvery == 0 {
				fmt.Printf("Progress eval=%d/%d score=%.4f\n", expensiveEvals, mayflyBudget, bestScore)
			}
			return 10.0
		}
		if score < bestScore {
			best = cand
			bestScore = score
			fmt.Printf("Improved eval=%d/%d score=%.4f knobs=%+v\n", expensiveEvals, mayflyBudget, bestScore, best)
		} else if expensiveEvals%progressEvery == 0 {
			fmt.Printf("Progress eval=%d/%d score=%.4f\n", expensiveEvals, mayflyBudget, bestScore)
		}
		return score
	}
	res, err := runMayfly(cfg)
	if err != nil {
		die("mayfly optimization failed: %v", err)
	}
	if res != nil && res.FuncEvalCount > objectiveCalls {
		objectiveCalls = res.FuncEvalCount
	}
	evals += expensiveEvals
	fmt.Printf("Mayfly done variant=%s pop=%d iterations=%d evals=%d objective-calls=%d best=%.4f\n", variant, *mayflyPop, mayflyIters, expensiveEvals, objectiveCalls, bestScore)

	// Lightweight coordinate refinement.
	best, bestScore, refinedEvals := refineLocally(base, best, bestScore, notes, references, rs)
	evals += refinedEvals

	// Final per-note metrics for report.
	_, perNote, err := evaluateKnobs(base, best, notes, references, rs)
	if err != nil {
		die("final evaluation failed: %v", err)
	}

	outParams := cloneParams(base)
	applyModalKnobs(outParams, best)
	outParams.StringModel = piano.StringModelModal

	if err := writePreset(*outputPreset, outParams); err != nil {
		die("write output preset: %v", err)
	}

	if *reportPath == "" {
		*reportPath = *outputPreset + ".report.json"
	}
	report := calibrationReport{
		ProfileVersion: "modal-calibration-v1",
		TimestampUTC:   time.Now().UTC().Format(time.RFC3339),
		BasePreset:     *basePreset,
		OutputPreset:   *outputPreset,
		SampleRate:     *sampleRate,
		Velocity:       *velocity,
		ReleaseAfter:   *releaseAfter,
		Notes:          notes,
		Evaluations:    evals,
		BestScore:      bestScore,
		BestKnobs:      best,
		PerNote:        perNote,
		ElapsedSec:     time.Since(start).Seconds(),
	}
	if err := writeJSON(*reportPath, report); err != nil {
		die("write report: %v", err)
	}

	fmt.Printf("Done evals=%d score=%.4f output=%s report=%s\n", evals, bestScore, *outputPreset, *reportPath)
}

func evaluateKnobs(base *piano.Params, knobs knobSet, notes []int, refs map[int][]float64, rs renderSettings) (float64, []noteCalibration, error) {
	params := cloneParams(base)
	applyModalKnobs(params, knobs)
	params.StringModel = piano.StringModelModal

	total := 0.0
	perNote := make([]noteCalibration, 0, len(notes))
	for _, note := range notes {
		ref := refs[note]
		if len(ref) == 0 {
			return 0, nil, fmt.Errorf("missing reference for note %d", note)
		}

		rs.note = note
		cand, err := renderNote(params, rs)
		if err != nil {
			return 0, nil, fmt.Errorf("render modal note %d: %w", note, err)
		}

		full := sanitizeMetrics(analysis.Compare(ref, cand, rs.sampleRate))
		attack := compareWindow(ref, cand, rs.sampleRate, matchWindows[0])
		early := compareWindow(ref, cand, rs.sampleRate, matchWindows[1])
		decay := compareWindow(ref, cand, rs.sampleRate, matchWindows[2])
		windowed := weightedScore(
			[]analysis.Metrics{attack, early, decay},
			[]float64{matchWindows[0].weight, matchWindows[1].weight, matchWindows[2].weight},
		)
		combined := 0.65*windowed + 0.35*full.Score
		if !isFiniteFloat(combined) {
			combined = 1.0
		}

		total += combined
		perNote = append(perNote, noteCalibration{
			Note:          note,
			Full:          full,
			Attack:        attack,
			EarlySustain:  early,
			Decay:         decay,
			WindowedScore: windowed,
			CombinedScore: combined,
		})
	}
	if len(notes) == 0 {
		return 0, perNote, nil
	}
	score := total / float64(len(notes))
	if !isFiniteFloat(score) {
		score = 1.0
	}
	return score, perNote, nil
}

func compareWindow(ref []float64, cand []float64, sampleRate int, w windowSpec) analysis.Metrics {
	start := int(w.startS * float64(sampleRate))
	end := int(w.endS * float64(sampleRate))
	if start < 0 {
		start = 0
	}
	n := minInt(len(ref), len(cand))
	if end > n {
		end = n
	}
	if start >= end || end-start < 256 {
		return analysis.Metrics{
			SampleRate:      sampleRate,
			ReferenceFrames: maxInt(0, end-start),
			CandidateFrames: maxInt(0, end-start),
			AlignedFrames:   0,
			Score:           1.0,
			Similarity:      0.0,
		}
	}
	return sanitizeMetrics(analysis.Compare(ref[start:end], cand[start:end], sampleRate))
}

func weightedScore(metrics []analysis.Metrics, weights []float64) float64 {
	totalW := 0.0
	total := 0.0
	n := minInt(len(metrics), len(weights))
	for i := 0; i < n; i++ {
		w := weights[i]
		if w <= 0 {
			continue
		}
		m := sanitizeMetrics(metrics[i])
		totalW += w
		total += m.Score * w
	}
	if totalW <= 0 {
		return 1.0
	}
	out := total / totalW
	if !isFiniteFloat(out) {
		return 1.0
	}
	return out
}

func refineLocally(base *piano.Params, start knobSet, startScore float64, notes []int, refs map[int][]float64, rs renderSettings) (knobSet, float64, int) {
	best := start
	bestScore := startScore
	evals := 0

	stepPartials := 2
	stepExp := 0.24
	stepExcite := 0.22
	stepUndamped := 0.22
	stepDamped := 0.22

	for round := 0; round < 4; round++ {
		try := func(next knobSet) {
			score, _, err := evaluateKnobs(base, next, notes, refs, rs)
			if err != nil {
				return
			}
			evals++
			if score < bestScore {
				best = next
				bestScore = score
			}
		}

		try(normalizeKnobs(knobSet{
			ModalPartials:     best.ModalPartials + stepPartials,
			ModalGainExponent: best.ModalGainExponent,
			ModalExcitation:   best.ModalExcitation,
			ModalUndampedLoss: best.ModalUndampedLoss,
			ModalDampedLoss:   best.ModalDampedLoss,
		}))
		try(normalizeKnobs(knobSet{
			ModalPartials:     best.ModalPartials - stepPartials,
			ModalGainExponent: best.ModalGainExponent,
			ModalExcitation:   best.ModalExcitation,
			ModalUndampedLoss: best.ModalUndampedLoss,
			ModalDampedLoss:   best.ModalDampedLoss,
		}))

		try(normalizeKnobs(knobSet{
			ModalPartials:     best.ModalPartials,
			ModalGainExponent: best.ModalGainExponent + stepExp,
			ModalExcitation:   best.ModalExcitation,
			ModalUndampedLoss: best.ModalUndampedLoss,
			ModalDampedLoss:   best.ModalDampedLoss,
		}))
		try(normalizeKnobs(knobSet{
			ModalPartials:     best.ModalPartials,
			ModalGainExponent: best.ModalGainExponent - stepExp,
			ModalExcitation:   best.ModalExcitation,
			ModalUndampedLoss: best.ModalUndampedLoss,
			ModalDampedLoss:   best.ModalDampedLoss,
		}))

		try(normalizeKnobs(knobSet{
			ModalPartials:     best.ModalPartials,
			ModalGainExponent: best.ModalGainExponent,
			ModalExcitation:   best.ModalExcitation + stepExcite,
			ModalUndampedLoss: best.ModalUndampedLoss,
			ModalDampedLoss:   best.ModalDampedLoss,
		}))
		try(normalizeKnobs(knobSet{
			ModalPartials:     best.ModalPartials,
			ModalGainExponent: best.ModalGainExponent,
			ModalExcitation:   best.ModalExcitation - stepExcite,
			ModalUndampedLoss: best.ModalUndampedLoss,
			ModalDampedLoss:   best.ModalDampedLoss,
		}))

		try(normalizeKnobs(knobSet{
			ModalPartials:     best.ModalPartials,
			ModalGainExponent: best.ModalGainExponent,
			ModalExcitation:   best.ModalExcitation,
			ModalUndampedLoss: best.ModalUndampedLoss + stepUndamped,
			ModalDampedLoss:   best.ModalDampedLoss,
		}))
		try(normalizeKnobs(knobSet{
			ModalPartials:     best.ModalPartials,
			ModalGainExponent: best.ModalGainExponent,
			ModalExcitation:   best.ModalExcitation,
			ModalUndampedLoss: best.ModalUndampedLoss - stepUndamped,
			ModalDampedLoss:   best.ModalDampedLoss,
		}))

		try(normalizeKnobs(knobSet{
			ModalPartials:     best.ModalPartials,
			ModalGainExponent: best.ModalGainExponent,
			ModalExcitation:   best.ModalExcitation,
			ModalUndampedLoss: best.ModalUndampedLoss,
			ModalDampedLoss:   best.ModalDampedLoss + stepDamped,
		}))
		try(normalizeKnobs(knobSet{
			ModalPartials:     best.ModalPartials,
			ModalGainExponent: best.ModalGainExponent,
			ModalExcitation:   best.ModalExcitation,
			ModalUndampedLoss: best.ModalUndampedLoss,
			ModalDampedLoss:   best.ModalDampedLoss - stepDamped,
		}))

		stepPartials = maxInt(1, stepPartials/2)
		stepExp *= 0.55
		stepExcite *= 0.55
		stepUndamped *= 0.55
		stepDamped *= 0.55
	}
	return best, bestScore, evals
}

func initialKnobs(p *piano.Params) knobSet {
	if p == nil {
		return knobSet{
			ModalPartials:     8,
			ModalGainExponent: 1.1,
			ModalExcitation:   1.0,
			ModalUndampedLoss: 1.0,
			ModalDampedLoss:   1.0,
		}
	}
	return normalizeKnobs(knobSet{
		ModalPartials:     p.ModalPartials,
		ModalGainExponent: float64(p.ModalGainExponent),
		ModalExcitation:   float64(p.ModalExcitation),
		ModalUndampedLoss: float64(p.ModalUndampedLoss),
		ModalDampedLoss:   float64(p.ModalDampedLoss),
	})
}

func normalizeKnobs(k knobSet) knobSet {
	if k.ModalPartials < 2 {
		k.ModalPartials = 2
	}
	if k.ModalPartials > 20 {
		k.ModalPartials = 20
	}
	k.ModalGainExponent = clamp(k.ModalGainExponent, 0.4, 3.2)
	k.ModalExcitation = clamp(k.ModalExcitation, 0.2, 4.0)
	k.ModalUndampedLoss = clamp(k.ModalUndampedLoss, 0.4, 2.4)
	k.ModalDampedLoss = clamp(k.ModalDampedLoss, 0.4, 3.2)
	return k
}

func knobsToNormalized(k knobSet) []float64 {
	k = normalizeKnobs(k)
	return []float64{
		normalizeUnit(float64(k.ModalPartials), 2.0, 20.0),
		normalizeUnit(k.ModalGainExponent, 0.4, 3.2),
		normalizeUnit(k.ModalExcitation, 0.2, 4.0),
		normalizeUnit(k.ModalUndampedLoss, 0.4, 2.4),
		normalizeUnit(k.ModalDampedLoss, 0.4, 3.2),
	}
}

func knobsFromNormalized(pos []float64) knobSet {
	get := func(idx int, lo float64, hi float64, round bool) float64 {
		v := 0.0
		if idx >= 0 && idx < len(pos) {
			v = clamp(pos[idx], 0, 1)
		}
		out := lo + v*(hi-lo)
		if round {
			out = math.Round(out)
		}
		return out
	}
	return normalizeKnobs(knobSet{
		ModalPartials:     int(get(0, 2.0, 20.0, true)),
		ModalGainExponent: get(1, 0.4, 3.2, false),
		ModalExcitation:   get(2, 0.2, 4.0, false),
		ModalUndampedLoss: get(3, 0.4, 2.4, false),
		ModalDampedLoss:   get(4, 0.4, 3.2, false),
	})
}

func normalizeUnit(v float64, lo float64, hi float64) float64 {
	if hi <= lo {
		return 0
	}
	return clamp((v-lo)/(hi-lo), 0, 1)
}

func applyModalKnobs(p *piano.Params, k knobSet) {
	if p == nil {
		return
	}
	k = normalizeKnobs(k)
	p.ModalPartials = k.ModalPartials
	p.ModalGainExponent = float32(k.ModalGainExponent)
	p.ModalExcitation = float32(k.ModalExcitation)
	p.ModalUndampedLoss = float32(k.ModalUndampedLoss)
	p.ModalDampedLoss = float32(k.ModalDampedLoss)
}

func renderNote(params *piano.Params, rs renderSettings) ([]float64, error) {
	if params == nil {
		return nil, errors.New("nil params")
	}
	p := piano.NewPiano(rs.sampleRate, 16, params)
	p.NoteOn(rs.note, rs.velocity)

	minFrames := int(float64(rs.sampleRate) * rs.minDurationSec)
	maxFrames := int(float64(rs.sampleRate) * rs.maxDurationSec)
	releaseFrame := int(float64(rs.sampleRate) * rs.releaseAfter)
	if releaseFrame < 0 {
		releaseFrame = 0
	}
	if maxFrames < 1 {
		return nil, errors.New("invalid max duration")
	}

	threshold := math.Pow(10.0, rs.decayDBFS/20.0)
	frames := 0
	belowCount := 0
	noteReleased := false
	stereo := make([]float32, 0, maxFrames*2)

	for frames < maxFrames {
		block := rs.blockSize
		if block < 16 {
			block = 16
		}
		if frames+block > maxFrames {
			block = maxFrames - frames
		}
		if !noteReleased && frames >= releaseFrame {
			p.NoteOff(rs.note)
			noteReleased = true
		}

		out := p.Process(block)
		stereo = append(stereo, out...)
		frames += block

		if frames >= minFrames {
			if stereoRMS(out) < threshold {
				belowCount++
				if belowCount >= rs.decayHold {
					break
				}
			} else {
				belowCount = 0
			}
		}
	}

	mono := make([]float64, len(stereo)/2)
	for i := 0; i < len(mono); i++ {
		l := float64(stereo[i*2])
		r := float64(stereo[i*2+1])
		v := 0.5 * (l + r)
		if !isFiniteFloat(v) {
			v = 0
		}
		mono[i] = v
	}
	return mono, nil
}

func stereoRMS(interleaved []float32) float64 {
	if len(interleaved) == 0 {
		return 0
	}
	var sum float64
	for _, s := range interleaved {
		v := float64(s)
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(interleaved)))
}

func parseNotes(raw string) ([]int, error) {
	parts := strings.Split(raw, ",")
	notes := make([]int, 0, len(parts))
	seen := make(map[int]bool)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("invalid note %q", part)
		}
		if n < 0 || n > 127 {
			return nil, fmt.Errorf("note out of range [0,127]: %d", n)
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		notes = append(notes, n)
	}
	if len(notes) == 0 {
		return nil, errors.New("empty notes list")
	}
	return notes, nil
}

func cloneParams(src *piano.Params) *piano.Params {
	if src == nil {
		return piano.NewDefaultParams()
	}
	d := *src
	d.PerNote = make(map[int]*piano.NoteParams, len(src.PerNote))
	for k, v := range src.PerNote {
		if v == nil {
			d.PerNote[k] = nil
			continue
		}
		nv := *v
		d.PerNote[k] = &nv
	}
	return &d
}

func writePreset(path string, p *piano.Params) error {
	if p == nil {
		return errors.New("nil params")
	}
	type noteEntry struct {
		F0             float32 `json:"f0,omitempty"`
		Inharmonicity  float32 `json:"inharmonicity,omitempty"`
		Loss           float32 `json:"loss,omitempty"`
		StrikePosition float32 `json:"strike_position,omitempty"`
	}
	type out struct {
		OutputGain                 float32              `json:"output_gain"`
		IRWavPath                  string               `json:"ir_wav_path,omitempty"`
		IRWetMix                   float32              `json:"ir_wet_mix"`
		IRDryMix                   float32              `json:"ir_dry_mix"`
		IRGain                     float32              `json:"ir_gain"`
		BodyIRWavPath              string               `json:"body_ir_wav_path,omitempty"`
		BodyIRGain                 float32              `json:"body_ir_gain"`
		BodyDryMix                 float32              `json:"body_dry_mix"`
		RoomIRWavPath              string               `json:"room_ir_wav_path,omitempty"`
		RoomWetMix                 float32              `json:"room_wet_mix"`
		RoomGain                   float32              `json:"room_gain"`
		ResonanceEnabled           bool                 `json:"resonance_enabled"`
		ResonanceGain              float32              `json:"resonance_gain"`
		ResonancePerNoteFilter     bool                 `json:"resonance_per_note_filter"`
		HammerStiffnessScale       float32              `json:"hammer_stiffness_scale"`
		HammerExponentScale        float32              `json:"hammer_exponent_scale"`
		HammerDampingScale         float32              `json:"hammer_damping_scale"`
		HammerInitialVelocityScale float32              `json:"hammer_initial_velocity_scale"`
		HammerContactTimeScale     float32              `json:"hammer_contact_time_scale"`
		UnisonDetuneScale          float32              `json:"unison_detune_scale"`
		UnisonCrossfeed            float32              `json:"unison_crossfeed"`
		StringModel                string               `json:"string_model"`
		ModalPartials              int                  `json:"modal_partials"`
		ModalGainExponent          float32              `json:"modal_gain_exponent"`
		ModalExcitation            float32              `json:"modal_excitation"`
		ModalUndampedLoss          float32              `json:"modal_undamped_loss"`
		ModalDampedLoss            float32              `json:"modal_damped_loss"`
		CouplingEnabled            bool                 `json:"coupling_enabled"`
		CouplingOctaveGain         float32              `json:"coupling_octave_gain"`
		CouplingFifthGain          float32              `json:"coupling_fifth_gain"`
		CouplingMaxForce           float32              `json:"coupling_max_force"`
		CouplingMode               string               `json:"coupling_mode"`
		CouplingAmount             float32              `json:"coupling_amount"`
		CouplingHarmonicFalloff    float32              `json:"coupling_harmonic_falloff"`
		CouplingDetuneSigmaCents   float32              `json:"coupling_detune_sigma_cents"`
		CouplingDistanceExponent   float32              `json:"coupling_distance_exponent"`
		CouplingMaxNeighbors       int                  `json:"coupling_max_neighbors"`
		SoftPedalStrikeOffset      float32              `json:"soft_pedal_strike_offset"`
		SoftPedalHardness          float32              `json:"soft_pedal_hardness"`
		AttackNoiseLevel           float32              `json:"attack_noise_level,omitempty"`
		AttackNoiseDurationMs      float32              `json:"attack_noise_duration_ms,omitempty"`
		AttackNoiseColor           float32              `json:"attack_noise_color,omitempty"`
		PerNote                    map[string]noteEntry `json:"per_note,omitempty"`
	}

	o := out{
		OutputGain:                 p.OutputGain,
		IRWavPath:                  p.IRWavPath,
		IRWetMix:                   p.IRWetMix,
		IRDryMix:                   p.IRDryMix,
		IRGain:                     p.IRGain,
		BodyIRWavPath:              p.BodyIRWavPath,
		BodyIRGain:                 p.BodyIRGain,
		BodyDryMix:                 p.BodyDryMix,
		RoomIRWavPath:              p.RoomIRWavPath,
		RoomWetMix:                 p.RoomWetMix,
		RoomGain:                   p.RoomGain,
		ResonanceEnabled:           p.ResonanceEnabled,
		ResonanceGain:              p.ResonanceGain,
		ResonancePerNoteFilter:     p.ResonancePerNoteFilter,
		HammerStiffnessScale:       p.HammerStiffnessScale,
		HammerExponentScale:        p.HammerExponentScale,
		HammerDampingScale:         p.HammerDampingScale,
		HammerInitialVelocityScale: p.HammerInitialVelocityScale,
		HammerContactTimeScale:     p.HammerContactTimeScale,
		UnisonDetuneScale:          p.UnisonDetuneScale,
		UnisonCrossfeed:            p.UnisonCrossfeed,
		StringModel:                string(p.StringModel),
		ModalPartials:              p.ModalPartials,
		ModalGainExponent:          p.ModalGainExponent,
		ModalExcitation:            p.ModalExcitation,
		ModalUndampedLoss:          p.ModalUndampedLoss,
		ModalDampedLoss:            p.ModalDampedLoss,
		CouplingEnabled:            p.CouplingEnabled,
		CouplingOctaveGain:         p.CouplingOctaveGain,
		CouplingFifthGain:          p.CouplingFifthGain,
		CouplingMaxForce:           p.CouplingMaxForce,
		CouplingMode:               string(p.CouplingMode),
		CouplingAmount:             p.CouplingAmount,
		CouplingHarmonicFalloff:    p.CouplingHarmonicFalloff,
		CouplingDetuneSigmaCents:   p.CouplingDetuneSigmaCents,
		CouplingDistanceExponent:   p.CouplingDistanceExponent,
		CouplingMaxNeighbors:       p.CouplingMaxNeighbors,
		SoftPedalStrikeOffset:      p.SoftPedalStrikeOffset,
		SoftPedalHardness:          p.SoftPedalHardness,
		AttackNoiseLevel:           p.AttackNoiseLevel,
		AttackNoiseDurationMs:      p.AttackNoiseDurationMs,
		AttackNoiseColor:           p.AttackNoiseColor,
		PerNote:                    map[string]noteEntry{},
	}
	for note, np := range p.PerNote {
		if np == nil {
			continue
		}
		o.PerNote[strconv.Itoa(note)] = noteEntry{
			F0:             np.F0,
			Inharmonicity:  np.Inharmonicity,
			Loss:           np.Loss,
			StrikePosition: np.StrikePosition,
		}
	}
	return writeJSON(path, o)
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}

func newMayflyConfig(variant string, pop int, dims int, iters int) (*mayfly.Config, error) {
	var cfg *mayfly.Config
	switch variant {
	case "ma":
		cfg = mayfly.NewDefaultConfig()
	case "desma":
		cfg = mayfly.NewDESMAConfig()
	case "olce":
		cfg = mayfly.NewOLCEConfig()
	case "eobbma":
		cfg = mayfly.NewEOBBMAConfig()
	case "gsasma":
		cfg = mayfly.NewGSASMAConfig()
	case "mpma":
		cfg = mayfly.NewMPMAConfig()
	case "aoblmoa":
		cfg = mayfly.NewAOBLMOAConfig()
	default:
		return nil, fmt.Errorf("unsupported variant %q", variant)
	}
	cfg.ProblemSize = dims
	cfg.LowerBound = 0.0
	cfg.UpperBound = 1.0
	cfg.MaxIterations = iters
	cfg.NPop = pop
	cfg.NPopF = pop
	cfg.NC = 2 * pop
	cfg.NM = maxInt(1, int(math.Round(0.05*float64(pop))))
	return cfg, nil
}

func runMayfly(cfg *mayfly.Config) (_ *mayfly.Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("mayfly panic: %v", r)
		}
	}()
	return mayfly.Optimize(cfg)
}

func clamp(v float64, lo float64, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func sanitizeMetrics(m analysis.Metrics) analysis.Metrics {
	if !isFiniteFloat(m.TimeRMSE) {
		m.TimeRMSE = 1.0
	}
	if !isFiniteFloat(m.EnvelopeRMSEDB) {
		m.EnvelopeRMSEDB = 60.0
	}
	if !isFiniteFloat(m.SpectralRMSEDB) {
		m.SpectralRMSEDB = 60.0
	}
	if !isFiniteFloat(m.RefDecayDBPerS) {
		m.RefDecayDBPerS = 0
	}
	if !isFiniteFloat(m.CandDecayDBPerS) {
		m.CandDecayDBPerS = 0
	}
	if !isFiniteFloat(m.DecayDiffDBPerS) {
		m.DecayDiffDBPerS = 60.0
	}
	if !isFiniteFloat(m.Score) {
		m.Score = 1.0
	}
	m.Score = clamp(m.Score, 0, 1)
	if !isFiniteFloat(m.Similarity) {
		m.Similarity = 0.0
	}
	m.Similarity = clamp(m.Similarity, 0, 1)
	return m
}

func isFiniteFloat(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
