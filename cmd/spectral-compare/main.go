package main

import (
	"flag"
	"fmt"
	"math"
	"math/cmplx"
	"os"

	algofft "github.com/cwbudde/algo-fft"
	fitcommon "github.com/cwbudde/algo-piano/internal/fitcommon"
	"github.com/cwbudde/algo-piano/piano"
	"github.com/cwbudde/algo-piano/preset"
)

func main() {
	refPath := flag.String("reference", "reference/c4.wav", "Reference WAV")
	presetPath := flag.String("preset", "out/stages/stage3.json", "Preset to render")
	note := flag.Int("note", 60, "MIDI note")
	velocity := flag.Int("velocity", 121, "MIDI velocity")
	releaseAfter := flag.Float64("release-after", 3.39, "Release after seconds")
	sampleRate := flag.Int("sample-rate", 48000, "Sample rate")
	flag.Parse()

	sr := *sampleRate

	// Read reference.
	refRaw, _, err := fitcommon.ReadWAVMono(*refPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ref: %v\n", err)
		os.Exit(1)
	}
	ref := make([]float64, len(refRaw))
	for i, v := range refRaw {
		ref[i] = float64(v)
	}
	fmt.Printf("Reference: %d frames @ %d Hz (%.2fs)\n", len(ref), sr, float64(len(ref))/float64(sr))

	// Render candidate.
	params, err := preset.LoadJSON(*presetPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "preset: %v\n", err)
		os.Exit(1)
	}
	p := piano.NewPiano(sr, 16, params)
	p.NoteOn(*note, *velocity)

	maxFrames := sr * 8
	releaseFrame := int(*releaseAfter * float64(sr))
	released := false
	stereo := make([]float32, 0, maxFrames*2)
	rendered := 0
	for rendered < maxFrames {
		block := 128
		if rendered+block > maxFrames {
			block = maxFrames - rendered
		}
		if !released && rendered >= releaseFrame {
			p.NoteOff(*note)
			released = true
		}
		stereo = append(stereo, p.Process(block)...)
		rendered += block
	}
	cand := make([]float64, rendered)
	for i := 0; i < rendered; i++ {
		cand[i] = float64(stereo[i*2]+stereo[i*2+1]) * 0.5
	}
	fmt.Printf("Candidate: %d frames @ %d Hz (%.2fs)\n\n", len(cand), sr, float64(len(cand))/float64(sr))

	// Report peak levels.
	refPeak, candPeak := 0.0, 0.0
	for _, v := range ref {
		if a := math.Abs(v); a > refPeak {
			refPeak = a
		}
	}
	for _, v := range cand {
		if a := math.Abs(v); a > candPeak {
			candPeak = a
		}
	}
	fmt.Printf("Peak levels: ref=%.4f (%.1f dB)  cand=%.4f (%.1f dB)  ratio=%.1fdB\n",
		refPeak, 20*math.Log10(math.Max(refPeak, 1e-12)),
		candPeak, 20*math.Log10(math.Max(candPeak, 1e-12)),
		20*math.Log10(math.Max(candPeak, 1e-12))-20*math.Log10(math.Max(refPeak, 1e-12)))

	// Simple lag estimation: find peak position in both signals.
	refPeakPos, candPeakPos := 0, 0
	for i, v := range ref {
		if math.Abs(v) == refPeak {
			refPeakPos = i
			break
		}
	}
	for i, v := range cand {
		if math.Abs(v) == candPeak {
			candPeakPos = i
			break
		}
	}
	lag := candPeakPos - refPeakPos
	fmt.Printf("Peak positions: ref=%d (%.1fms)  cand=%d (%.1fms)  lag=%d (%.1fms)\n",
		refPeakPos, float64(refPeakPos)/float64(sr)*1000,
		candPeakPos, float64(candPeakPos)/float64(sr)*1000,
		lag, float64(lag)/float64(sr)*1000)

	// Align signals using lag.
	if lag > 0 && lag < len(cand) {
		cand = cand[lag:]
		fmt.Printf("Aligned: shifted candidate by %d samples\n", lag)
	} else if lag < 0 && -lag < len(ref) {
		ref = ref[-lag:]
		fmt.Printf("Aligned: shifted reference by %d samples\n", -lag)
	}
	fmt.Println()

	n := len(ref)
	if len(cand) < n {
		n = len(cand)
	}

	// STFT analysis.
	fftSize := 4096
	hop := 2048
	plan, err := algofft.NewPlanReal64(fftSize)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fft plan: %v\n", err)
		os.Exit(1)
	}

	type band struct {
		name string
		loHz float64
		hiHz float64
	}
	bands := []band{
		{"sub-bass (20-100Hz)", 20, 100},
		{"bass (100-300Hz)", 100, 300},
		{"low-mid (300-1kHz)", 300, 1000},
		{"mid (1-3kHz)", 1000, 3000},
		{"hi-mid (3-6kHz)", 3000, 6000},
		{"high (6-12kHz)", 6000, 12000},
		{"air (12-20kHz)", 12000, 20000},
	}

	type timeWindow struct {
		name    string
		startMs float64
		endMs   float64
	}
	windows := []timeWindow{
		{"attack (0-20ms)", 0, 20},
		{"early (20-100ms)", 20, 100},
		{"sustain (100-500ms)", 100, 500},
		{"decay (0.5-2s)", 500, 2000},
		{"late (2-4s)", 2000, 4000},
	}

	binHz := float64(sr) / float64(fftSize)
	hann := make([]float64, fftSize)
	for i := range hann {
		hann[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(fftSize-1))
	}

	specRef := make([]complex128, fftSize/2+1)
	specCand := make([]complex128, fftSize/2+1)
	bufRef := make([]float64, fftSize)
	bufCand := make([]float64, fftSize)
	nBins := fftSize / 2

	for _, tw := range windows {
		startSamp := int(tw.startMs / 1000.0 * float64(sr))
		endSamp := int(tw.endMs / 1000.0 * float64(sr))
		if endSamp > n {
			endSamp = n
		}
		if startSamp >= endSamp {
			continue
		}

		avgRef := make([]float64, nBins)
		avgCand := make([]float64, nBins)
		nFrames := 0

		for pos := startSamp; pos+fftSize <= endSamp; pos += hop {
			for i := 0; i < fftSize; i++ {
				bufRef[i] = ref[pos+i] * hann[i]
				bufCand[i] = cand[pos+i] * hann[i]
			}
			plan.Forward(specRef, bufRef)
			plan.Forward(specCand, bufCand)
			for k := 1; k < nBins; k++ {
				avgRef[k] += cmplx.Abs(specRef[k])
				avgCand[k] += cmplx.Abs(specCand[k])
			}
			nFrames++
		}

		if nFrames == 0 {
			for i := range bufRef {
				bufRef[i] = 0
				bufCand[i] = 0
			}
			winLen := endSamp - startSamp
			for i := 0; i < winLen && i < fftSize; i++ {
				bufRef[i] = ref[startSamp+i] * hann[i]
				bufCand[i] = cand[startSamp+i] * hann[i]
			}
			plan.Forward(specRef, bufRef)
			plan.Forward(specCand, bufCand)
			for k := 1; k < nBins; k++ {
				avgRef[k] = cmplx.Abs(specRef[k])
				avgCand[k] = cmplx.Abs(specCand[k])
			}
			nFrames = 1
		}

		scale := 1.0 / float64(nFrames)
		for k := range avgRef {
			avgRef[k] *= scale
			avgCand[k] *= scale
		}

		fmt.Printf("--- %s (%d STFT frames) ---\n", tw.name, nFrames)
		for _, b := range bands {
			loK := int(b.loHz / binHz)
			hiK := int(b.hiHz / binHz)
			if loK < 1 {
				loK = 1
			}
			if hiK >= nBins {
				hiK = nBins - 1
			}
			if loK > hiK {
				continue
			}

			var sumSq float64
			var refPow, candPow float64
			cnt := 0
			for k := loK; k <= hiK; k++ {
				rDB := 20 * math.Log10(math.Max(avgRef[k], 1e-12))
				cDB := 20 * math.Log10(math.Max(avgCand[k], 1e-12))
				d := rDB - cDB
				sumSq += d * d
				refPow += avgRef[k] * avgRef[k]
				candPow += avgCand[k] * avgCand[k]
				cnt++
			}
			rmseDB := math.Sqrt(sumSq / float64(cnt))
			refDB := 10 * math.Log10(math.Max(refPow/float64(cnt), 1e-24))
			candDB := 10 * math.Log10(math.Max(candPow/float64(cnt), 1e-24))
			diff := candDB - refDB
			marker := ""
			if rmseDB > 15 {
				marker = " <<<"
			}
			if rmseDB > 25 {
				marker = " <<< !!!"
			}
			fmt.Printf("  %-22s RMSE=%5.1fdB  ref=%6.1fdB  cand=%6.1fdB  diff=%+5.1fdB%s\n",
				b.name, rmseDB, refDB, candDB, diff, marker)
		}
		fmt.Println()
	}
}
