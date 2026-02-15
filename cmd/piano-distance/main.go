package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"

	dspresample "github.com/cwbudde/algo-dsp/dsp/resample"
	"github.com/cwbudde/algo-piano/analysis"
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
	minDuration := flag.Float64("min-duration", 2.0, "Minimum rendered duration in seconds")
	maxDuration := flag.Float64("max-duration", 30.0, "Maximum rendered duration in seconds")
	releaseAfter := flag.Float64("release-after", 2.0, "Note hold time before NoteOff for rendered candidate")
	writeCandidate := flag.String("write-candidate", "", "Optional path to write rendered candidate WAV")
	jsonOut := flag.Bool("json", false, "Print metrics as JSON")
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

	metrics := analysis.Compare(ref, cand, *sampleRate)
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(metrics); err != nil {
			die("json encode failed: %v", err)
		}
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
}

func renderCandidate(
	presetPath string,
	note int,
	velocity int,
	sampleRate int,
	decayDBFS float64,
	decayHoldBlocks int,
	minDuration float64,
	maxDuration float64,
	releaseAfter float64,
) ([]float32, []float64, error) {
	params, err := preset.LoadJSON(presetPath)
	if err != nil {
		return nil, nil, err
	}
	if params.IRWavPath == "" {
		params.IRWavPath = piano.DefaultIRWavPath
	}

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

	threshold := math.Pow(10.0, decayDBFS/20.0)
	blockSize := 128
	framesRendered := 0
	belowCount := 0
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
			if stereoRMS(block) < threshold {
				belowCount++
				if belowCount >= decayHoldBlocks {
					break
				}
			} else {
				belowCount = 0
			}
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
	defer f.Close()

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
	defer f.Close()

	enc := wav.NewEncoder(f, sampleRate, 16, 2, 1)
	defer enc.Close()

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

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
