// Main thread JavaScript - loads WASM and drives audio rendering

let audioContext = null;
let outputNode = null;
let wasmMemory = null;
let wasmMemoryBuffer = null;
let initAudioPromise = null;
const pendingNotes = new Map();
const heldNotes = new Set();
const latchedNotes = new Set();
const latchedReleaseArmed = new Set();
const mousePressedNotes = new Set();
let wasmReady = false;
let audioReady = false;
let sustainPedalDown = false;
let damperEngaged = true;
let sustainLevel = 50;
let masterVolume = 100;
let limiterEnabled = false;
let reverbEnabled = false;
let reverbAmount = 20;
let noteVelocity = 96;
let couplingMode = 'static';
let stringModel = 'dwg';
const velocityCurve = {
    mode: 'power',
    exponent: 1.7,
    floor: 0.0
};
const RENDER_CHUNK_FRAMES = 128;
const SCRIPT_BUFFER_SIZE = 256;

function normalizeVelocityCurveMode(mode) {
    const normalized = String(mode || '').trim().toLowerCase();
    if (normalized === 'linear' || normalized === 'power') {
        return normalized;
    }
    return 'power';
}

function setVelocityCurve(config = {}) {
    if (config.mode !== undefined) {
        velocityCurve.mode = normalizeVelocityCurveMode(config.mode);
    }
    if (config.exponent !== undefined) {
        const exp = Number(config.exponent);
        if (Number.isFinite(exp) && exp > 0) {
            velocityCurve.exponent = exp;
        }
    }
    if (config.floor !== undefined) {
        const floor = Number(config.floor);
        if (Number.isFinite(floor)) {
            velocityCurve.floor = Math.min(0.99, clamp01(floor));
        }
    }
}

function applyVelocityCurveFromURL() {
    const params = new URLSearchParams(window.location.search);
    const mode = params.get('vel_curve');
    const exponent = params.get('vel_exp');
    const floor = params.get('vel_floor');

    const next = {};
    if (mode !== null) next.mode = mode;
    if (exponent !== null) next.exponent = Number(exponent);
    if (floor !== null) next.floor = Number(floor);

    if (Object.keys(next).length > 0) {
        setVelocityCurve(next);
    }
}

