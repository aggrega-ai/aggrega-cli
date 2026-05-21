# Wasm loader test fixtures

This directory holds `.wasm` fixtures the loader's integration tests
in `loader_integration_test.go` consume. The artifacts are built on
demand from their source in the sibling `aggrega-pdk/` submodule and
gitignored — nothing binary is committed here.

## How the fixtures are built

`testdata_build_test.go` defines `ensureFixtureBuilt(t, "<name>")`.
For each requested artifact:

1. Parse the artifact name as `<socket>-<lang>.wasm`.
2. Locate the source at `aggrega-pdk/<lang>/examples/<socket>/`.
3. If the `.wasm` is missing or older than any source file, invoke
   the fixture's `build.sh` to rebuild it into this directory.
4. If the per-language PDK source is not present, or the toolchain
   the fixture needs is not on `PATH`, `t.Skipf` cleanly with a
   diagnostic pointing at `scripts/setup-workspace.sh`.

The cache lives per `go test` process; multiple tests sharing a
fixture build it once. A fresh CI checkout always rebuilds.

## Toolchain dependencies

Each fixture's `build.sh` invokes its source language's Wasm build
chain:

- Go (TinyGo `wasm-unknown`) — `tinygo`.
- Rust (`cargo build --release --target wasm32-unknown-unknown`) —
  `cargo` + the `wasm32-unknown-unknown` target. (Ships with the
  Rust PDK.)
- TypeScript (Extism JS PDK build) — `node` + the Extism JS PDK
  tooling. (Ships with the TypeScript PDK.)

The workspace setup script (`scripts/setup-workspace.sh` from the
umbrella root) installs all required toolchains user-locally.

## Naming convention

`<socket>-<lang>.wasm` — for example `mutator-go.wasm`,
`policy-validator-rust.wasm`. The helper relies on this convention
to resolve the source directory from the artifact name.

## Test consumption shape

```go
func TestPluginRoundTrip_MutatorGo(t *testing.T) {
    plugin, cleanup := loadFixturePlugin(t, "mutator-go.wasm")
    defer cleanup()
    // ... call plugin.Call(...) and assert on the output
}
```

`loadFixturePlugin` invokes `ensureFixtureBuilt` then constructs the
`Plugin` via the package's `Loader.Load`. Test bodies remain focused
on input/output behaviour; the build orchestration is encapsulated
in the helper.
