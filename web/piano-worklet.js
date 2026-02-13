// AudioWorkletProcessor for piano synthesis

class PianoWorkletProcessor extends AudioWorkletProcessor {
    constructor() {
        super();
        this.port.onmessage = this.handleMessage.bind(this);
        this.wasmMemoryBuffer = null;
    }

    handleMessage(event) {
        const { type, data } = event.data;

        switch (type) {
            case 'noteOn':
                if (typeof wasmNoteOn !== 'undefined') {
                    wasmNoteOn(data.note, data.velocity);
                }
                break;
            case 'noteOff':
                if (typeof wasmNoteOff !== 'undefined') {
                    wasmNoteOff(data.note);
                }
                break;
            case 'sustain':
                if (typeof wasmSetSustain !== 'undefined') {
                    wasmSetSustain(data.down);
                }
                break;
            case 'memoryBuffer':
                this.wasmMemoryBuffer = data.buffer;
                break;
        }
    }

    process(inputs, outputs, parameters) {
        const output = outputs[0];

        if (typeof wasmProcessBlock === 'undefined' || !this.wasmMemoryBuffer) {
            return true;
        }

        const numFrames = output[0].length;

        // Call WASM to get audio buffer pointer
        const bufferPtr = wasmProcessBlock(numFrames);

        if (bufferPtr === 0) {
            return true;
        }

        // Create Float32Array view into WASM memory
        const wasmMemory = new Float32Array(
            this.wasmMemoryBuffer,
            bufferPtr,
            numFrames * 2
        );

        // Copy stereo data to output
        for (let i = 0; i < numFrames; i++) {
            output[0][i] = wasmMemory[i * 2];     // Left
            output[1][i] = wasmMemory[i * 2 + 1]; // Right
        }

        return true;
    }
}

registerProcessor('piano-worklet-processor', PianoWorkletProcessor);
