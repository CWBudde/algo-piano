package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	dspresample "github.com/cwbudde/algo-dsp/dsp/resample"
	"github.com/cwbudde/algo-piano/analysis"
	"github.com/cwbudde/algo-piano/internal/render"
	"github.com/cwbudde/algo-piano/piano"
	"github.com/cwbudde/algo-piano/preset"
	"github.com/cwbudde/wav"
	"github.com/go-audio/audio"
)

func main() {
	referencePath := flag.String("reference", "reference/c4.wav", "Reference WAV path")
	candidatePath := flag.String("candidate", "", "Candidate WAV path; if empty, render candidate from piano model")
	presetPath := flag.String("preset", "assets/presets/default.json", "Preset JSON path for rendered candidate")
	note := flag.Int("note", 60, "MIDI note for rendered candidate")
	velocity := flag.Int("velocity", 100, "MIDI velocity for rendered candidate")
	sampleRate := flag.Int("sample-rate", 48000, "Analysis sample rate in Hz")
	decayDBFS := flag.Float64("decay-dbfs", -90.0, "Auto-stop threshold in dBFS for rendered candidate")
	decayHoldBlocks := flag.Int("decay-hold-blocks", 6, "Consecutive below-threshold blocks required for stop")
	decayRelative := flag.Bool("decay-relative", true, "Stop the auto-decay render N dB below the render's OWN running peak rather than N dB below full scale. "+
		"Relative makes the render length independent of the absolute output level, which is what makes output_gain score-invariant; "+
		"false restores the pre-2026-08-22 absolute threshold so numbers measured under it can be reproduced")
	minDuration := flag.Float64("min-duration", 2.0, "Minimum rendered duration in seconds")
	maxDuration := flag.Float64("max-duration", 30.0, "Maximum rendered duration in seconds")
	releaseAfter := flag.Float64("release-after", 2.0, "Note hold time before NoteOff for rendered candidate")
	writeCandidate := flag.String("write-candidate", "", "Optional path to write rendered candidate WAV")
	jsonOut := flag.Bool("json", false, "Print metrics as JSON")
	thresholdsPath := flag.String("thresholds", "", "Optional gate threshold JSON path; exit 2 when a metric exceeds its max")
	flag.Parse()

	ref, refSR, err := readWAVMono(*referencePath)
	if err != nil {
		die("failed to read reference: %v", err)
	}
	ref, err = resampleIfNeeded(ref, refSR, *sampleRate)
	if err != nil {
		die("failed to resample reference: %v", err)
	}

	var cand []float64
	if *candidatePath != "" {
		candRaw, candSR, err := readWAVMono(*candidatePath)
		if err != nil {
			die("failed to read candidate: %v", err)
		}
		cand, err = resampleIfNeeded(candRaw, candSR, *sampleRate)
		if err != nil {
			die("failed to resample candidate: %v", err)
		}
	} else {
		stereo, mono, err := renderCandidate(
			*presetPath,
			*note,
			*velocity,
			*sampleRate,
			*decayDBFS,
			*decayHoldBlocks,
			*decayRelative,
			*minDuration,
			*maxDuration,
			*releaseAfter,
		)
		if err != nil {
			die("failed to render candidate: %v", err)
		}
		cand = mono
		if *writeCandidate != "" {
			if err := writeWAVStereo(*writeCandidate, stereo, *sampleRate); err != nil {
				die("failed to write candidate wav: %v", err)
			}
		}
	}

	// The MIDI note is the only thing that says which pitch is being compared.
	// Without it the fundamental is estimated from the spectrum, and a
	// mis-estimated f0 biases every partial ratio derived from it.
	metrics := analysis.CompareWithOptions(ref, cand, *sampleRate, analysis.Options{MIDINote: *note}).Sanitized()
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(metrics); err != nil {
			die("json encode failed: %v", err)
		}
		runGate(*thresholdsPath, metrics)
		return
	}

	fmt.Printf("Reference frames: %d\n", metrics.ReferenceFrames)
	fmt.Printf("Candidate frames: %d\n", metrics.CandidateFrames)
	fmt.Printf("Aligned frames:   %d\n", metrics.AlignedFrames)
	fmt.Printf("Lag:              %d samples (%.3f ms)\n", metrics.LagSamples, 1000.0*float64(metrics.LagSamples)/float64(metrics.SampleRate))
	fmt.Println()
	fmt.Printf("Component        Raw          Norm   Weight  Contribution\n")
	fmt.Printf("─────────────────────────────────────────────────────────\n")
	printComp := func(name string, raw string, norm, weight float64, dominant bool) {
		contrib := norm * weight
		marker := ""
		if dominant {
			marker = " ◄"
		}
		fmt.Printf("%-16s %-12s %5.1f%%  ×%.2f   → %.4f%s\n", name, raw, norm*100, weight, contrib, marker)
	}
	printComp("Time RMSE", fmt.Sprintf("%.6f", metrics.TimeRMSE), metrics.TimeNorm, analysis.WeightTime, metrics.Dominant == "time")
	printComp("Envelope RMSE", fmt.Sprintf("%.1f dB", metrics.EnvelopeRMSEDB), metrics.EnvelopeNorm, analysis.WeightEnvelope, metrics.Dominant == "envelope")
	printComp("Spectral RMSE", fmt.Sprintf("%.1f dB", metrics.SpectralRMSEDB), metrics.SpectralNorm, analysis.WeightSpectral, metrics.Dominant == "spectral")
	printComp("Decay diff", fmt.Sprintf("%.1f dB/s", metrics.DecayDiffDBPerS), metrics.DecayNorm, analysis.WeightDecay, metrics.Dominant == "decay")
	fmt.Printf("─────────────────────────────────────────────────────────\n")
	fmt.Printf("Score:            %.4f  (0 best, 1 worst)\n", metrics.Score)
	fmt.Printf("Similarity:       %.2f%%\n", metrics.Similarity*100.0)
	fmt.Printf("Dominant factor:  %s\n", metrics.Dominant)
	fmt.Printf("\nDecay slopes: ref=%.1f dB/s  cand=%.1f dB/s\n", metrics.RefDecayDBPerS, metrics.CandDecayDBPerS)
	fmt.Printf("\nSpectral bands:   low(0-500Hz)=%.1f dB  mid(500-2k)=%.1f dB  high(2k+)=%.1f dB\n",
		metrics.SpectralLowRMSEDB, metrics.SpectralMidRMSEDB, metrics.SpectralHighRMSEDB)

	fmt.Printf("\nPartials:         f0=%.2f Hz  level=%.1f dB  freq=%.1f cents  tristimulus=%.3f\n",
		metrics.F0Hz, metrics.PartialLevelRMSEDB, metrics.PartialFreqRMSECents, metrics.TristimulusDistance)
	if metrics.AttackAvailable {
		fmt.Printf("Attack:           rise ref=%.1f ms cand=%.1f ms (diff %.1f ms)  centroid ref=%.0f Hz cand=%.0f Hz (%.3f oct)\n",
			metrics.RefRiseTimeMS, metrics.CandRiseTimeMS, metrics.AttackRiseDiffMS,
			metrics.RefAttackCentroidHz, metrics.CandAttackCentroidHz, metrics.AttackCentroidRMSEOct)
	} else {
		fmt.Printf("Attack:           unavailable (no rising onset in the compared window)\n")
	}

	printSaturationWarnings(metrics)

	runGate(*thresholdsPath, metrics)
}

