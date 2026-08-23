package main

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/cwbudde/algo-piano/piano"
	"github.com/cwbudde/algo-piano/preset"
)

func TestWriteOutputsPreservesHotBodyIRResponse(t *testing.T) {
	dir := t.TempDir()
	originalIR := []float32{2.469134, -3.75, 4, -0.1875}
	const originalGain = float32(0.75)
	params := piano.NewDefaultParams()
	params.BodyIRGain = originalGain
	req := outputRequest{
		outputIR:     filepath.Join(dir, "modal.wav"),
		outputPreset: filepath.Join(dir, "fitted.json"),
		sampleRate:   48000,
		bestParams:   params,
		bestBodyIR:   originalIR,
	}
	if err := writeOutputs(req); err != nil {
		t.Fatalf("writeOutputs: %v", err)
	}

	loaded, err := preset.LoadJSON(req.outputPreset)
	if err != nil {
		t.Fatalf("load written preset: %v", err)
	}
	if loaded.BodyIRGain != 3 { // peak 4 => exact power-of-two scale 4
		t.Fatalf("BodyIRGain = %g, want 3", loaded.BodyIRGain)
	}
	persisted, sampleRate, err := readWAVMono(loaded.BodyIRWavPath)
	if err != nil {
		t.Fatalf("read written body IR: %v", err)
	}
	if sampleRate != req.sampleRate {
		t.Fatalf("sample rate = %d, want %d", sampleRate, req.sampleRate)
	}
	if len(persisted) != len(originalIR) {
		t.Fatalf("body IR length = %d, want %d", len(persisted), len(originalIR))
	}
	for i, sample := range persisted {
		if math.Abs(sample) > 1 {
			t.Fatalf("persisted sample %d = %g, outside [-1,1]", i, sample)
		}
		got := sample * float64(loaded.BodyIRGain)
		want := float64(originalIR[i]) * float64(originalGain)
		if math.Abs(got-want) > 1e-6 {
			t.Fatalf("effective sample %d = %.9g, want %.9g", i, got, want)
		}
	}
}
