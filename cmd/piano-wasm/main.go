//go:build js && wasm

package main

import (
	"os"
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
	js.Global().Set("wasmNoteOff", js.FuncOf(wasmNoteOff))
	js.Global().Set("wasmSetSustain", js.FuncOf(wasmSetSustain))
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

func wasmLoadIR(this js.Value, args []js.Value) interface{} {
	if len(args) < 1 || globalPiano == nil {
		return nil
	}

	// Get ArrayBuffer from JavaScript
	arrayBuffer := args[0]
	length := arrayBuffer.Get("byteLength").Int()

	if length == 0 {
		println("IR data is empty")
		return nil
	}

	// Copy data from JS to Go
	irData := make([]byte, length)
	js.CopyBytesToGo(irData, arrayBuffer)

	// Write to temporary file
	tmpFile := "/tmp/ir.wav"
	err := os.WriteFile(tmpFile, irData, 0644)
	if err != nil {
		println("Failed to write IR file:", err.Error())
		return nil
	}

	println("IR loaded successfully:", length, "bytes")
	// TODO: Need Piano method to reload IR at runtime
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
	return js.ValueOf(uintptr(unsafe.Pointer(ptr)))
}

func wasmGetMemoryBuffer(this js.Value, args []js.Value) interface{} {
	// Return WASM memory buffer for access from JS
	return js.Global().Get("Go").Get("_inst").Get("exports").Get("mem").Get("buffer")
}
