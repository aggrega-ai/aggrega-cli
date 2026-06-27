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

## WASI imports and the static import firewall

The loader enables WASI under a deterministic, capability-denied wazero
configuration: observable wall time is frozen, monotonic time advances
(the guest runtime needs an advancing monotonic clock to bootstrap),
the entropy source yields zeros, stdio is discarded, and there is no
filesystem, argument, or environment access. On top of that
configuration a static import firewall walks each plugin's
`wasi_snapshot_preview1` import section against a default-deny
allowlist: the benign runtime-init imports a language runtime touches
during startup are permitted (`clock_time_get`, `clock_res_get`,
`random_get`, `environ_*`, `args_*`, `fd_*`, `fd_fdstat_set_flags`,
`sched_yield`, `poll_oneoff`, `proc_exit`); the capability-granting
imports are rejected (`path_*`, `sock_*`, `fd_readdir`). A guest that
invokes `proc_exit` surfaces as an `ErrWasmPluginExited`-wrapped error
without affecting the host process.

Two classes of fixture exercise the firewall:

- **WASI-free fixtures** — `mutator-rust.wasm`,
  `policy-validator-rust.wasm` (Cargo `wasm32-unknown-unknown`). These
  emit zero WASI imports and load with no import-firewall work.
- **WASI-importing fixtures** — `mutator-go.wasm`,
  `policy-validator-go.wasm`, `frp-step-go.wasm` (standard Go
  `GOOS=wasip1` reactors), `mutator-typescript.wasm`,
  `policy-validator-typescript.wasm` (extism-js + QuickJS runtime), and
  `wasi-probe-rust.wasm` (Cargo `wasm32-wasip1`). Each imports a subset
  of the allowlisted runtime-init functions; the static import
  allowlist is the load-bearing component accepting them.

## Toolchain dependencies

Each fixture's `build.sh` invokes its source language's Wasm build
chain:

- Go (standard `GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared`)
  — the standard Go toolchain (`go`).
- Rust (`cargo build --release --target wasm32-unknown-unknown`) —
  `cargo` + the `wasm32-unknown-unknown` target. (Ships with the
  Rust PDK.)
- Rust WASI (`cargo build --release --target wasm32-wasip1`) —
  `cargo` + the `wasm32-wasip1` target. (Only the wasi-probe
  fixture uses this; the fixture's build.sh runs `rustup target
  add wasm32-wasip1` idempotently.)
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