// runGate evaluates the threshold file at path against m, prints the verdict,
// and exits with status 2 when any enforced metric is breached. An empty path
// disables the gate entirely.
func runGate(path string, m analysis.Metrics) {
	if path == "" {
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		die("failed to read thresholds: %v", err)
	}
	var spec gateSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		die("failed to parse thresholds %s: %v", path, err)
	}
	breaches, worstUsed, worstMetric, err := evaluateGate(spec, m)
	if err != nil {
		die("%v", err)
	}
	enforced := spec.enforcedCount()
	if len(breaches) > 0 {
		for _, b := range breaches {
			fmt.Fprintln(os.Stderr, formatBreach(b))
		}
		fmt.Fprintf(os.Stderr, "gate: %d of %d enforced metrics breached\n", len(breaches), enforced)
		os.Exit(2)
	}
	fmt.Printf("gate: PASS %d enforced metrics within thresholds\n", enforced)
	if worstMetric != "" {
		values := metricsByJSONTag(m)
		fmt.Printf("gate: worst headroom %s %.2f/%.2f (%.0f%% of budget used)\n",
			worstMetric, values[worstMetric], *spec.Max[worstMetric], worstUsed*100.0)
	}
}

// saturationWarning describes one component whose norm is pinned at 1.0.
type saturationWarning struct {
	name      string
	saturated bool
	raw       float64
	unit      string
	norm      float64
	normName  string
}

