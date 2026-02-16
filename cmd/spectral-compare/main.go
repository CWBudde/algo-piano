package main

import (
	"flag"
	"fmt"
	"math"
	"math/cmplx"
	"os"

	algofft "github.com/cwbudde/algo-fft"
	"github.com/cwbudde/algo-piano/analysis"
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

	// Report peak and RMS levels.
	refPeak, candPeak := peakAbs(ref), peakAbs(cand)
	refRMS, candRMS := rms(ref), rms(cand)
	fmt.Printf("Peak levels: ref=%.4f (%.1f dB)  cand=%.4f (%.1f dB)  gap=%+.1f dB\n",
		refPeak, toDB(refPeak), candPeak, toDB(candPeak), toDB(candPeak)-toDB(refPeak))
	fmt.Printf("RMS  levels: ref=%.4f (%.1f dB)  cand=%.4f (%.1f dB)  gap=%+.1f dB\n",
		refRMS, toDB(refRMS), candRMS, toDB(candRMS), toDB(candRMS)-toDB(refRMS))

	// FFT-based lag estimation via cross-correlation.
	lag := estimateLagXCorr(ref, cand, sr/2)
	fmt.Printf("Lag: %d samples (%.1f ms)\n", lag, float64(lag)/float64(sr)*1000)

	// Align signals.
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

	// STFT analysis with adaptive FFT size.
	maxFFTSize := 4096

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

	var totalSumSq float64
	var totalCnt int

	for _, tw := range windows {
		startSamp := int(tw.startMs / 1000.0 * float64(sr))
		endSamp := int(tw.endMs / 1000.0 * float64(sr))
		if endSamp > n {
			endSamp = n
		}
		if startSamp >= endSamp {
			continue
		}

		// Per-window time-domain RMS.
		winRef := ref[startSamp:endSamp]
		winCand := cand[startSamp:endSamp]
		wRefRMS := rms(winRef)
		wCandRMS := rms(winCand)

		// Adaptive FFT size: use smaller FFT for short windows.
		winSamples := endSamp - startSamp
		fftSize := maxFFTSize
		for fftSize > winSamples && fftSize > 256 {
			fftSize /= 2
		}
		if fftSize < 256 {
			fftSize = 256
		}
		hop := fftSize / 2
		nBins := fftSize / 2

		plan, planErr := algofft.NewPlanReal64(fftSize)
		if planErr != nil {
			fmt.Fprintf(os.Stderr, "fft plan (%d): %v\n", fftSize, planErr)
			continue
		}

		binHz := float64(sr) / float64(fftSize)
		hann := makeHann(fftSize)
		specRef := make([]complex128, fftSize/2+1)
		specCand := make([]complex128, fftSize/2+1)
		bufRef := make([]float64, fftSize)
		bufCand := make([]float64, fftSize)

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

		// Fallback for very short windows: zero-pad single frame.
		if nFrames == 0 {
			clear(bufRef)
			clear(bufCand)
			for i := 0; i < winSamples && i < fftSize; i++ {
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

		fmt.Printf("--- %s (%d STFT frames, FFT=%d) ---\n", tw.name, nFrames, fftSize)
		fmt.Printf("  RMS: ref=%.1f dB  cand=%.1f dB  gap=%+.1f dB\n",
			toDB(wRefRMS), toDB(wCandRMS), toDB(wCandRMS)-toDB(wRefRMS))

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
			totalSumSq += sumSq
			totalCnt += cnt
		}
		fmt.Println()
	}

	// Overall spectral summary.
	if totalCnt > 0 {
		fmt.Printf("=== Overall spectral RMSE: %.1f dB (across %d bins) ===\n\n",
			math.Sqrt(totalSumSq/float64(totalCnt)), totalCnt)
	}

	// Optimizer-aligned metrics (uses RMS normalization internally, like piano-fit).
	m := analysis.Compare(ref[:n], cand[:n], sr)
	fmt.Printf("=== Optimizer metrics (RMS-normalized) ===\n")
	fmt.Printf("  TimeRMSE:        %.4f  (norm'd to 0.25 → %.1f%%)\n", m.TimeRMSE, clamp01(m.TimeRMSE/0.25)*100)
	fmt.Printf("  EnvelopeRMSEDB:  %.1f dB (norm'd to 30 → %.1f%%)\n", m.EnvelopeRMSEDB, clamp01(m.EnvelopeRMSEDB/30)*100)
	fmt.Printf("  SpectralRMSEDB:  %.1f dB (norm'd to 30 → %.1f%%)\n", m.SpectralRMSEDB, clamp01(m.SpectralRMSEDB/30)*100)
	fmt.Printf("    low(0-500Hz):  %.1f dB  mid(500-2k): %.1f dB  high(2k+): %.1f dB\n",
		m.SpectralLowRMSEDB, m.SpectralMidRMSEDB, m.SpectralHighRMSEDB)
	fmt.Printf("  DecayDiffDBPerS: %.1f dB/s (norm'd to 40 → %.1f%%)\n", m.DecayDiffDBPerS, clamp01(m.DecayDiffDBPerS/40)*100)
	fmt.Printf("  Score:           %.3f  Similarity: %.3f\n", m.Score, m.Similarity)
}

func toDB(x float64) float64 {
	if x < 1e-12 {
		x = 1e-12
	}
	return 20 * math.Log10(x)
}

func peakAbs(x []float64) float64 {
	p := 0.0
	for _, v := range x {
		if a := math.Abs(v); a > p {
			p = a
		}
	}
	return p
}

func rms(x []float64) float64 {
	if len(x) == 0 {
		return 0
	}
	var sum float64
	for _, v := range x {
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(x)))
}