async function init() {
    try {
        applyVelocityCurveFromURL();
        window.setPianoVelocityCurve = setVelocityCurve;
        window.getPianoVelocityCurve = () => ({ ...velocityCurve });

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
                                blackKeyWidth / 2;
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

function normalizeCouplingMode(mode) {
    const normalized = String(mode || '').trim().toLowerCase();
    if (normalized === 'off' || normalized === 'static' || normalized === 'physical') {
        return normalized;
    }
    return 'static';
}

function setCouplingMode(mode) {
    couplingMode = normalizeCouplingMode(mode);
    const select = document.getElementById('coupling-mode');
    if (select && select.value !== couplingMode) {
        select.value = couplingMode;
    }
    if (!audioReady || typeof wasmSetCouplingMode === 'undefined') {
        return;
    }
    const ok = wasmSetCouplingMode(couplingMode);
    if (ok === false) {
        console.warn('Failed to set coupling mode:', couplingMode);
    }
}

function normalizeStringModel(model) {
    const normalized = String(model || '').trim().toLowerCase();
    if (normalized === 'dwg' || normalized === 'modal') {
        return normalized;
    }
    return 'dwg';
}

function setStringModel(model) {
    stringModel = normalizeStringModel(model);
    const select = document.getElementById('string-model');
    if (select && select.value !== stringModel) {
        select.value = stringModel;
    }
    if (!audioReady || typeof wasmSetStringModel === 'undefined') {
        return;
    }
    const ok = wasmSetStringModel(stringModel);
    if (ok === false) {
        console.warn('Failed to set string model:', stringModel);
    }
}

function syncKeyVisual(note) {
    const key = document.querySelector(`[data-note="${note}"]`);
    if (!key) return;

    const isLatched = latchedNotes.has(note);
    const isDown = heldNotes.has(note) || isLatched;
    key.classList.toggle('active', isDown);
    key.classList.toggle('latched', isLatched);
}

function clamp01(x) {
    if (x <= 0) return 0;
    if (x >= 1) return 1;
    return x;
}

function normalizedVelocityFromPointerY(clientY, keyElement) {
    if (!keyElement) {
        return clamp01(noteVelocity / 127);
    }
    const rect = keyElement.getBoundingClientRect();
    if (!rect || rect.height <= 0) {
        return clamp01(noteVelocity / 127);
    }
    const y = clientY - rect.top;
    return clamp01(y / rect.height);
}

function midiVelocityFromNormalized(normalized) {
    const floor = clamp01(velocityCurve.floor);
    const raw = clamp01(normalized);
    if (raw <= floor) {
        return 0;
    }

    const scaled = clamp01((raw - floor) / (1 - floor));
    let shaped = scaled;
    if (velocityCurve.mode === 'power') {
        shaped = Math.pow(scaled, velocityCurve.exponent);
    }

    const v = Math.round(clamp01(shaped) * 127);
    if (v < 0) return 0;
    if (v > 127) return 127;
    return v;
}

function sendKeyDownToWasm(note, midiVelocity) {
    const v = Math.max(0, Math.min(127, midiVelocity | 0));
    if (v <= 0) {
        if (typeof wasmKeyDown !== 'undefined') {
            wasmKeyDown(note);
            return;
        }
        return;
    }
    if (typeof wasmNoteOn !== 'undefined') {
        wasmNoteOn(note, v);
    }
}

function attachKeyboardListeners() {
    const keys = document.querySelectorAll('.key');
    const sustainButton = document.getElementById('sustain-pedal');
    const sustainState = document.getElementById('sustain-state');
    const damperButton = document.getElementById('damper-toggle');
    const sustainLevelSlider = document.getElementById('sustain-level');
    const sustainLevelValue = document.getElementById('sustain-level-value');
    const couplingModeSelect = document.getElementById('coupling-mode');
    const stringModelSelect = document.getElementById('string-model');
    const masterVolumeSlider = document.getElementById('master-volume');
    const masterVolumeValue = document.getElementById('master-volume-value');
    const limiterButton = document.getElementById('limiter-toggle');
    const reverbButton = document.getElementById('reverb-toggle');
    const reverbAmountSlider = document.getElementById('reverb-amount');
    const reverbAmountValue = document.getElementById('reverb-amount-value');

    function updateSliderFill(slider, value) {
        const min = Number(slider.min) || 0;
        const max = Number(slider.max) || 100;
        const pct = `${((value - min) / (max - min)) * 100}%`;
        slider.style.background = `linear-gradient(90deg, rgba(222, 189, 126, 0.9) 0%, rgba(222, 189, 126, 0.42) ${pct}, rgba(31, 34, 41, 0.85) ${pct}, rgba(31, 34, 41, 0.85) 100%)`;
    }

    function syncPedalUI() {
        sustainButton.classList.toggle('active', sustainPedalDown);
        sustainButton.setAttribute('aria-pressed', String(sustainPedalDown));
        sustainState.textContent = sustainPedalDown ? 'ON' : 'OFF';

        damperButton.classList.toggle('active', damperEngaged);
        damperButton.setAttribute('aria-pressed', String(damperEngaged));
        damperButton.textContent = damperEngaged ? 'ON' : 'OFF';
    }

    function syncOutputUI() {
        limiterButton.classList.toggle('active', limiterEnabled);
        limiterButton.setAttribute('aria-pressed', String(limiterEnabled));
        limiterButton.textContent = limiterEnabled ? 'ON' : 'OFF';

        reverbButton.classList.toggle('active', reverbEnabled);
        reverbButton.setAttribute('aria-pressed', String(reverbEnabled));
        reverbButton.textContent = reverbEnabled ? 'ON' : 'OFF';
        reverbAmountSlider.disabled = !reverbEnabled;
    }

    function setSustainState(down) {
        sustainPedalDown = down;
        syncPedalUI();
        handleSustain(down);
    }

    function setDamperState(on) {
        damperEngaged = on;
        syncPedalUI();
        pushSustainToEngine();
    }

    // A slider at 0 parses to numeric zero, so fall back only when the value is
    // not a number at all; `|| 50` would silently turn 0% into 50%.
    function parseSustainLevel(value) {
        const parsed = parseInt(value, 10);
        return Number.isNaN(parsed) ? 50 : parsed;
    }

    sustainLevel = parseSustainLevel(sustainLevelSlider.value);
    sustainLevelValue.textContent = `${sustainLevel}%`;
    updateSliderFill(sustainLevelSlider, sustainLevel);
    masterVolume = parseSustainLevel(masterVolumeSlider.value);
    masterVolumeValue.textContent = `${masterVolume}%`;
    updateSliderFill(masterVolumeSlider, masterVolume);
    reverbAmount = parseSustainLevel(reverbAmountSlider.value);
    reverbAmountValue.textContent = `${reverbAmount}%`;
    updateSliderFill(reverbAmountSlider, reverbAmount);
    syncPedalUI();
    syncOutputUI();
    setCouplingMode(couplingModeSelect ? couplingModeSelect.value : couplingMode);
    setStringModel(stringModelSelect ? stringModelSelect.value : stringModel);

    keys.forEach(key => {
        const note = parseInt(key.dataset.note, 10);

        key.addEventListener('contextmenu', (e) => {
            e.preventDefault();
        });

        // Mouse events
        key.addEventListener('mousedown', (e) => {
            if (e.button !== 0 && e.button !== 2) {
                return;
            }
            e.preventDefault();

            if (latchedNotes.has(note)) {
                latchedReleaseArmed.add(note);
                return;
            }

            if (e.button === 2) {
                latchedNotes.add(note);
                if (!heldNotes.has(note)) {
                    const velocityNorm = normalizedVelocityFromPointerY(e.clientY, key);
                    handleNoteOn(note, velocityNorm);
                }
                syncKeyVisual(note);
                return;
            }

            mousePressedNotes.add(note);
            const velocityNorm = normalizedVelocityFromPointerY(e.clientY, key);
            handleNoteOn(note, velocityNorm);
            syncKeyVisual(note);
        });

        key.addEventListener('mouseup', (e) => {
            if (e.button !== 0 && e.button !== 2) {
                return;
            }
            e.preventDefault();

            if (latchedReleaseArmed.has(note)) {
                latchedReleaseArmed.delete(note);
                latchedNotes.delete(note);
                mousePressedNotes.delete(note);
                handleNoteOff(note);
                syncKeyVisual(note);
                return;
            }

            mousePressedNotes.delete(note);
            if (latchedNotes.has(note)) {
                syncKeyVisual(note);
                return;
            }

            handleNoteOff(note);
            syncKeyVisual(note);
        });

        key.addEventListener('mouseleave', () => {
            if (mousePressedNotes.has(note) && !latchedNotes.has(note)) {
                mousePressedNotes.delete(note);
                handleNoteOff(note);
                syncKeyVisual(note);
            }
        });

        // Touch events
        key.addEventListener('touchstart', (e) => {
            e.preventDefault();
            const touch = e.touches && e.touches.length > 0 ? e.touches[0] : null;
            const velocityNorm = touch ? normalizedVelocityFromPointerY(touch.clientY, key) : (noteVelocity / 127);
            handleNoteOn(note, velocityNorm);
            syncKeyVisual(note);
        });

        key.addEventListener('touchend', (e) => {
            e.preventDefault();
            if (latchedNotes.has(note)) {
                latchedNotes.delete(note);
            }
            handleNoteOff(note);
            syncKeyVisual(note);
        });
    });

    document.addEventListener('mouseup', () => {
        if (latchedReleaseArmed.size === 0) {
            return;
        }
        for (const note of Array.from(latchedReleaseArmed)) {
            latchedReleaseArmed.delete(note);
            latchedNotes.delete(note);
            mousePressedNotes.delete(note);
            handleNoteOff(note);
            syncKeyVisual(note);
        }
    });

    sustainButton.addEventListener('click', () => {
        setSustainState(!sustainPedalDown);
    });

    damperButton.addEventListener('click', () => {
        setDamperState(!damperEngaged);
    });

    sustainLevelSlider.addEventListener('input', (event) => {
        sustainLevel = parseSustainLevel(event.target.value);
        sustainLevelValue.textContent = `${sustainLevel}%`;
        updateSliderFill(sustainLevelSlider, sustainLevel);
        pushSustainToEngine();
    });

    masterVolumeSlider.addEventListener('input', (event) => {
        masterVolume = parseSustainLevel(event.target.value);
        masterVolumeValue.textContent = `${masterVolume}%`;
        updateSliderFill(masterVolumeSlider, masterVolume);
        pushOutputToEngine();
    });

    limiterButton.addEventListener('click', () => {
        limiterEnabled = !limiterEnabled;
        syncOutputUI();
        pushOutputToEngine();
    });

    reverbButton.addEventListener('click', () => {
        reverbEnabled = !reverbEnabled;
        syncOutputUI();
        pushOutputToEngine();
    });

    reverbAmountSlider.addEventListener('input', (event) => {
        reverbAmount = parseSustainLevel(event.target.value);
        reverbAmountValue.textContent = `${reverbAmount}%`;
        updateSliderFill(reverbAmountSlider, reverbAmount);
        pushOutputToEngine();
    });

    if (couplingModeSelect) {
        couplingModeSelect.value = normalizeCouplingMode(couplingModeSelect.value);
        couplingModeSelect.addEventListener('change', (event) => {
            setCouplingMode(event.target.value);
        });
    }
    if (stringModelSelect) {
        stringModelSelect.value = normalizeStringModel(stringModelSelect.value);
        stringModelSelect.addEventListener('change', (event) => {
            setStringModel(event.target.value);
        });
    }

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
            handleNoteOn(note, noteVelocity / 127);
            syncKeyVisual(note);
        }
    });

    document.addEventListener('keyup', (e) => {
        if (e.code === 'Space') {
            return;
        }

        const note = keyMap.get(e.key.toUpperCase());
        if (note !== undefined) {
            pressedKeys.delete(e.key);
            if (!latchedNotes.has(note)) {
                handleNoteOff(note);
            }
            syncKeyVisual(note);
        }
    });
}

