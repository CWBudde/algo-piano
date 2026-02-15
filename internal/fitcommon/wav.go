package fitcommon

import (
	"fmt"
	"math"
	"os"
	"path/filepath"

	dspresample "github.com/cwbudde/algo-dsp/dsp/resample"
	"github.com/cwbudde/wav"
	"github.com/go-audio/audio"
)

func ReadWAVMono(path string) ([]float64, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	dec := wav.NewDecoder(f)
	if !dec.IsValidFile() {
		return nil, 0, fmt.Errorf("invalid wav file: %s", path)
	}
	buf, err := dec.FullPCMBuffer()
	if err != nil {
		return nil, 0, err
	}
	if buf == nil || buf.Format == nil || buf.Format.NumChannels < 1 {
		return nil, 0, fmt.Errorf("invalid wav buffer: %s", path)
	}
	ch := buf.Format.NumChannels
	frames := len(buf.Data) / ch
	out := make([]float64, frames)
	for i := 0; i < frames; i++ {
		var sum float64
		for c := 0; c < ch; c++ {
			sum += float64(buf.Data[i*ch+c])
		}
		out[i] = sum / float64(ch)
	}
	return out, buf.Format.SampleRate, nil
}

func ResampleIfNeeded(in []float64, fromRate int, toRate int) ([]float64, error) {
	if fromRate == toRate {
		return in, nil
	}
	r, err := dspresample.NewForRates(
		float64(fromRate),
		float64(toRate),
		dspresample.WithQuality(dspresample.QualityBest),
	)
	if err != nil {
		return nil, err
	}
	return r.Process(in), nil
}

func WriteStereoWAVLR(path string, left []float32, right []float32, sampleRate int) error {
	if len(left) != len(right) {
		return fmt.Errorf("left/right length mismatch")
	}
	data := make([]float32, len(left)*2)
	for i := 0; i < len(left); i++ {
		data[i*2] = left[i]
		data[i*2+1] = right[i]
	}
	return WriteStereoInterleavedWAV(path, data, sampleRate)
}

func WriteStereoInterleavedWAV(path string, samples []float32, sampleRate int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := wav.NewEncoder(f, sampleRate, 16, 2, 1)
	defer enc.Close()

	buf := &audio.Float32Buffer{
		Format: &audio.Format{
			SampleRate:  sampleRate,
			NumChannels: 2,
		},
		Data:           samples,
		SourceBitDepth: 16,
	}
	return enc.Write(buf)
}

func WriteMonoWAV(path string, data []float32, sampleRate int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := wav.NewEncoder(f, sampleRate, 16, 1, 1)
	defer enc.Close()

	buf := &audio.Float32Buffer{
		Format: &audio.Format{
			SampleRate:  sampleRate,
			NumChannels: 1,
		},
		Data:           data,
		SourceBitDepth: 16,
	}
	return enc.Write(buf)
}

func StereoToMono64(st []float32) []float64 {
	if len(st) < 2 {
		return nil
	}
	n := len(st) / 2
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		out[i] = 0.5 * (float64(st[i*2]) + float64(st[i*2+1]))
	}
	return out
}

func StereoRMS(interleaved []float32) float64 {
	if len(interleaved) == 0 {
		return 0
	}

	var sum float64
	for _, s := range interleaved {
		v := float64(s)
		sum += v * v
	}

	return math.Sqrt(sum / float64(len(interleaved)))
}
