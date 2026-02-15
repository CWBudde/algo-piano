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

func wasmLoadIR(this js.Value, args []js.Value) interface{} {
	if len(args) < 1 || globalPiano == nil {
		return nil
	}

	src := args[0]
	uint8Array := js.Global().Get("Uint8Array")
	arrayBuffer := js.Global().Get("ArrayBuffer")

	if src.InstanceOf(arrayBuffer) {
		src = uint8Array.New(src)
	}
	if !src.InstanceOf(uint8Array) {
		println("IR data is not an ArrayBuffer/Uint8Array")
		return nil
	}

	length := src.Get("byteLength").Int()

	if length == 0 {
		println("IR data is empty")
		return nil
	}

	// Copy data from JS to Go
	irData := make([]byte, length)
	copied := js.CopyBytesToGo(irData, src)
	if copied != length {
		println("IR copy mismatch:", copied, "of", length)
	}

	// TODO: Parse WAV from bytes and apply via SetRoomIR/SetBodyIR at runtime.
	println("IR loaded:", copied, "bytes (runtime IR apply not implemented yet)")
	return nil
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