func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

func makeHann(n int) []float64 {
	w := make([]float64, n)
	for i := range w {
		w[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1))
	}
	return w
}

// estimateLagXCorr uses FFT-based cross-correlation to find the best alignment.
func estimateLagXCorr(ref, cand []float64, maxLag int) int {
	if len(ref) == 0 || len(cand) == 0 || maxLag < 1 {
		return 0
	}
	if maxLag > len(ref)-1 {
		maxLag = len(ref) - 1
	}
	if maxLag > len(cand)-1 {
		maxLag = len(cand) - 1
	}

	nfft := nextPow2(len(ref) + len(cand) - 1)
	if nfft < 2 {
		nfft = 2
	}

	plan, err := algofft.NewPlanReal64(nfft)
	if err != nil {
		// Fallback to peak-based alignment.
		return estimateLagPeak(ref, cand)
	}

	inA := make([]float64, nfft)
	inB := make([]float64, nfft)
	copy(inA, ref)
	copy(inB, cand)

	specA := make([]complex128, nfft/2+1)
	specB := make([]complex128, nfft/2+1)
	plan.Forward(specA, inA)
	plan.Forward(specB, inB)

	for i := range specA {
		specA[i] *= cmplx.Conj(specB[i])
	}

	corr := make([]float64, nfft)
	plan.Inverse(corr, specA)

	bestLag := 0
	best := math.Inf(-1)
	for lag := -maxLag; lag <= maxLag; lag++ {
		idx := lag
		if idx < 0 {
			idx += nfft
		}
		if corr[idx] > best {
			best = corr[idx]
			bestLag = lag
		}
	}
	return bestLag
}

func estimateLagPeak(ref, cand []float64) int {
	refPos := peakPos(ref)
	candPos := peakPos(cand)
	return candPos - refPos
}

func peakPos(x []float64) int {
	best := 0.0
	pos := 0
	for i, v := range x {
		if a := math.Abs(v); a > best {
			best = a
			pos = i
		}
	}
	return pos
}

func nextPow2(n int) int {
	if n <= 1 {
		return 1
	}
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}
