// Main thread JavaScript - loads WASM and sets up AudioWorklet

let audioContext = null;
let pianoWorkletNode = null;
let wasmReady = false;
let audioReady = false;

async function init() {
    try {
        // Load WASM
        const go = new Go();
        const result = await WebAssembly.instantiateStreaming(
            fetch('dist/piano.wasm'),
            go.importObject
        );
        go.run(result.instance);

        // Wait a bit for WASM exports to be set
        await new Promise(resolve => setTimeout(resolve, 100));

        if (typeof wasmInit === 'undefined') {
            throw new Error('WASM exports not found');
        }

        wasmReady = true;
        updateStatus('WASM loaded. Click any key to start audio.');

        // Generate piano keyboard
        generateKeyboard();

    } catch (error) {
        console.error('Failed to load WASM:', error);
        updateStatus('Error: ' + error.message);
    }
}

function updateStatus(message) {
    const loading = document.getElementById('loading');
    const ready = document.getElementById('ready');
    const info = document.getElementById('info');

    loading.style.display = 'none';
    ready.style.display = 'none';
    info.style.display = 'none';

    if (message.startsWith('Error:')) {
        info.textContent = message;
        info.style.display = 'block';
        info.style.color = '#ff6b6b';
    } else if (message.includes('Click')) {
        ready.textContent = message;
        ready.style.display = 'block';
    } else {
        info.textContent = message;
        info.style.display = 'block';
    }
}

function generateKeyboard() {
    const keyboard = document.getElementById('piano-keyboard');

    // Generate 2 octaves starting from C4 (MIDI note 60)
    const startNote = 60; // C4
    const numOctaves = 2;

    const whiteKeyPattern = [0, 2, 4, 5, 7, 9, 11]; // C D E F G A B
    const blackKeyOffsets = [1, 3, 6, 8, 10]; // C# D# F# G# A#
    const computerKeys = ['A', 'W', 'S', 'E', 'D', 'F', 'T', 'G', 'Y', 'H', 'U', 'J', 'K'];

    let whiteKeyIndex = 0;

    // Generate white keys first
    for (let octave = 0; octave < numOctaves; octave++) {
        for (let i = 0; i < whiteKeyPattern.length; i++) {
            const noteOffset = whiteKeyPattern[i];
            const midiNote = startNote + octave * 12 + noteOffset;
            const keyIndex = octave * 12 + noteOffset;

            const key = document.createElement('div');
            key.className = 'key white';
            key.dataset.note = midiNote;
            key.style.left = `${whiteKeyIndex * 52}px`;

            const label = document.createElement('div');
            label.className = 'key-label';
            if (keyIndex < computerKeys.length) {
                label.textContent = computerKeys[keyIndex];
            }
            key.appendChild(label);

            keyboard.appendChild(key);
            whiteKeyIndex++;
        }
    }

    // Generate black keys on top
    whiteKeyIndex = 0;
    for (let octave = 0; octave < numOctaves; octave++) {
        for (let i = 0; i < 12; i++) {
            if (blackKeyOffsets.includes(i)) {
                const midiNote = startNote + octave * 12 + i;
                const keyIndex = octave * 12 + i;

                const key = document.createElement('div');
                key.className = 'key black';
                key.dataset.note = midiNote;

                // Position black keys between white keys
                const whiteKeysBefore = whiteKeyPattern.filter(n => n < i).length;
                key.style.left = `${whiteKeysBefore * 52 + 36 + octave * 7 * 52}px`;

                const label = document.createElement('div');
                label.className = 'key-label';
                if (keyIndex < computerKeys.length) {
                    label.textContent = computerKeys[keyIndex];
                }
                key.appendChild(label);

                keyboard.appendChild(key);
            }
        }
    }

    // Add event listeners
    attachKeyboardListeners();
}