// printSaturationWarnings reports every component whose raw value has met or
// exceeded its normalization constant. Such a component is clamped to 1.0 and
// therefore contributes a constant to the score: it cannot distinguish two
// candidates and gives an optimizer no gradient at all. That is a property of
// the norm, not of the audio, and it is invisible in the score itself, so it
// is called out explicitly.
func printSaturationWarnings(m analysis.Metrics) {
	warnings := []saturationWarning{
		{"time", m.TimeSaturated, m.TimeRMSE, "", analysis.NormTime, "NormTime"},
		{"envelope", m.EnvelopeSaturated, m.EnvelopeRMSEDB, " dB", analysis.NormEnvelope, "NormEnvelope"},
		{"spectral", m.SpectralSaturated, m.SpectralRMSEDB, " dB", analysis.NormSpectral, "NormSpectral"},
		{"decay", m.DecaySaturated, m.DecayDiffDBPerS, " dB/s", analysis.NormDecay, "NormDecay"},
		{"partial_level", m.PartialLevelSaturated, m.PartialLevelRMSEDB, " dB", analysis.NormPartialLevel, "NormPartialLevel"},
		{"partial_freq", m.PartialFreqSaturated, m.PartialFreqRMSECents, " cents", analysis.NormPartialFreq, "NormPartialFreq"},
		{"tristimulus", m.TristimulusSaturated, m.TristimulusDistance, "", analysis.NormTristimulus, "NormTristimulus"},
		{"decay_segment", m.DecaySegmentSaturated, m.DecaySegmentRMSEDBPerS, " dB/s", analysis.NormDecaySegment, "NormDecaySegment"},
	}
	first := true
	for _, w := range warnings {
		if !w.saturated {
			continue
		}
		if first {
			fmt.Println()
			first = false
		}
		fmt.Fprintf(os.Stderr,
			"WARNING: %s component saturated (%.1f%s >= %s %.1f) - this component provides no gradient\n",
			w.name, w.raw, w.unit, w.normName, w.norm)
	}
	if m.AttackSaturated {
		if first {
			fmt.Println()
		}
		fmt.Fprintf(os.Stderr,
			"WARNING: attack component saturated (rise %.1f ms >= NormAttackRise %.1f and centroid %.3f oct >= NormAttackCentroid %.3f) - this component provides no gradient\n",
			m.AttackRiseDiffMS, analysis.NormAttackRise, m.AttackCentroidRMSEOct, analysis.NormAttackCentroid)
	}
}

