package main

import (
	"path/filepath"
	"testing"

	"github.com/cwbudde/algo-piano/analysis"
	"github.com/cwbudde/algo-piano/piano"
	"github.com/cwbudde/algo-piano/preset"
)

type evalFixture struct {
	ref        []float64
	baseParams *piano.Params
	defs       []knobDef
	cand       candidate
	note       int
	sampleRate int
}

func loadEvalFixture(b *testing.B) evalFixture {
	b.Helper()

	const (
		note       = 60
		sampleRate = 48000
	)

	presetPath := filepath.Join("..", "..", "assets", "presets", "default.json")
	baseParams, err := preset.LoadJSON(presetPath)
	if err != nil {
		b.Fatalf("load preset: %v", err)
	}
	if baseParams.IRWavPath == "" {
		baseParams.IRWavPath = piano.DefaultIRWavPath
	}

	refPath := filepath.Join("..", "..", "reference", "c4.wav")
	ref, refSR, err := readWAVMono(refPath)
	if err != nil {
		b.Fatalf("read reference: %v", err)
	}
	ref, err = resampleIfNeeded(ref, refSR, sampleRate)
	if err != nil {
		b.Fatalf("resample reference: %v", err)
	}

	defs, cand := initCandidate(baseParams, note)

	return evalFixture{
		ref:        ref,
		baseParams: baseParams,
		defs:       defs,
		cand:       cand,
		note:       note,
		sampleRate: sampleRate,
	}
}

func BenchmarkEvalRenderAndCompare(b *testing.B) {
	fx := loadEvalFixture(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p, velocity, releaseAfter := applyCandidate(fx.baseParams, fx.note, fx.defs, fx.cand)
		mono, _, err := renderCandidateFromParams(
			p,
			fx.note,
			velocity,
			fx.sampleRate,
			-90,
			6,
			2.0,
			30,
			releaseAfter,
		)
		if err != nil {
			b.Fatalf("render candidate: %v", err)
		}
		_ = analysis.Compare(fx.ref, mono, fx.sampleRate)
	}
}

func BenchmarkEvalRenderOnly(b *testing.B) {
	fx := loadEvalFixture(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p, velocity, releaseAfter := applyCandidate(fx.baseParams, fx.note, fx.defs, fx.cand)
		_, _, err := renderCandidateFromParams(
			p,
			fx.note,
			velocity,
			fx.sampleRate,
			-90,
			6,
			2.0,
			30,
			releaseAfter,
		)
		if err != nil {
			b.Fatalf("render candidate: %v", err)
		}
	}
}

func BenchmarkEvalCompareOnly(b *testing.B) {
	fx := loadEvalFixture(b)

	p, velocity, releaseAfter := applyCandidate(fx.baseParams, fx.note, fx.defs, fx.cand)
	mono, _, err := renderCandidateFromParams(
		p,
		fx.note,
		velocity,
		fx.sampleRate,
		-90,
		6,
		2.0,
		30,
		releaseAfter,
	)
	if err != nil {
		b.Fatalf("render candidate: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = analysis.Compare(fx.ref, mono, fx.sampleRate)
	}
}
