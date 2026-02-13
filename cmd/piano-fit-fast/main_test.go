package main

import (
	"path/filepath"
	"testing"
)

func TestPresetIRPathRelativizesFromPresetDir(t *testing.T) {
	presetPath := filepath.Join("assets", "presets", "fitted-c4.json")
	irPath := filepath.Join("assets", "ir", "default_96k.wav")

	got := presetIRPath(presetPath, irPath)
	want := filepath.ToSlash(filepath.Join("..", "ir", "default_96k.wav"))
	if got != want {
		t.Fatalf("presetIRPath() = %q, want %q", got, want)
	}
}

func TestPresetIRPathEmpty(t *testing.T) {
	if got := presetIRPath("assets/presets/fitted.json", ""); got != "" {
		t.Fatalf("presetIRPath() = %q, want empty", got)
	}
}