func renderCandidate(
	presetPath string,
	note int,
	velocity int,
	sampleRate int,
	decayDBFS float64,
	decayHoldBlocks int,
	decayRelative bool,
	minDuration float64,
	maxDuration float64,
	releaseAfter float64,
) ([]float32, []float64, error) {
	params, err := preset.LoadJSON(presetPath)
	if err != nil {
		return nil, nil, err
	}
	// Fall back to the shipped IR as the *room* stage of the radiation path, so
	// scoring never silently selects the legacy single-IR path as its default.
	piano.ApplyDefaultRoomIR(params)

	p := piano.NewPiano(sampleRate, 16, params)
	p.NoteOn(note, velocity)

	if decayHoldBlocks < 1 {
		decayHoldBlocks = 1
	}
	if minDuration < 0 {
		minDuration = 0
	}
	if maxDuration < minDuration {
		maxDuration = minDuration
	}

	minFrames := int(float64(sampleRate) * minDuration)
	maxFrames := int(float64(sampleRate) * maxDuration)
	releaseAtFrame := int(float64(sampleRate) * releaseAfter)
	if releaseAtFrame < 0 {
		releaseAtFrame = 0
	}
	if maxFrames < 1 {
		return nil, nil, errors.New("max duration too small")
	}

	detector := render.NewDecayDetector(decayDBFS, decayHoldBlocks, decayRelative)
	blockSize := 128
	framesRendered := 0
	noteReleased := false
	stereo := make([]float32, 0, maxFrames*2)

	for framesRendered < maxFrames {
		framesToRender := blockSize
		if framesRendered+framesToRender > maxFrames {
			framesToRender = maxFrames - framesRendered
		}
		if !noteReleased && framesRendered >= releaseAtFrame {
			p.NoteOff(note)
			noteReleased = true
		}
		block := p.Process(framesToRender)
		stereo = append(stereo, block...)
		framesRendered += framesToRender

		if framesRendered >= minFrames {
			if detector.Update(block) {
				break
			}
		} else {
			detector.Track(block)
		}
	}

	mono := stereoToMono64(stereo)
	return stereo, mono, nil
}

func readWAVMono(path string) ([]float64, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = f.Close() }()

	dec := wav.NewDecoder(f)
	if !dec.IsValidFile() {
		return nil, 0, fmt.Errorf("invalid wav file: %s", path)
	}
	buf, err := dec.FullPCMBuffer()
	if err != nil {
		return nil, 0, err
	}
	if buf == nil || buf.Format == nil || buf.Format.NumChannels < 1 {
		return nil, 0, fmt.Errorf("invalid wav buffer: %s", path)
	}
	ch := buf.Format.NumChannels
	frames := len(buf.Data) / ch
	out := make([]float64, frames)
	for i := 0; i < frames; i++ {
		var sum float64
		for c := 0; c < ch; c++ {
			sum += float64(buf.Data[i*ch+c])
		}
		out[i] = sum / float64(ch)
	}
	return out, buf.Format.SampleRate, nil
}

func resampleIfNeeded(in []float64, fromRate int, toRate int) ([]float64, error) {
	if fromRate == toRate {
		return in, nil
	}
	r, err := dspresample.NewForRates(
		float64(fromRate),
		float64(toRate),
		dspresample.WithQuality(dspresample.QualityBest),
	)
	if err != nil {
		return nil, err
	}
	return r.Process(in), nil
}

func writeWAVStereo(path string, samples []float32, sampleRate int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	enc := wav.NewEncoder(f, sampleRate, 16, 2, 1)
	defer func() { _ = enc.Close() }()

	buf := &audio.Float32Buffer{
		Format: &audio.Format{
			SampleRate:  sampleRate,
			NumChannels: 2,
		},
		Data:           samples,
		SourceBitDepth: 16,
	}
	return enc.Write(buf)
}

func stereoToMono64(st []float32) []float64 {
	if len(st) < 2 {
		return nil
	}
	n := len(st) / 2
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		out[i] = 0.5 * (float64(st[i*2]) + float64(st[i*2+1]))
	}
	return out
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
