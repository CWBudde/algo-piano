// Command body-ir-audit isolates a body impulse response from the room stage
// and reports whether its apparent benefit survives equal-RMS comparison.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strings"

	"github.com/cwbudde/algo-piano/internal/bodyiraudit"
	"github.com/cwbudde/algo-piano/internal/fitcommon"
	"github.com/cwbudde/algo-piano/irsynth"
	"github.com/cwbudde/algo-piano/piano"
	"github.com/cwbudde/algo-piano/preset"
)

type options struct {
	reference      string
	preset         string
	bodyWAV        string
	bodyTransfer   string
	analytical     bool
	format         string
	output         string
	note           int
	velocity       int
	sampleRate     int
	duration       float64
	releaseAfter   float64
	modalGain      float64
	modalLossScale float64
	modalDuration  float64
	modalFadeOut   float64
}

type bodySource struct {
	name string
	path string
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "body-ir-audit: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("body-ir-audit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var opt options
	fs.StringVar(&opt.reference, "reference", "reference/c4.wav", "reference WAV")
	fs.StringVar(&opt.preset, "preset", "assets/presets/fitted-c4-mayfly.json", "piano preset")
	fs.StringVar(&opt.bodyWAV, "body-wav", "", "selected body IR WAV")
	fs.StringVar(&opt.bodyTransfer, "body-transfer", "", "body-modal-transfer-v1 JSON")
	fs.BoolVar(&opt.analytical, "analytical-default", false, "use irsynth.DefaultBodyConfig")
	fs.StringVar(&opt.format, "format", "table", "output format: table or json")
	fs.StringVar(&opt.output, "output", "", "write report to path instead of stdout")
	fs.IntVar(&opt.note, "note", 60, "MIDI note")
	fs.IntVar(&opt.velocity, "velocity", 118, "MIDI velocity")
	fs.IntVar(&opt.sampleRate, "sample-rate", 48000, "render and analysis sample rate")
	fs.Float64Var(&opt.duration, "duration", 5.0, "fixed render duration in seconds")
	fs.Float64Var(&opt.releaseAfter, "release-after", 3.5, "note release time in seconds")
	fs.Float64Var(&opt.modalGain, "modal-gain", 1.0, "explicit modal transfer gain")
	fs.Float64Var(&opt.modalLossScale, "modal-loss-scale", 1.0, "modal loss-factor multiplier")
	fs.Float64Var(&opt.modalDuration, "modal-duration", 0.3, "rendered modal IR duration in seconds")
	fs.Float64Var(&opt.modalFadeOut, "modal-fade-out", 0.01, "modal IR cosine fade-out in seconds")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}
	if err := validateOptions(opt); err != nil {
		return err
	}
	source, err := selectSource(opt)
	if err != nil {
		return err
	}

	reference, refRate, err := fitcommon.ReadWAVMono(opt.reference)
	if err != nil {
		return fmt.Errorf("read reference: %w", err)
	}
	reference, err = fitcommon.ResampleIfNeeded(reference, refRate, opt.sampleRate)
	if err != nil {
		return fmt.Errorf("resample reference: %w", err)
	}
	wantReferenceFrames := int(math.Round(opt.duration * float64(opt.sampleRate)))
	if len(reference) < wantReferenceFrames {
		return fmt.Errorf("reference has %.3f s, shorter than --duration %.3f s", float64(len(reference))/float64(opt.sampleRate), opt.duration)
	}
	reference = reference[:wantReferenceFrames]

	selectedIR, err := loadSelectedIR(source, opt)
	if err != nil {
		return err
	}
	identity, err := render(opt, []float32{1})
	if err != nil {
		return fmt.Errorf("render identity body: %w", err)
	}
	selected, err := render(opt, selectedIR)
	if err != nil {
		return fmt.Errorf("render selected body: %w", err)
	}

	ir64 := make([]float64, len(selectedIR))
	for i, v := range selectedIR {
		ir64[i] = float64(v)
	}
	report, err := bodyiraudit.Analyze(reference, identity, selected, ir64, bodyiraudit.Config{
		SampleRate: opt.sampleRate, MIDINote: opt.note, ReleaseAfter: opt.releaseAfter, Source: source.name,
	})
	if err != nil {
		return err
	}

	w := stdout
	var closeOutput func() error
	if opt.output != "" {
		file, err := os.Create(opt.output)
		if err != nil {
			return fmt.Errorf("create output: %w", err)
		}
		w = file
		closeOutput = file.Close
	}
	if closeOutput != nil {
		defer func() { _ = closeOutput() }()
	}
	switch opt.format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return fmt.Errorf("write JSON: %w", err)
		}
	case "table":
		if err := writeTable(w, report); err != nil {
			return fmt.Errorf("write table: %w", err)
		}
	default:
		return fmt.Errorf("format must be table or json, got %q", opt.format)
	}
	return nil
}

