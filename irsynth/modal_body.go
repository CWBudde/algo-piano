package irsynth

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
)

const (
	// BodyModalTransferSchemaVersion is the current body modal transfer schema.
	BodyModalTransferSchemaVersion = 1
	// BodyModalTransferKind identifies the force-to-velocity transfer rendered
	// by GenerateModalBody.
	BodyModalTransferKind       = "bridge_force_to_area_velocity"
	BodyModalTransferInputUnit  = "N*s"
	BodyModalTransferOutputUnit = "m/s"
)

// BodyTransferMode is one mass-normalized structural mode projected from the
// bridge-force input to the area-averaged normal-velocity output. Residue is
// signed: changing its sign changes the phase of that mode by pi.
type BodyTransferMode struct {
	FrequencyHz float64 `json:"frequency_hz"`
	LossFactor  float64 `json:"loss_factor"`
	Residue     float64 `json:"residue"`
}

// BodyModalTransfer is the stable JSON boundary between the offline structural
// solver and the real-time piano repository.
type BodyModalTransfer struct {
	SchemaVersion int                `json:"schema_version"`
	TransferKind  string             `json:"transfer_kind"`
	ModelSHA256   string             `json:"model_sha256"`
	InputUnit     string             `json:"input_unit"`
	OutputUnit    string             `json:"output_unit"`
	SourceID      string             `json:"source_id"`
	Modes         []BodyTransferMode `json:"modes"`
}

// Validate rejects artifacts whose convention is ambiguous or whose modal
// data cannot produce a finite causal response.
func (t *BodyModalTransfer) Validate() error {
	if t == nil {
		return errors.New("nil body modal transfer")
	}
	if t.SchemaVersion != BodyModalTransferSchemaVersion {
		return fmt.Errorf("unsupported schema_version %d (want %d)", t.SchemaVersion, BodyModalTransferSchemaVersion)
	}
	if t.TransferKind != BodyModalTransferKind {
		return fmt.Errorf("unsupported transfer_kind %q (want %q)", t.TransferKind, BodyModalTransferKind)
	}
	if t.InputUnit != BodyModalTransferInputUnit {
		return fmt.Errorf("unsupported input_unit %q (want %q)", t.InputUnit, BodyModalTransferInputUnit)
	}
	if t.OutputUnit != BodyModalTransferOutputUnit {
		return fmt.Errorf("unsupported output_unit %q (want %q)", t.OutputUnit, BodyModalTransferOutputUnit)
	}
	if strings.TrimSpace(t.SourceID) == "" {
		return errors.New("source_id must not be empty")
	}
	if len(t.ModelSHA256) != 64 || strings.ToLower(t.ModelSHA256) != t.ModelSHA256 {
		return errors.New("model_sha256 must be 64 lowercase hexadecimal characters")
	}
	if _, err := hex.DecodeString(t.ModelSHA256); err != nil {
		return fmt.Errorf("model_sha256 must be lowercase hexadecimal: %w", err)
	}
	if len(t.Modes) == 0 {
		return errors.New("modes must not be empty")
	}
	lastFrequency := 0.0
	for i, mode := range t.Modes {
		if !finite(mode.FrequencyHz) || mode.FrequencyHz <= 0 {
			return fmt.Errorf("modes[%d].frequency_hz must be finite and > 0", i)
		}
		if mode.FrequencyHz <= lastFrequency {
			return fmt.Errorf("modes[%d].frequency_hz must be strictly increasing", i)
		}
		if !finite(mode.LossFactor) || mode.LossFactor < 0 {
			return fmt.Errorf("modes[%d].loss_factor must be finite and >= 0", i)
		}
		if !finite(mode.Residue) {
			return fmt.Errorf("modes[%d].residue must be finite", i)
		}
		lastFrequency = mode.FrequencyHz
	}
	return nil
}

// DecodeBodyModalTransfer strictly decodes one artifact. Unknown fields and
// trailing JSON are errors so schema drift cannot silently change acoustics.
func DecodeBodyModalTransfer(r io.Reader) (*BodyModalTransfer, error) {
	if r == nil {
		return nil, errors.New("nil body modal transfer reader")
	}
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var transfer BodyModalTransfer
	if err := dec.Decode(&transfer); err != nil {
		return nil, fmt.Errorf("decode body modal transfer: %w", err)
	}
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, errors.New("decode body modal transfer: trailing JSON value")
		}
		return nil, fmt.Errorf("decode body modal transfer: %w", err)
	}
	if err := transfer.Validate(); err != nil {
		return nil, fmt.Errorf("validate body modal transfer: %w", err)
	}
	return &transfer, nil
}

