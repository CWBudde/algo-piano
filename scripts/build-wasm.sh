#!/bin/bash
set -e

echo "Building WASM piano demo..."

# Create output directories
mkdir -p web/dist/assets/ir

# Build WASM
echo "Compiling Go to WASM..."
GOOS=js GOARCH=wasm go build -o web/dist/piano.wasm ./cmd/piano-wasm

# Copy WASM runtime
echo "Copying wasm_exec.js..."
GOROOT=$(go env GOROOT)
if [ -f "$GOROOT/lib/wasm/wasm_exec.js" ]; then
	cp "$GOROOT/lib/wasm/wasm_exec.js" web/
elif [ -f "$GOROOT/misc/wasm/wasm_exec.js" ]; then
	cp "$GOROOT/misc/wasm/wasm_exec.js" web/
else
	echo "Error: wasm_exec.js not found in GOROOT"
	exit 1
fi

# Copy assets
echo "Copying assets..."
if [ -d "assets/ir" ]; then
	cp -r assets/ir/* web/dist/assets/ir/ 2>/dev/null || true
fi

echo "Build complete! Files in web/dist/"
echo "Run: python3 -m http.server -d web 8080"
