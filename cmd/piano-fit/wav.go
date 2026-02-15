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
	return fitcommon.WriteMonoWAV(path, data, sampleRate)
}

func stereoToMono64(st []float32) []float64 {
	return fitcommon.StereoToMono64(st)
}

func stereoRMS(interleaved []float32) float64 {
	return fitcommon.StereoRMS(interleaved)
}
