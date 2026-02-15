// Main thread JavaScript - loads WASM and drives audio rendering

let audioContext = null;
let outputNode = null;
let wasmMemory = null;
let wasmMemoryBuffer = null;
let initAudioPromise = null;
const pendingNotes = new Set();
const heldNotes = new Set();
const sustainedNotes = new Set();
const sustainReleaseTimers = new Map();
let wasmReady = false;
let audioReady = false;
let sustainPedalDown = false;
let damperEngaged = true;
let sustainLevel = 50;
let sustainReleaseMs = 1800;
let noteVelocity = 96;
const RENDER_CHUNK_FRAMES = 128;
const SCRIPT_BUFFER_SIZE = 256;

async function init() {
    try {
        // Load WASM
        const go = new Go();
        const result = await WebAssembly.instantiateStreaming(
            fetch('dist/piano.wasm'),
            go.importObject
        );
        wasmMemory = result.instance.exports.mem || result.instance.exports.memory || null;
        if (!wasmMemory) {
            throw new Error('WASM memory export not found');
        }
        window.__algoPianoWasmMemory = wasmMemory;
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
        info.style.color = '';
    }
}

function generateKeyboard() {
    const keyboard = document.getElementById('piano-keyboard');

    // Create a container for the keys
    const keysContainer = document.createElement('div');
    keysContainer.className = 'keys-container';
    keyboard.appendChild(keysContainer);

    // Generate 2 octaves starting from C4 (MIDI note 60)
    const startNote = 60; // C4
    const numOctaves = 2;

    const whiteKeyPattern = [0, 2, 4, 5, 7, 9, 11]; // C D E F G A B
    const blackKeyOffsets = [1, 3, 6, 8, 10]; // C# D# F# G# A#
    const computerKeys = ['A', 'W', 'S', 'E', 'D', 'F', 'T', 'G', 'Y', 'H', 'U', 'J', 'K'];

    const rootStyles = getComputedStyle(document.documentElement);
    const whiteKeyWidth = parseFloat(rootStyles.getPropertyValue('--white-key-width')) || 52;
    const whiteKeyGap = parseFloat(rootStyles.getPropertyValue('--white-key-gap')) || 2;
    const whiteKeyMargin = whiteKeyGap;
    const blackKeyWidth = parseFloat(rootStyles.getPropertyValue('--black-key-width')) || 34;
    const blackKeyFineTune = -2;

    // Generate white keys first
    for (let octave = 0; octave < numOctaves; octave++) {
        for (let i = 0; i < whiteKeyPattern.length; i++) {
            const noteOffset = whiteKeyPattern[i];
            const midiNote = startNote + octave * 12 + noteOffset;
            const keyIndex = octave * 12 + noteOffset;

            const key = document.createElement('div');
            key.className = 'key white';
            key.dataset.note = midiNote;

            const label = document.createElement('div');
            label.className = 'key-label';
            if (keyIndex < computerKeys.length) {
                label.textContent = computerKeys[keyIndex];
            }
            key.appendChild(label);

            keysContainer.appendChild(key);
        }
    }

    // Generate black keys on top
    for (let octave = 0; octave < numOctaves; octave++) {
        for (let i = 0; i < 12; i++) {
            if (blackKeyOffsets.includes(i)) {
                const midiNote = startNote + octave * 12 + i;
                const keyIndex = octave * 12 + i;

                const key = document.createElement('div');
                key.className = 'key black';
                key.dataset.note = midiNote;

                // Position black keys between white keys
                // Each white key takes up (width + 2*margin) space
                const whiteKeysBefore = whiteKeyPattern.filter(n => n < i).length;
                const totalWhiteKeysSpace = (whiteKeyWidth + whiteKeyMargin * 2);
                const leftPos = (whiteKeysBefore + octave * 7) * totalWhiteKeysSpace -
                                blackKeyWidth / 2 +
                                blackKeyFineTune;
                key.style.left = `${leftPos}px`;

                const label = document.createElement('div');
                label.className = 'key-label';
                if (keyIndex < computerKeys.length) {
                    label.textContent = computerKeys[keyIndex];
                }
                key.appendChild(label);

                keysContainer.appendChild(key);
            }
        }
    }

    // Add event listeners
    attachKeyboardListeners();
}