func validateOptions(opt options) error {
	if opt.reference == "" {
		return errors.New("--reference is required")
	}
	if opt.preset == "" {
		return errors.New("--preset is required")
	}
	if opt.note < 0 || opt.note > 127 {
		return errors.New("--note must be in [0,127]")
	}
	if opt.velocity < 1 || opt.velocity > 127 {
		return errors.New("--velocity must be in [1,127]")
	}
	if opt.sampleRate < 8000 {
		return errors.New("--sample-rate must be >= 8000")
	}
	if opt.duration <= 0 {
		return errors.New("--duration must be > 0")
	}
	if opt.releaseAfter < 0 || opt.releaseAfter >= opt.duration {
		return errors.New("--release-after must be >= 0 and less than --duration")
	}
	if opt.modalGain <= 0 || opt.modalLossScale <= 0 {
		return errors.New("--modal-gain and --modal-loss-scale must be > 0")
	}
	if opt.modalDuration <= 0 || opt.modalFadeOut < 0 || opt.modalFadeOut > opt.modalDuration {
		return errors.New("--modal-duration must be > 0 and --modal-fade-out must be in [0, modal-duration]")
	}
	if opt.format != "table" && opt.format != "json" {
		return fmt.Errorf("--format must be table or json, got %q", opt.format)
	}
	return nil
}

func selectSource(opt options) (bodySource, error) {
	count := 0
	if opt.bodyWAV != "" {
		count++
	}
	if opt.bodyTransfer != "" {
		count++
	}
	if opt.analytical {
		count++
	}
	if count != 1 {
		return bodySource{}, errors.New("select exactly one body source: --body-wav, --body-transfer, or --analytical-default")
	}
	switch {
	case opt.bodyWAV != "":
		return bodySource{name: "wav:" + opt.bodyWAV, path: opt.bodyWAV}, nil
	case opt.bodyTransfer != "":
		return bodySource{name: "modal-transfer:" + opt.bodyTransfer, path: opt.bodyTransfer}, nil
	default:
		return bodySource{name: "analytical-default"}, nil
	}
}

func loadSelectedIR(source bodySource, opt options) ([]float32, error) {
	switch {
	case opt.bodyWAV != "":
		data, rate, err := fitcommon.ReadWAVMono(source.path)
		if err != nil {
			return nil, fmt.Errorf("read body WAV: %w", err)
		}
		data, err = fitcommon.ResampleIfNeeded(data, rate, opt.sampleRate)
		if err != nil {
			return nil, fmt.Errorf("resample body WAV: %w", err)
		}
		out := make([]float32, len(data))
		for i, v := range data {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return nil, fmt.Errorf("body WAV contains non-finite sample at %d", i)
			}
			out[i] = float32(v)
		}
		return out, nil
	case opt.analytical:
		cfg := irsynth.DefaultBodyConfig()
		cfg.SampleRate = opt.sampleRate
		ir, err := irsynth.GenerateBody(cfg)
		if err != nil {
			return nil, fmt.Errorf("generate analytical body: %w", err)
		}
		return ir, nil
	default:
		return generateModalIR(source.path, opt)
	}
}

// generateModalIR is kept separate from source selection so the command's
// validation and WAV/analytical controls remain independently testable.
func generateModalIR(path string, opt options) ([]float32, error) {
	transfer, err := irsynth.LoadBodyModalTransfer(path)
	if err != nil {
		return nil, fmt.Errorf("load body transfer: %w", err)
	}
	cfg := irsynth.DefaultModalBodyConfig()
	cfg.SampleRate = opt.sampleRate
	cfg.DurationS = opt.modalDuration
	cfg.FadeOutS = opt.modalFadeOut
	cfg.TransferGain = opt.modalGain
	cfg.LossScale = opt.modalLossScale
	ir, err := irsynth.GenerateModalBody(transfer, cfg)
	if err != nil {
		return nil, fmt.Errorf("generate modal body: %w", err)
	}
	return ir, nil
}

