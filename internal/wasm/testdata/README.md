# Wasm loader test fixtures

This directory is a placeholder for hand-authored `.wasm` test fixtures
that the loader's tests will consume once a Wasm authoring toolchain is
available to a contributor working on this package.

## What lives here today

Nothing yet. The current test surface (`loader_test.go`) covers the
loader's Go-side contract — translation, firewall, host-function
allowlist, panic-recovery wrap — without any real `.wasm` artifact.

## What would live here

A small set of hand-authored `.wasm` fixtures exercising the loader's
Wasm-side surface end-to-end. The fixtures most useful to add:

- `panic.wasm` — a tiny plugin whose exported function executes the
  `unreachable` Wasm instruction (or otherwise traps). Lets the
  panic-recovery test exercise the full Wasm-trap-propagation path
  through Extism, not just the Go-side recover wrap.
- `noop.wasm` — a tiny plugin whose exported function returns
  successfully with empty output. Lets `Load` + `Plugin.Call` exercise
  the happy path end-to-end against a real plugin.

## How to author the fixtures

Three toolchain options. Pick whichever is most convenient for the
contributor.

### Option 1 — Rust (recommended for Extism plugins)

Install the Rust toolchain and add the `wasm32-unknown-unknown`
target:

```sh
rustup target add wasm32-unknown-unknown
```

Author a small Rust crate that uses the `extism-pdk` crate to declare
exported functions, then build with:

```sh
cargo build --release --target wasm32-unknown-unknown
```

Copy the resulting `.wasm` file from
`target/wasm32-unknown-unknown/release/<crate-name>.wasm` to this
directory.

### Option 2 — TinyGo

Install TinyGo and build a small Go program for `wasm-unknown`:

```sh
tinygo build -o panic.wasm -target wasm-unknown ./panic.go
```

### Option 3 — hand-authored WebAssembly Text format

Install `wabt` (provides `wat2wasm`). Author a `.wat` file using the
WebAssembly Text format, then compile:

```sh
wat2wasm panic.wat -o panic.wasm
```

The smallest panic-producing module in `.wat` is about a dozen lines:
a single module with one exported function whose body is the
`unreachable` instruction.

## How the test would consume a fixture

Once a fixture lands here, `loader_test.go` can add an end-to-end test
along the lines of:

```go
func TestPluginCallPropagatesGuestPanic(t *testing.T) {
    loader := &Loader{}
    spec := PluginSpec{
        Name: "panic-fixture",
        Path: "testdata/panic.wasm",
    }
    p, err := loader.Load(context.Background(), spec)
    if err != nil {
        t.Fatalf("load: %v", err)
    }
    defer p.Close(context.Background())

    _, err = p.Call(context.Background(), "panic_function", nil)
    // The expected result depends on how Extism translates the trap:
    // either an error from the SDK (most common), or a panic the wrap
    // converts to ErrWasmGuestPanic.
    if err == nil {
        t.Fatalf("expected error from panicking guest, got nil")
    }
}
```

Until the fixture lands, the Go-side panic-recovery wrap is exercised
via `TestCallWithPanicRecoveryWrapsPanic` in `loader_test.go`, which
documents the coverage gap this README would close.
