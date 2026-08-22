package main

// WASM export-contract guard.
//
// cmd/piano-wasm is built with `//go:build js && wasm`, so none of its code can
// be compiled - let alone executed - on the host that runs `go test`. There is
// therefore no way to call the exports and check them. What can still be
// checked, and what actually breaks in practice, is the *name contract* between
// the two sides: main.go registers a set of globals with js.Global().Set, and
// web/main.js plus web/piano-worklet.js call those globals by name. A rename on
// either side is silent - the browser only reports `wasmFoo is not defined` at
// runtime, and only on the code path that happens to use it.
//
// So this file parses both sides as text and cross-checks the names:
//
//   - The Go side is parsed with go/ast (not grep), so only real
//     js.Global().Set("name", ...) calls count; a name in a comment or a string
//     literal elsewhere does not.
//   - The JS side is scanned for call expressions `wasmXxx(`, which is how the
//     web client invokes an export. `typeof wasmXxx !== 'undefined'` guards are
//     not matched on their own, but every guarded name in the web client is also
//     called in the same file, so nothing is lost.
//
// Both directions are checked, and the union is additionally pinned against a
// literal list, so that adding an export without wiring it up (or deleting one
// the web client still calls) fails here instead of in a browser.
//
// This test file deliberately carries no build constraint: it is the only Go
// file in this directory that the host toolchain compiles. `go test` is happy
// with a directory whose only host-visible file is a test file.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"sort"
	"strconv"
	"testing"
)

// wasmExportContract is the agreed set of globals the WASM build exposes to the
// web client. It is duplicated here on purpose: a pinned literal turns "someone
// added/removed an export" into a deliberate two-line edit with a reviewer
// looking at it, rather than a silent change that both extraction halves would
// happily agree on.
var wasmExportContract = []string{
	"wasmInit",
	"wasmNoteOn",
	"wasmKeyDown",
	"wasmNoteOff",
	"wasmSetSustain",
	"wasmSetSustainAmount",
	"wasmSetCouplingMode",
	"wasmSetStringModel",
	"wasmLoadIR",
	"wasmSetIRMix",
	"wasmProcessBlock",
	"wasmGetMemoryBuffer",
}

// wasmExportsNotCalledByWebClient lists exports that exist for the host page but
// are not invoked by name from the bundled JS.
//
// wasmGetMemoryBuffer is the only one as of 2026-08-22: web/main.js reads the
// linear memory through `result.instance.exports.mem`/`memory` and slices
// `wasmMemory.buffer` directly, so the accessor is only a fallback for embedders
// that do not have the instance handle. It stays exported on purpose; the
// allowlist keeps this test from either failing on it or going quiet about it.
var wasmExportsNotCalledByWebClient = map[string]bool{
	"wasmGetMemoryBuffer": true,
}

// webClientSources are the JS files that talk to the WASM instance.
var webClientSources = []string{
	"../../web/main.js",
	"../../web/piano-worklet.js",
}

// jsWasmCall matches a call of a WASM global, e.g. `wasmNoteOn(note, v)`. The
// leading word boundary keeps `window.wasmNoteOn(` out (there is no such usage
// today, and if one appears it should be reviewed rather than silently matched).
var jsWasmCall = regexp.MustCompile(`\bwasm[A-Z][A-Za-z0-9_]*\s*\(`)

// goExportedWASMNames returns the names registered via js.Global().Set(...) in
// the given Go source file.
func goExportedWASMNames(t *testing.T, path string) []string {
	t.Helper()

	fset := token.NewFileSet()
	// parser.SkipObjectResolution keeps this cheap; the file's build constraint
	// is irrelevant to the parser, which never consults it.
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	var names []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		// Shape: <expr>.Set(<string>, <expr>) where <expr> is js.Global().
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Set" {
			return true
		}
		inner, ok := sel.X.(*ast.CallExpr)
		if !ok {
			return true
		}
		globalSel, ok := inner.Fun.(*ast.SelectorExpr)
		if !ok || globalSel.Sel.Name != "Global" {
			return true
		}
		pkg, ok := globalSel.X.(*ast.Ident)
		if !ok || pkg.Name != "js" {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		name, err := strconv.Unquote(lit.Value)
		if err != nil {
			t.Fatalf("unquote export name %s: %v", lit.Value, err)
		}
		names = append(names, name)
		return true
	})

	sort.Strings(names)
	return names
}

// jsCalledWASMNames returns the WASM globals invoked by the web client sources.
func jsCalledWASMNames(t *testing.T) map[string]string {
	t.Helper()

	called := make(map[string]string)
	for _, path := range webClientSources {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, m := range jsWasmCall.FindAllString(string(data), -1) {
			// Trim the trailing whitespace and "(" that the pattern includes.
			name := m[:len(m)-1]
			for len(name) > 0 && (name[len(name)-1] == ' ' || name[len(name)-1] == '\t') {
				name = name[:len(name)-1]
			}
			if _, seen := called[name]; !seen {
				called[name] = path
			}
		}
	}
	return called
}

// TestWASMExportContractMatchesWebClient fails if the Go export list, the web
// client's call sites, and the pinned contract above ever disagree.
func TestWASMExportContractMatchesWebClient(t *testing.T) {
	exported := goExportedWASMNames(t, "main.go")
	if len(exported) == 0 {
		t.Fatalf("no js.Global().Set exports found in main.go; the extraction is broken, not the contract")
	}

	exportedSet := make(map[string]bool, len(exported))
	for _, n := range exported {
		exportedSet[n] = true
	}

	pinned := make(map[string]bool, len(wasmExportContract))
	for _, n := range wasmExportContract {
		pinned[n] = true
	}

	// 1. Go side vs pinned contract, both directions.
	for _, n := range exported {
		if !pinned[n] {
			t.Errorf("main.go exports %q, which is not in wasmExportContract: add it here and wire it up in web/, or drop the export", n)
		}
	}
	for _, n := range wasmExportContract {
		if !exportedSet[n] {
			t.Errorf("wasmExportContract lists %q, but main.go no longer exports it: the web client would see an undefined global", n)
		}
	}

	// 2. JS call sites must resolve to a real export.
	called := jsCalledWASMNames(t)
	if len(called) == 0 {
		t.Fatalf("no wasm* call sites found in %v; the extraction is broken, not the contract", webClientSources)
	}
	for name, path := range called {
		if !exportedSet[name] {
			t.Errorf("%s calls %q, which the WASM build does not export", path, name)
		}
	}

	// 3. Exports the web client never calls must be declared dead on purpose.
	for _, n := range exported {
		if _, isCalled := called[n]; isCalled {
			continue
		}
		if !wasmExportsNotCalledByWebClient[n] {
			t.Errorf("main.go exports %q but no web client source calls it: either wire it up or add it to wasmExportsNotCalledByWebClient with a reason", n)
		}
	}
	for n := range wasmExportsNotCalledByWebClient {
		if _, isCalled := called[n]; isCalled {
			t.Errorf("%q is listed as not called by the web client, but it now is: drop the allowlist entry", n)
		}
		if !exportedSet[n] {
			t.Errorf("%q is allowlisted as an uncalled export, but main.go does not export it at all", n)
		}
	}
}
