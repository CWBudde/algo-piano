//go:build js && wasm

package main

import (
	"strings"
	"syscall/js"
	"unsafe"

	"github.com/cwbudde/algo-piano/piano"
)

var (
	globalPiano  *piano.Piano
	outputBuffer []float32
)

// Provisional modal profile from initial DWG->modal calibration run (notes 36,48,60,72,84).
const (
	webModalPartials     = 2
	webModalGainExponent = float32(0.4)
	webModalExcitation   = float32(2.7738688)
	webModalUndampedLoss = float32(0.4)
	webModalDampedLoss   = float32(0.9669802)
	webMinNote           = 60
	webMaxNote           = 83
)

func main() {
	// Keep program running
	c := make(chan struct{})

	// Export functions to JavaScript
	js.Global().Set("wasmInit", js.FuncOf(wasmInit))
	js.Global().Set("wasmNoteOn", js.FuncOf(wasmNoteOn))
	js.Global().Set("wasmKeyDown", js.FuncOf(wasmKeyDown))
	js.Global().Set("wasmNoteOff", js.FuncOf(wasmNoteOff))
	js.Global().Set("wasmSetSustain", js.FuncOf(wasmSetSustain))
	js.Global().Set("wasmSetCouplingMode", js.FuncOf(wasmSetCouplingMode))
	js.Global().Set("wasmSetStringModel", js.FuncOf(wasmSetStringModel))
	js.Global().Set("wasmLoadIR", js.FuncOf(wasmLoadIR))
	js.Global().Set("wasmSetIRMix", js.FuncOf(wasmSetIRMix))
	js.Global().Set("wasmProcessBlock", js.FuncOf(wasmProcessBlock))
	js.Global().Set("wasmGetMemoryBuffer", js.FuncOf(wasmGetMemoryBuffer))

	println("WASM piano module loaded")
	<-c
}

func wasmInit(this js.Value, args []js.Value) interface{} {
	if len(args) < 1 {
		return nil
	}
	sampleRate := args[0].Int()

	params := piano.NewDefaultParams()
	params.ModalPartials = webModalPartials
	params.ModalGainExponent = webModalGainExponent
	params.ModalExcitation = webModalExcitation
	params.ModalUndampedLoss = webModalUndampedLoss
	params.ModalDampedLoss = webModalDampedLoss
	params.MinNote = webMinNote
	params.MaxNote = webMaxNote
	globalPiano = piano.NewPiano(sampleRate, 16, params)

	// Pre-allocate output buffer for 128 stereo frames
	outputBuffer = make([]float32, 128*2)

	println("Piano initialized at", sampleRate, "Hz")
	return nil
}

func wasmNoteOn(this js.Value, args []js.Value) interface{} {
	if len(args) < 2 || globalPiano == nil {
		return nil
	}
	note := args[0].Int()
	velocity := args[1].Int()
	globalPiano.NoteOn(note, velocity)
	return nil
}

func wasmKeyDown(this js.Value, args []js.Value) interface{} {
	if len(args) < 1 || globalPiano == nil {
		return nil
	}
	note := args[0].Int()
	globalPiano.KeyDown(note)
	return nil
}

func wasmNoteOff(this js.Value, args []js.Value) interface{} {
	if len(args) < 1 || globalPiano == nil {
		return nil
	}
	note := args[0].Int()
	globalPiano.NoteOff(note)
	return nil
}

func wasmSetSustain(this js.Value, args []js.Value) interface{} {
	if len(args) < 1 || globalPiano == nil {
		return nil
	}
	down := args[0].Bool()
	globalPiano.SetSustainPedal(down)
	return nil
}

func wasmSetCouplingMode(this js.Value, args []js.Value) interface{} {
	if len(args) < 1 || globalPiano == nil {
		return false
	}
	modeRaw := strings.TrimSpace(strings.ToLower(args[0].String()))
	mode := piano.CouplingMode(modeRaw)
	return globalPiano.SetCouplingMode(mode)
}

func wasmSetStringModel(this js.Value, args []js.Value) interface{} {
	if len(args) < 1 || globalPiano == nil {
		return false
	}
	modelRaw := strings.TrimSpace(strings.ToLower(args[0].String()))
	model := piano.StringModel(modelRaw)
	return globalPiano.SetStringModel(model)
}

// wasmLoadIR applies an impulse response at runtime.
//
// Usage: wasmLoadIR(kind, buffer) with kind "body" or "room". The legacy
// single-argument form wasmLoadIR(buffer) is still accepted and targets the
// room slot. Returns true on success.
func wasmLoadIR(this js.Value, args []js.Value) interface{} {
	if len(args) < 1 || globalPiano == nil {
		return false
	}

	kind := "room"
	src := args[0]
	if len(args) >= 2 {
		kind = strings.TrimSpace(strings.ToLower(args[0].String()))
		src = args[1]
	}
	if kind != "body" && kind != "room" {
		println("unknown IR kind:", kind)
		return false
	}

	irData, ok := irBytesFromJS(src)
	if !ok {
		return false
	}

	var err error
	if kind == "body" {
		err = globalPiano.SetBodyIRFromBytes(irData)
	} else {
		err = globalPiano.SetRoomIRFromBytes(irData)
	}
	if err != nil {
		println("failed to apply", kind, "IR:", err.Error())
		return false
	}

	println("applied", kind, "IR:", len(irData), "bytes")
	return true
}

// irBytesFromJS copies an ArrayBuffer/Uint8Array into a Go byte slice.
func irBytesFromJS(src js.Value) ([]byte, bool) {
	uint8Array := js.Global().Get("Uint8Array")
	arrayBuffer := js.Global().Get("ArrayBuffer")

	if src.InstanceOf(arrayBuffer) {
		src = uint8Array.New(src)
	}
	if !src.InstanceOf(uint8Array) {
		println("IR data is not an ArrayBuffer/Uint8Array")
		return nil, false
	}

	length := src.Get("byteLength").Int()
	if length == 0 {
		println("IR data is empty")
		return nil, false
	}

	irData := make([]byte, length)
	copied := js.CopyBytesToGo(irData, src)
	if copied != length {
		println("IR copy mismatch:", copied, "of", length)
		return nil, false
	}
	return irData, true
}

// wasmSetIRMix sets the dual-IR mix: (bodyDry, bodyGain, roomWet, roomGain).
func wasmSetIRMix(this js.Value, args []js.Value) interface{} {
	if len(args) < 4 || globalPiano == nil {
		return false
	}
	globalPiano.SetIRMix(
		float32(args[0].Float()),
		float32(args[1].Float()),
		float32(args[2].Float()),
		float32(args[3].Float()),
	)
	return true
}

func wasmProcessBlock(this js.Value, args []js.Value) interface{} {
	if len(args) < 1 || globalPiano == nil {
		return 0
	}

	numFrames := args[0].Int()
	if numFrames > 128 {
		numFrames = 128
	}

	// Process audio
	output := globalPiano.Process(numFrames)

	// Copy to persistent buffer
	copy(outputBuffer, output)

	// Return pointer to buffer in WASM linear memory
	ptr := &outputBuffer[0]
	return float64(uintptr(unsafe.Pointer(ptr)))
}

func wasmGetMemoryBuffer(this js.Value, args []js.Value) interface{} {
	mem := js.Global().Get("__algoPianoWasmMemory")
	if !mem.Truthy() {
		return js.Null()
	}
	return mem.Get("buffer")
}
