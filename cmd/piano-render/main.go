package main

import (
	"flag"
	"fmt"
	"math"
	"os"

	"github.com/cwbudde/algo-piano/piano"
	"github.com/cwbudde/algo-piano/preset"
	"github.com/cwbudde/wav"
	"github.com/go-audio/audio"
)

func main() {
	// Command-line flags
	note := flag.Int("note", 69, "MIDI note number (69 = A4 = 440 Hz)")
	velocity := flag.Int("velocity", 100, "MIDI velocity (0-127)")
	duration := flag.Float64("duration", 2.0, "Duration in seconds")
	decayDBFS := flag.Float64("decay-dbfs", math.Inf(1), "Auto-stop when stereo block RMS falls below this dBFS (e.g. -90). Disabled by default")
	decayHoldBlocks := flag.Int("decay-hold-blocks", 6, "Consecutive below-threshold blocks required to stop in auto-decay mode")
	minDuration := flag.Float64("min-duration", 0.5, "Minimum render duration in seconds when using -decay-dbfs")
	maxDuration := flag.Float64("max-duration", 20.0, "Maximum render duration in seconds when using -decay-dbfs")
	releaseAfter := flag.Float64("release-after", 0.12, "Send NoteOff after this many seconds in auto-decay mode")
	sampleRate := flag.Int("sample-rate", 48000, "Render sample rate in Hz")
	presetPath := flag.String("preset", "assets/presets/default.json", "Preset JSON file path")
	irPath := flag.String("ir", "", "IR WAV path override (optional)")
	output := flag.String("output", "output.wav", "Output WAV file path")
	flag.Parse()

	// Create piano engine
	numChannels := 2 // stereo
	maxPolyphony := 16

	params, err := preset.LoadJSON(*presetPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading preset %q: %v\n", *presetPath, err)
		os.Exit(1)
	}
	if *irPath != "" {
		params.IRWavPath = *irPath
	}
	if params.IRWavPath == "" {
		params.IRWavPath = piano.DefaultIRWavPath
	}

	fmt.Printf("Rendering note %d, velocity %d, for %.2f seconds at %d Hz (preset: %s, IR: %s)...\n", *note, *velocity, *duration, *sampleRate, *presetPath, params.IRWavPath)

	p := piano.NewPiano(*sampleRate, maxPolyphony, params)

	// Trigger note
	p.NoteOn(*note, *velocity)

	blockSize := 128 // process in blocks
	autoStop := !math.IsInf(*decayDBFS, 1)

	var totalFrames int
	if !autoStop {
		totalFrames = int(float64(*sampleRate) * (*duration))
		if totalFrames < 1 {
			totalFrames = 1
		}
	}

	// Allocate output buffer.
	initialFrames := totalFrames
	if autoStop {
		initialFrames = int(float64(*sampleRate) * (*minDuration))
		if initialFrames < blockSize {
			initialFrames = blockSize
		}
	}
	samples := make([]float32, 0, initialFrames*numChannels)

	framesRendered := 0
	if autoStop {
		minFrames := int(float64(*sampleRate) * (*minDuration))
		maxFrames := int(float64(*sampleRate) * (*maxDuration))
		releaseAtFrame := int(float64(*sampleRate) * (*releaseAfter))
		if releaseAtFrame < 0 {
			releaseAtFrame = 0
		}
		if maxFrames < minFrames {
			maxFrames = minFrames
		}
		if maxFrames < 1 {
			maxFrames = blockSize
		}

		thresholdLin := math.Pow(10.0, *decayDBFS/20.0)
		noteReleased := false
		belowCount := 0
		if *decayHoldBlocks < 1 {
			*decayHoldBlocks = 1
		}
		for framesRendered < maxFrames {
			framesToRender := blockSize
			if framesRendered+framesToRender > maxFrames {
				framesToRender = maxFrames - framesRendered
			}

			if !noteReleased && framesRendered >= releaseAtFrame {
				p.NoteOff(*note)
				noteReleased = true
			}

			block := p.Process(framesToRender)
			samples = append(samples, block...)
			framesRendered += framesToRender

			if framesRendered >= minFrames {
				if stereoRMS(block) < thresholdLin {
					belowCount++
					if belowCount >= *decayHoldBlocks {
						break
					}
				} else {
					belowCount = 0
				}
			}
		}
		totalFrames = framesRendered
		fmt.Printf("Auto-stop at %d frames (%.3fs), threshold %.1f dBFS\n", totalFrames, float64(totalFrames)/float64(*sampleRate), *decayDBFS)
	} else {
		for framesRendered < totalFrames {
			framesToRender := blockSize
			if framesRendered+framesToRender > totalFrames {
				framesToRender = totalFrames - framesRendered
			}

			block := p.Process(framesToRender)
			samples = append(samples, block...)
			framesRendered += framesToRender
		}
	}

	// Write to WAV file
	file, err := os.Create(*output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating output file: %v\n", err)
		os.Exit(1)
	}
	defer file.Close()

	// Create encoder with 16-bit PCM (audioFormat = 1)
	encoder := wav.NewEncoder(file, *sampleRate, 16, numChannels, 1)
	defer encoder.Close()

	// Create audio buffer
	buf := &audio.Float32Buffer{
		Format: &audio.Format{
			SampleRate:  *sampleRate,
			NumChannels: numChannels,
		},
		Data:           samples,
		SourceBitDepth: 16,
	}

	// Write the buffer
	if err := encoder.Write(buf); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing WAV file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Successfully wrote %s (%d frames)\n", *output, totalFrames)
}

func stereoRMS(interleaved []float32) float64 {
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