// ParseBodyModalTransfer strictly decodes an artifact from memory.
func ParseBodyModalTransfer(data []byte) (*BodyModalTransfer, error) {
	return DecodeBodyModalTransfer(bytes.NewReader(data))
}

// LoadBodyModalTransfer strictly loads and validates an artifact from disk.
func LoadBodyModalTransfer(path string) (*BodyModalTransfer, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open body modal transfer: %w", err)
	}
	transfer, decodeErr := DecodeBodyModalTransfer(f)
	closeErr := f.Close()
	if decodeErr != nil {
		return nil, fmt.Errorf("load %s: %w", path, decodeErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close body modal transfer %s: %w", path, closeErr)
	}
	return transfer, nil
}

// ModalBodyConfig controls rendering of an offline structural transfer. No
// normalization or stochastic shaping is applied; TransferGain is the exact
// scalar multiplying the physical force-to-velocity response.
type ModalBodyConfig struct {
	SampleRate   int
	DurationS    float64
	FadeOutS     float64
	LossScale    float64
	TransferGain float64
}

// DefaultModalBodyConfig returns explicit, neutral transfer controls.
func DefaultModalBodyConfig() ModalBodyConfig {
	return ModalBodyConfig{
		SampleRate:   96000,
		DurationS:    0.3,
		FadeOutS:     0.01,
		LossScale:    1,
		TransferGain: 1,
	}
}

func (c *ModalBodyConfig) Validate() error {
	if c == nil {
		return errors.New("nil modal body config")
	}
	if c.SampleRate < 8000 {
		return fmt.Errorf("sample rate too low: %d", c.SampleRate)
	}
	if !finite(c.DurationS) || c.DurationS <= 0 {
		return errors.New("duration must be finite and > 0")
	}
	if !finite(c.FadeOutS) || c.FadeOutS < 0 {
		return errors.New("fade-out must be finite and >= 0")
	}
	if !finite(c.LossScale) || c.LossScale < 0 {
		return errors.New("loss scale must be finite and >= 0")
	}
	if !finite(c.TransferGain) {
		return errors.New("transfer gain must be finite")
	}
	return nil
}

// GenerateModalBody renders the causal velocity response to a unit force
// impulse. With mass-normalized modes, each mode starts at its signed residue.
// The structural loss factor eta is converted to damping ratio zeta=eta/2.
func GenerateModalBody(transfer *BodyModalTransfer, cfg ModalBodyConfig) ([]float32, error) {
	if err := transfer.Validate(); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	n := int(math.Round(cfg.DurationS * float64(cfg.SampleRate)))
	if n < 1 {
		n = 1
	}
	response := make([]float64, n)
	dt := 1 / float64(cfg.SampleRate)
	for _, mode := range transfer.Modes {
		// Modes above Nyquist cannot be represented by this sampled artifact.
		// Rejecting is safer than silently aliasing an offline solve.
		if mode.FrequencyHz >= float64(cfg.SampleRate)/2 {
			return nil, fmt.Errorf("mode frequency %.9g Hz is at or above Nyquist %.9g Hz", mode.FrequencyHz, float64(cfg.SampleRate)/2)
		}
		omega := 2 * math.Pi * mode.FrequencyHz
		zeta := mode.LossFactor * cfg.LossScale / 2
		for i := range response {
			t := float64(i) * dt
			response[i] += cfg.TransferGain * mode.Residue * modalVelocityImpulse(omega, zeta, t)
		}
	}
	applyFadeOut(response, cfg.FadeOutS, cfg.SampleRate)
	out := make([]float32, n)
	for i, sample := range response {
		if !finite(sample) || math.Abs(sample) > math.MaxFloat32 {
			return nil, fmt.Errorf("non-finite modal response at sample %d", i)
		}
		out[i] = float32(sample)
	}
	return out, nil
}

func modalVelocityImpulse(omega, zeta, t float64) float64 {
	a := zeta * omega
	switch {
	case zeta < 1:
		wd := omega * math.Sqrt(1-zeta*zeta)
		return math.Exp(-a*t) * (math.Cos(wd*t) - (a/wd)*math.Sin(wd*t))
	case zeta == 1:
		return math.Exp(-omega*t) * (1 - omega*t)
	default:
		d := omega * math.Sqrt(zeta*zeta-1)
		r1, r2 := -a+d, -a-d
		return (r1*math.Exp(r1*t) - r2*math.Exp(r2*t)) / (r1 - r2)
	}
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