function attachKeyboardListeners() {
    const keys = document.querySelectorAll('.key');
    const sustainButton = document.getElementById('sustain-pedal');
    const sustainState = document.getElementById('sustain-state');
    const damperButton = document.getElementById('damper-toggle');
    const sustainLevelSlider = document.getElementById('sustain-level');
    const sustainLevelValue = document.getElementById('sustain-level-value');

    function updateSliderFill(value) {
        const pct = `${value}%`;
        sustainLevelSlider.style.background = `linear-gradient(90deg, rgba(222, 189, 126, 0.9) 0%, rgba(222, 189, 126, 0.42) ${pct}, rgba(31, 34, 41, 0.85) ${pct}, rgba(31, 34, 41, 0.85) 100%)`;
    }

    function updateSustainReleaseFromLevel(value) {
        const normalized = Math.max(0, Math.min(100, value)) / 100;
        if (normalized >= 0.999) {
            sustainReleaseMs = Infinity;
            return;
        }
        // 0% -> near-immediate release, 100% -> hold until pedal-up.
        sustainReleaseMs = Math.round(30 + Math.pow(normalized, 1.7) * 6000);
    }

    function syncPedalUI() {
        sustainButton.classList.toggle('active', sustainPedalDown);
        sustainButton.setAttribute('aria-pressed', String(sustainPedalDown));
        sustainState.textContent = sustainPedalDown ? 'ON' : 'OFF';

        damperButton.classList.toggle('active', damperEngaged);
        damperButton.setAttribute('aria-pressed', String(damperEngaged));
        damperButton.textContent = damperEngaged ? 'ON' : 'OFF';
    }

    function setSustainState(down) {
        sustainPedalDown = down;
        syncPedalUI();
        handleSustain(down);
    }

    function setDamperState(on) {
        damperEngaged = on;
        syncPedalUI();
    }

    sustainLevel = parseInt(sustainLevelSlider.value, 10) || 50;
    sustainLevelValue.textContent = `${sustainLevel}%`;
    updateSliderFill(sustainLevel);
    updateSustainReleaseFromLevel(sustainLevel);
    syncPedalUI();

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

    sustainButton.addEventListener('click', () => {
        setSustainState(!sustainPedalDown);
    });

    damperButton.addEventListener('click', () => {
        setDamperState(!damperEngaged);
    });

    sustainLevelSlider.addEventListener('input', (event) => {
        sustainLevel = parseInt(event.target.value, 10) || 50;
        sustainLevelValue.textContent = `${sustainLevel}%`;
        updateSliderFill(sustainLevel);
        updateSustainReleaseFromLevel(sustainLevel);

        if (sustainPedalDown) {
            for (const note of sustainedNotes) {
                scheduleSustainRelease(note);
            }
        }
    });

    // Computer keyboard
    const keyMap = buildKeyMap();
    let pressedKeys = new Set();

    document.addEventListener('keydown', (e) => {
        if (e.repeat) return;

        if (e.code === 'Space') {
            e.preventDefault();
            setSustainState(!sustainPedalDown);
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

function clearSustainReleaseTimer(note) {
    const timer = sustainReleaseTimers.get(note);
    if (timer !== undefined) {
        clearTimeout(timer);
        sustainReleaseTimers.delete(note);
    }
}

function releaseNote(note) {
    clearSustainReleaseTimer(note);
    sustainedNotes.delete(note);
    if (!audioReady) return;

    if (typeof wasmNoteOff !== 'undefined') {
        wasmNoteOff(note);
    }
}

function scheduleSustainRelease(note) {
    clearSustainReleaseTimer(note);
    if (!Number.isFinite(sustainReleaseMs)) {
        return;
    }

    const timer = setTimeout(() => {
        sustainReleaseTimers.delete(note);
        if (!sustainPedalDown) {
            return;
        }
        if (heldNotes.has(note)) {
            return;
        }
        releaseNote(note);
    }, sustainReleaseMs);
    sustainReleaseTimers.set(note, timer);
}

function flushSustainedNotes() {
    const notesToRelease = [];
    for (const note of sustainedNotes) {
        if (!heldNotes.has(note)) {
            notesToRelease.push(note);
        } else {
            clearSustainReleaseTimer(note);
            sustainedNotes.delete(note);
        }
    }

    for (const note of notesToRelease) {
        releaseNote(note);
    }
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
    if (initAudioPromise) return initAudioPromise;

    initAudioPromise = (async () => {
        audioContext = new (window.AudioContext || window.webkitAudioContext)();

        // Initialize WASM with sample rate.
        wasmInit(audioContext.sampleRate);
        wasmMemoryBuffer = wasmMemory ? wasmMemory.buffer : null;
        if (!wasmMemoryBuffer) {
            throw new Error('WASM memory buffer unavailable');
        }

        // Match algo-dsp's main-thread rendering model so WASM exports are in scope.
        outputNode = audioContext.createScriptProcessor(SCRIPT_BUFFER_SIZE, 0, 2);
        outputNode.onaudioprocess = (event) => {
            const outputBuffer = event.outputBuffer;
            const left = outputBuffer.getChannelData(0);
            const hasStereo = outputBuffer.numberOfChannels > 1;
            const right = hasStereo ? outputBuffer.getChannelData(1) : null;

            try {
                if (!audioReady || typeof wasmProcessBlock === 'undefined' || !wasmMemory) {
                    left.fill(0);
                    if (right) right.fill(0);
                    return;
                }

                // Refresh in case WASM memory has grown.
                wasmMemoryBuffer = wasmMemory.buffer;
                if (!wasmMemoryBuffer) {
                    left.fill(0);
                    if (right) right.fill(0);
                    return;
                }

                let offset = 0;
                while (offset < left.length) {
                    const frames = Math.min(RENDER_CHUNK_FRAMES, left.length - offset);
                    const bufferPtr = wasmProcessBlock(frames);

                    if (bufferPtr === 0) {
                        left.fill(0, offset);
                        if (right) right.fill(0, offset);
                        break;
                    }

                    const interleaved = new Float32Array(
                        wasmMemoryBuffer,
                        bufferPtr,
                        frames * 2
                    );

                    for (let i = 0; i < frames; i++) {
                        left[offset + i] = interleaved[i * 2];
                        if (right) right[offset + i] = interleaved[i * 2 + 1];
                    }

                    offset += frames;
                }
            } catch (error) {
                console.error('Audio render error:', error);
                left.fill(0);
                if (right) right.fill(0);
            }
        };

        outputNode.connect(audioContext.destination);
        await audioContext.resume();

        audioReady = true;
        if (sustainPedalDown && typeof wasmSetSustain !== 'undefined') {
            wasmSetSustain(sustainPedalDown);
        }
        updateStatus(`Ready! Sample rate: ${audioContext.sampleRate} Hz`);

        // Try to load IR
        loadIR();
    })();

    try {
        await initAudioPromise;
    } catch (error) {
        audioReady = false;
        wasmMemoryBuffer = null;
        if (outputNode) {
            outputNode.disconnect();
            outputNode = null;
        }
        console.error('Failed to initialize audio:', error);
        updateStatus('Error initializing audio: ' + error.message);
        throw error;
    } finally {
        initAudioPromise = null;
    }
}

function handleNoteOn(note) {
    heldNotes.add(note);
    clearSustainReleaseTimer(note);
    sustainedNotes.delete(note);

    if (!audioReady) {
        pendingNotes.add(note);
        initAudio()
            .then(() => {
                if (audioReady && pendingNotes.has(note) && typeof wasmNoteOn !== 'undefined') {
                    wasmNoteOn(note, noteVelocity);
                }
            })
            .catch(() => {
                // initAudio already updates UI with the error details.
            });
        return;
    }

    if (typeof wasmNoteOn !== 'undefined') {
        wasmNoteOn(note, noteVelocity);
    }
}

function handleNoteOff(note) {
    pendingNotes.delete(note);
    heldNotes.delete(note);
    if (!audioReady) return;

    if (sustainPedalDown) {
        sustainedNotes.add(note);
        scheduleSustainRelease(note);
        return;
    }

    releaseNote(note);
}

function handleSustain(down) {
    sustainPedalDown = down;
    if (!down) {
        flushSustainedNotes();
    } else if (Number.isFinite(sustainReleaseMs)) {
        for (const note of sustainedNotes) {
            scheduleSustainRelease(note);
        }
    }

    if (!audioReady) return;

    if (typeof wasmSetSustain !== 'undefined') {
        wasmSetSustain(down);
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
