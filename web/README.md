# Algo Piano Web Demo

Browser-based piano demo using WebAssembly and a main-thread audio callback.

## Local Development

### Build WASM

```bash
./scripts/build-wasm.sh
```

Or manually:

```bash
mkdir -p web/dist/assets/ir
GOOS=js GOARCH=wasm go build -o web/dist/piano.wasm ./cmd/piano-wasm
cp "$(go env GOROOT)/misc/wasm/wasm_exec.js" web/
cp -r assets/ir/* web/dist/assets/ir/ 2>/dev/null || true
```

### Serve Locally

```bash
python3 -m http.server -d web 8080
```

Then open: http://localhost:8080

## Usage

- **Mouse:** Click piano keys to play notes
- **Keyboard:** Use ASDF row for white keys, QWERTY row for black keys
- **Sustain Pedal:** Click button or press Spacebar

## Architecture

- `index.html` - Main page
- `styles.css` - Keyboard styling
- `main.js` - Main thread: WASM loader, UI, and audio rendering
- `wasm_exec.js` - Go WASM runtime (from Go SDK)
- `dist/piano.wasm` - Compiled Go synthesizer
- `dist/assets/ir/` - Impulse response files

## Browser Requirements

- Chrome 66+
- Firefox 61+
- Safari 14.1+
- Edge 79+

## Live Demo

https://cwbudde.github.io/algo-piano/
