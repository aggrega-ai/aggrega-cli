# aggrega

The Aggrega compiler and command-line interface: a deterministic, Ahead-Of-Time
compiler that renders deployment artifacts from Aggrega entities. Given identical
inputs it produces byte-identical output.

## Building

The module root is this directory. Build the CLI with the Go toolchain:

```bash
go build ./cmd/aggrega
```

The CLI embeds the provider rendering guests. These are reproducible build
outputs generated from guest source, not tracked files, so a plain `go build`
embeds whatever guest artifacts already sit on disk from an earlier build. After
changing a guest or the shared render core it reuses, regenerate before
building:

```bash
go generate ./internal/wasm/embedded/... && go build ./cmd/aggrega
```

In the umbrella workspace, the build script at `scripts/build.sh` (at the
workspace root) ensures the embedded guests are current before the compile
that embeds them, so it needs no separate generate step. This repository's
own `scripts/check-embedded-guests.sh` helper reports any embedded guest
that is older than its source.

## Testing

The fast unit suite needs nothing beyond this repository:

```bash
go test ./...
```

The integration suite is build-tagged `integration` and is excluded from the
fast run (its in-process OCI registry harness pulls in test-only dependencies
that never enter the production binary). It exercises real catalog resolution and
the worked examples end to end, so it expects two companion repositories checked
out **as sibling directories** next to this one. The sibling directory names are
load-bearing — they must be exactly `aggrega-hub` and `aggrega-docs`:

```text
<parent>/
  <this-repo>/     # the compiler and CLI (this module)
  aggrega-hub/     # the standard catalog (under aggrega-catalog/)
  aggrega-docs/    # the language-reference worked examples
```

With the siblings in place:

```bash
go test -tags=integration ./cmd/aggrega/
```

Tests that need a sibling skip automatically when it is absent, so the
integration suite degrades gracefully on a partial checkout — but full coverage
requires both `aggrega-hub` and `aggrega-docs` present.