function attachKeyboardListeners() {
    const keys = document.querySelectorAll('.key');

    keys.forEach(key => {
        // Mouse events
        key.addEventListener('mousedown', (e) => {
            e.preventDefault();
            handleNoteOn(parseInt(key.dataset.note));
            key.classList.add('active');
        });

        key.addEventListener('mouseup', (e) => {
            e.preventDefault();
            handleNoteOff(parseInt(key.dataset.note));
            key.classList.remove('active');
        });

        key.addEventListener('mouseleave', (e) => {
            if (key.classList.contains('active')) {
                handleNoteOff(parseInt(key.dataset.note));
                key.classList.remove('active');
            }
        });

        // Touch events
        key.addEventListener('touchstart', (e) => {
            e.preventDefault();
            handleNoteOn(parseInt(key.dataset.note));
            key.classList.add('active');
        });

        key.addEventListener('touchend', (e) => {
            e.preventDefault();
            handleNoteOff(parseInt(key.dataset.note));
            key.classList.remove('active');
        });
    });

    // Sustain pedal
    const sustainButton = document.getElementById('sustain-pedal');
    let sustainDown = false;

    sustainButton.addEventListener('click', () => {
        sustainDown = !sustainDown;
        sustainButton.classList.toggle('active', sustainDown);
        sustainButton.textContent = `Sustain Pedal (Space): ${sustainDown ? 'ON' : 'OFF'}`;
        handleSustain(sustainDown);
    });

    // Computer keyboard
    const keyMap = buildKeyMap();
    let pressedKeys = new Set();

    document.addEventListener('keydown', (e) => {
        if (e.repeat) return;

        if (e.code === 'Space') {
            e.preventDefault();
            sustainButton.click();
            return;
        }

        const note = keyMap.get(e.key.toUpperCase());
        if (note !== undefined && !pressedKeys.has(e.key)) {
            pressedKeys.add(e.key);
            handleNoteOn(note);

            const keyElement = document.querySelector(`[data-note="${note}"]`);
            if (keyElement) keyElement.classList.add('active');
        }
    });

    document.addEventListener('keyup', (e) => {
        if (e.code === 'Space') {
            return;
        }

        const note = keyMap.get(e.key.toUpperCase());
        if (note !== undefined) {
            pressedKeys.delete(e.key);
            handleNoteOff(note);

            const keyElement = document.querySelector(`[data-note="${note}"]`);
            if (keyElement) keyElement.classList.remove('active');
        }
    });
}

function buildKeyMap() {
    const map = new Map();
    const keys = ['A', 'W', 'S', 'E', 'D', 'F', 'T', 'G', 'Y', 'H', 'U', 'J', 'K'];
    const startNote = 60;

    keys.forEach((key, index) => {
        map.set(key, startNote + index);
    });

    return map;
}

async function initAudio() {
    if (audioReady) return;

    try {
        audioContext = new (window.AudioContext || window.webkitAudioContext)();

        // Initialize WASM with sample rate
        wasmInit(audioContext.sampleRate);

        // Load AudioWorklet
        await audioContext.audioWorklet.addModule('piano-worklet.js');

        // Create worklet node
        pianoWorkletNode = new AudioWorkletNode(
            audioContext,
            'piano-worklet-processor',
            {
                numberOfInputs: 0,
                numberOfOutputs: 1,
                outputChannelCount: [2]
            }
        );

        // Send WASM memory buffer to worklet
        const memoryBuffer = wasmGetMemoryBuffer();
        pianoWorkletNode.port.postMessage({
            type: 'memoryBuffer',
            data: { buffer: memoryBuffer }
        });

        pianoWorkletNode.connect(audioContext.destination);

        audioReady = true;
        updateStatus(`Ready! Sample rate: ${audioContext.sampleRate} Hz`);

        // Try to load IR
        loadIR();

    } catch (error) {
        console.error('Failed to initialize audio:', error);
        updateStatus('Error initializing audio: ' + error.message);
    }
}

function handleNoteOn(note) {
    if (!audioReady) {
        initAudio();
        return;
    }

    if (pianoWorkletNode) {
        pianoWorkletNode.port.postMessage({
            type: 'noteOn',
            data: { note, velocity: 80 }
        });
    }
}

function handleNoteOff(note) {
    if (!audioReady) return;

    if (pianoWorkletNode) {
        pianoWorkletNode.port.postMessage({
            type: 'noteOff',
            data: { note }
        });
    }
}

function handleSustain(down) {
    if (!audioReady) return;

    if (pianoWorkletNode) {
        pianoWorkletNode.port.postMessage({
            type: 'sustain',
            data: { down }
        });
    }
}

async function loadIR() {
    try {
        const response = await fetch('dist/assets/ir/default_96k.wav');
        if (!response.ok) {
            console.warn('IR not found, continuing without convolution');
            return;
        }

        const arrayBuffer = await response.arrayBuffer();
        wasmLoadIR(arrayBuffer);

        console.log('IR loaded successfully');
    } catch (error) {
        console.warn('Failed to load IR:', error);
    }
}

// Initialize on load
window.addEventListener('load', init);
