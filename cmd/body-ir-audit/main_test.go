package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/cwbudde/algo-piano/internal/bodyiraudit"
)

func TestSelectSourceRequiresExactlyOne(t *testing.T) {
	tests := []struct {
		name string
		opt  options
		want string
	}{
		{name: "none", opt: options{}},
		{name: "wav", opt: options{bodyWAV: "body.wav"}, want: "wav:body.wav"},
		{name: "transfer", opt: options{bodyTransfer: "body.json"}, want: "modal-transfer:body.json"},
		{name: "analytical", opt: options{analytical: true}, want: "analytical-default"},
		{name: "two", opt: options{bodyWAV: "body.wav", analytical: true}},
		{name: "three", opt: options{bodyWAV: "body.wav", bodyTransfer: "body.json", analytical: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := selectSource(tt.opt)
			if tt.want == "" {
				if err == nil || !strings.Contains(err.Error(), "exactly one") {
					t.Fatalf("selectSource error = %v, want exactly-one error", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.name != tt.want {
				t.Fatalf("source name = %q, want %q", got.name, tt.want)
			}
		})
	}
}

func TestValidateOptionsRejectsInvalidInputs(t *testing.T) {
	valid := options{
		reference: "ref.wav", preset: "preset.json", analytical: true,
		format: "json", note: 60, velocity: 118, sampleRate: 48000,
		duration: 8, releaseAfter: 3.5, modalGain: 1, modalLossScale: 1,
		modalDuration: 0.3, modalFadeOut: 0.01,
	}
	tests := []struct {
		name   string
		mutate func(*options)
	}{
		{"reference", func(o *options) { o.reference = "" }},
		{"preset", func(o *options) { o.preset = "" }},
		{"note", func(o *options) { o.note = 128 }},
		{"velocity", func(o *options) { o.velocity = 0 }},
		{"sample rate", func(o *options) { o.sampleRate = 7999 }},
		{"duration", func(o *options) { o.duration = 0 }},
		{"release", func(o *options) { o.releaseAfter = o.duration }},
		{"gain", func(o *options) { o.modalGain = 0 }},
		{"loss", func(o *options) { o.modalLossScale = 0 }},
		{"modal duration", func(o *options) { o.modalDuration = 0 }},
		{"modal fade", func(o *options) { o.modalFadeOut = o.modalDuration + 1 }},
		{"format", func(o *options) { o.format = "yaml" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opt := valid
			tt.mutate(&opt)
			if err := validateOptions(opt); err == nil {
				t.Fatal("validateOptions succeeded, want error")
			}
		})
	}
}

func TestWriteTableContainsAttributionAndWindows(t *testing.T) {
	report := bodyiraudit.Report{
		Source: "fixture",
		FixedGain: bodyiraudit.PairMetrics{
			Identity: bodyiraudit.SignalMetrics{SpectralGaps: []bodyiraudit.WindowSpectralGap{{Name: "attack"}}},
			Selected: bodyiraudit.SignalMetrics{SpectralGaps: []bodyiraudit.WindowSpectralGap{{Name: "attack"}}},
		},
		Attribution: bodyiraudit.Attribution{Classification: "neutral", Explanation: "fixture"},
	}
	var out bytes.Buffer
	if err := writeTable(&out, report); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Body IR attribution (fixture)", "attack fixed spectral gap", "classification: neutral"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("table missing %q:\n%s", want, out.String())
		}
	}
}
