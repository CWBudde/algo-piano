package main

import (
	"flag"
	"fmt"
	"math"
	"os"

	"github.com/cwbudde/algo-piano/irsynth"
	"github.com/cwbudde/wav"
	"github.com/go-audio/audio"
)

func main() {
	cfg := irsynth.DefaultConfig()

	output := flag.String("output", "assets/ir/synth_96k.wav", "Output WAV path")
	flag.IntVar(&cfg.SampleRate, "sample-rate", cfg.SampleRate, "Output sample rate")
	flag.Float64Var(&cfg.DurationS, "duration", cfg.DurationS, "IR length in seconds")
	flag.IntVar(&cfg.Modes, "modes", cfg.Modes, "Number of damped modes")
	flag.Int64Var(&cfg.Seed, "seed", cfg.Seed, "Random seed")
	flag.Float64Var(&cfg.Brightness, "brightness", cfg.Brightness, "Spectral brightness control (>0)")
	flag.Float64Var(&cfg.StereoWidth, "stereo-width", cfg.StereoWidth, "Stereo decorrelation width")
	flag.Float64Var(&cfg.DirectLevel, "direct", cfg.DirectLevel, "Direct impulse level")
	flag.IntVar(&cfg.EarlyCount, "early", cfg.EarlyCount, "Number of early reflections")
	flag.Float64Var(&cfg.LateLevel, "late", cfg.LateLevel, "Diffuse late-tail level")
	flag.Float64Var(&cfg.LowDecayS, "low-decay", cfg.LowDecayS, "Low-frequency decay time (s)")
	flag.Float64Var(&cfg.HighDecayS, "high-decay", cfg.HighDecayS, "High-frequency decay time (s)")
	flag.Float64Var(&cfg.NormalizePeak, "normalize", cfg.NormalizePeak, "Peak normalization target")
	flag.Parse()

	left, right, err := irsynth.GenerateStereo(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ir-synth error: %v\n", err)
		os.Exit(1)
	}

	if err := writeStereoWAV(*output, left, right, cfg.SampleRate); err != nil {
		fmt.Fprintf(os.Stderr, "wav write error: %v\n", err)
		os.Exit(1)
	}

	peak, rms := stats(left, right)
	fmt.Printf("Wrote %s\n", *output)
	fmt.Printf("SampleRate: %d Hz, Duration: %.3f s, Samples: %d\n", cfg.SampleRate, cfg.DurationS, len(left))
	fmt.Printf("Peak: %.6f, RMS: %.6f\n", peak, rms)
}

func writeStereoWAV(path string, left []float32, right []float32, sampleRate int) error {
	if len(left) != len(right) {
		return fmt.Errorf("left/right length mismatch")
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := wav.NewEncoder(f, sampleRate, 16, 2, 1)
	defer enc.Close()

	data := make([]float32, len(left)*2)
	for i := 0; i < len(left); i++ {
		data[i*2] = left[i]
		data[i*2+1] = right[i]
	}
	buf := &audio.Float32Buffer{
		Format: &audio.Format{
			SampleRate:  sampleRate,
			NumChannels: 2,
		},
		Data:           data,
		SourceBitDepth: 16,
	}
	return enc.Write(buf)
}

func stats(left []float32, right []float32) (peak float64, rms float64) {
	if len(left) == 0 || len(right) == 0 {
		return 0, 0
	}
	var sum float64
	n := len(left) * 2
	for i := 0; i < len(left); i++ {
		lv := float64(left[i])
		rv := float64(right[i])
		a := math.Abs(lv)
		if b := math.Abs(rv); b > a {
			a = b
		}
		if a > peak {
			peak = a
		}
		sum += lv*lv + rv*rv
	}
	return peak, math.Sqrt(sum / float64(n))
}
