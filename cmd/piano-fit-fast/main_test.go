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

func TestParseWorkersFlag(t *testing.T) {
	tests := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{in: "1", want: 1},
		{in: "8", want: 8},
		{in: "auto", want: 0},
		{in: "AUTO", want: 0},
		{in: "0", wantErr: true},
		{in: "-2", wantErr: true},
		{in: "abc", wantErr: true},
	}

	for _, tt := range tests {
		got, err := parseWorkersFlag(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("parseWorkersFlag(%q) expected error", tt.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("parseWorkersFlag(%q) unexpected error: %v", tt.in, err)
		}
		if got != tt.want {
			t.Fatalf("parseWorkersFlag(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}