// Pushes the current pedal state to the engine. The slider is the pedal's
// travel depth, so it only takes effect while the pedal is engaged.
function pushSustainToEngine() {
    if (!audioReady) return;

    const amount = damperEngaged
        ? (sustainPedalDown ? sustainLevel / 100 : 0)
        : 1;
    if (typeof wasmSetSustainAmount !== 'undefined') {
        wasmSetSustainAmount(amount);
    } else if (typeof wasmSetSustain !== 'undefined') {
        wasmSetSustain(sustainPedalDown);
    }
}

function pushOutputToEngine() {
    if (!audioReady) return;

    if (typeof wasmSetMasterGain !== 'undefined') {
        wasmSetMasterGain(masterVolume / 100);
    }
    if (typeof wasmSetLimiterEnabled !== 'undefined') {
        wasmSetLimiterEnabled(limiterEnabled);
    }
    if (typeof wasmSetReverbEnabled !== 'undefined') {
        wasmSetReverbEnabled(reverbEnabled);
    }
    if (typeof wasmSetReverbAmount !== 'undefined') {
        wasmSetReverbAmount(reverbAmount / 100);
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

                    // Go may grow WASM memory while rendering, which detaches
                    // the ArrayBuffer captured before wasmProcessBlock.
                    wasmMemoryBuffer = wasmMemory.buffer;
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
        setCouplingMode(couplingMode);
        setStringModel(stringModel);
        pushSustainToEngine();
        pushOutputToEngine();
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

function handleNoteOn(note, velocityNormalized = noteVelocity / 127) {
    if (heldNotes.has(note)) {
        return;
    }
    heldNotes.add(note);
    const midiVelocity = midiVelocityFromNormalized(velocityNormalized);

    if (!audioReady) {
        pendingNotes.set(note, midiVelocity);
        initAudio()
            .then(() => {
                if (audioReady && heldNotes.has(note) && pendingNotes.has(note)) {
                    sendKeyDownToWasm(note, pendingNotes.get(note));
                }
            })
            .catch(() => {
                // initAudio already updates UI with the error details.
            });
        return;
    }

    sendKeyDownToWasm(note, midiVelocity);
}

function handleNoteOff(note) {
    if (!heldNotes.has(note) && !pendingNotes.has(note)) {
        return;
    }
    pendingNotes.delete(note);
    heldNotes.delete(note);
    if (!audioReady) return;

    // The engine owns the release: the string keeps ringing exactly as long as
    // the current damper setting allows.
    if (typeof wasmNoteOff !== 'undefined') {
        wasmNoteOff(note);
    }
}

function handleSustain(down) {
    sustainPedalDown = down;
    pushSustainToEngine();
}

// Dual-IR mix applied once a room IR is active.
// (bodyDry, bodyGain, roomWet, roomGain)
const IR_MIX_WITH_ROOM = [0.7, 1.0, 0.35, 1.0];

async function loadIR() {
    try {
        const response = await fetch('dist/assets/ir/default_96k.wav');
        if (!response.ok) {
            console.warn('IR not found, continuing without convolution');
            return;
        }

        const arrayBuffer = await response.arrayBuffer();
        if (typeof wasmLoadIR === 'undefined') {
            console.warn('wasmLoadIR unavailable, continuing without convolution');
            return;
        }

        if (wasmLoadIR('room', arrayBuffer) === false) {
            console.warn('Room IR could not be applied, continuing without convolution');
            return;
        }

        if (typeof wasmSetIRMix !== 'undefined') {
            wasmSetIRMix(...IR_MIX_WITH_ROOM);
        }

        console.log('Room IR loaded and applied');
    } catch (error) {
        console.warn('Failed to load IR:', error);
    }
}

// Initialize on load
window.addEventListener('load', init);