func render(opt options, bodyIR []float32) ([]float64, error) {
	params, err := preset.LoadJSON(opt.preset)
	if err != nil {
		return nil, err
	}
	// Body-only, fixed-level radiation path. In particular, do not call
	// ApplyDefaultRoomIR: the room stage is the confound this command excludes.
	params.IRWavPath = ""
	params.BodyIRWavPath = ""
	params.RoomIRWavPath = ""
	params.IRDryMix = 1
	params.IRWetMix = 0
	params.IRGain = 1
	params.BodyDryMix = 1
	params.BodyIRGain = 1
	params.RoomWetMix = 0
	params.RoomGain = 1

	p := piano.NewPiano(opt.sampleRate, 16, params)
	p.SetBodyIR(bodyIR)
	p.SetIRMix(1, 1, 0, 1)
	p.NoteOn(opt.note, opt.velocity)

	totalFrames := int(math.Round(opt.duration * float64(opt.sampleRate)))
	releaseFrame := int(math.Round(opt.releaseAfter * float64(opt.sampleRate)))
	const blockSize = 128
	out := make([]float64, 0, totalFrames)
	frames := 0
	released := false
	for frames < totalFrames {
		if !released && frames == releaseFrame {
			p.NoteOff(opt.note)
			released = true
		}
		n := min(blockSize, totalFrames-frames)
		if !released && frames < releaseFrame && frames+n > releaseFrame {
			n = releaseFrame - frames
		}
		stereo := p.Process(n)
		for i := 0; i < n; i++ {
			v := 0.5 * (float64(stereo[2*i]) + float64(stereo[2*i+1]))
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return nil, fmt.Errorf("non-finite output at frame %d", frames+i)
			}
			out = append(out, v)
		}
		frames += n
	}
	return out, nil
}

func writeTable(w io.Writer, report bodyiraudit.Report) error {
	table := fmt.Sprintf("Body IR attribution (%s)\n", report.Source) +
		"metric                         identity      selected         delta\n" +
		fmt.Sprintf("fixed peak                  %12.6f  %12.6f  %+12.3f dB\n", report.FixedGain.Identity.Peak, report.FixedGain.Selected.Peak, report.Delta.PeakDB) +
		fmt.Sprintf("fixed RMS                   %12.6f  %12.6f  %+12.3f dB\n", report.FixedGain.Identity.RMS, report.FixedGain.Selected.RMS, report.Delta.RMSDB) +
		fmt.Sprintf("fixed |RMS gap|             %12.3f  %12.3f  %+12.3f dB\n", math.Abs(report.FixedGain.Identity.RMSGapDB), math.Abs(report.FixedGain.Selected.RMSGapDB), report.Delta.AbsRMSGapDB) +
		fmt.Sprintf("equal-RMS score             %12.6f  %12.6f  %+12.6f\n", report.EqualRMS.Identity.Score, report.EqualRMS.Selected.Score, report.Delta.Score) +
		fmt.Sprintf("equal-RMS spectral overall  %12.3f  %12.3f  %+12.3f dB\n", report.EqualRMS.Identity.SpectralDB, report.EqualRMS.Selected.SpectralDB, report.Delta.SpectralDB) +
		fmt.Sprintf("equal-RMS spectral low      %12.3f  %12.3f  %+12.3f dB\n", report.EqualRMS.Identity.SpectralLow, report.EqualRMS.Selected.SpectralLow, report.Delta.SpectralLow) +
		fmt.Sprintf("equal-RMS spectral mid      %12.3f  %12.3f  %+12.3f dB\n", report.EqualRMS.Identity.SpectralMid, report.EqualRMS.Selected.SpectralMid, report.Delta.SpectralMid) +
		fmt.Sprintf("equal-RMS spectral high     %12.3f  %12.3f  %+12.3f dB\n", report.EqualRMS.Identity.SpectralHigh, report.EqualRMS.Selected.SpectralHigh, report.Delta.SpectralHigh) +
		fmt.Sprintf("IR peak/DC/L2: %.6f / %.6f / %.6f\n", report.IR.Peak, report.IR.DCGain, report.IR.L2Gain) +
		fmt.Sprintf("IR band gain dB (all/low/mid/high): %.3f / %.3f / %.3f / %.3f\n", report.IR.BandGains.Overall, report.IR.BandGains.Low, report.IR.BandGains.Mid, report.IR.BandGains.High)
	for i, selected := range report.FixedGain.Selected.SpectralGaps {
		identity := report.FixedGain.Identity.SpectralGaps[i]
		table += fmt.Sprintf("%s fixed spectral gap dB (identity -> selected, all/low/mid/high): %.3f/%.3f/%.3f/%.3f -> %.3f/%.3f/%.3f/%.3f\n",
			selected.Name,
			identity.GapDB.Overall, identity.GapDB.Low, identity.GapDB.Mid, identity.GapDB.High,
			selected.GapDB.Overall, selected.GapDB.Low, selected.GapDB.Mid, selected.GapDB.High)
	}
	table += fmt.Sprintf("classification: %s — %s\n", report.Attribution.Classification, report.Attribution.Explanation)
	_, err := io.WriteString(w, table)
	return err
}
