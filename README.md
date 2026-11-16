# Aggrega

Aggrega is an open-source compiler for AI-era architecture. Describe the
components of an application and how they connect; Aggrega turns them,
deterministically, into the deployment files that run it.

- **Use the languages you know.** A compiler for the languages you already use:
  YAML in; Helm charts, Kubernetes manifests and Compose, Terraform or
  CloudFormation files out.
- **Developers describe, Ops implement.** Developers declare components, their
  features and who uses whom; the platform team decides how and where each one
  runs.
- **One application, many platforms.** The same application runs on Docker
  Compose on a laptop and on Kubernetes in production: changing target changes a
  mapping, not your code.
- **Reproducible and governed.** Same input, same output, with every dependency
  pinned in a lockfile. Your policies plug into the compiler and stop a build
  that breaks them, saying what to fix.

This repository holds the compiler and its command-line interface, the `aggrega`
binary.

## Install

`curl -fsSL https://aggrega.ai/install.sh | sh` on Linux and macOS, or
`irm https://aggrega.ai/install.ps1 | iex` in PowerShell on Windows, installs the
`aggrega` binary for the current user.
[Install Aggrega](https://aggrega.ai/docs/install/#install-the-binary) covers what
the command does, the manual download, and connecting an AI assistant.

## Use it with your AI assistant

An assistant that can run commands on your machine can install Aggrega, add the
Aggrega skill to itself and connect itself to the Aggrega MCP server, from one
prompt you paste into it:
[Set up Aggrega with your AI assistant](https://aggrega.ai/docs/ai-assistants/set-up/)
gives the prompt. To connect a client yourself, follow
[Connect an MCP client](https://aggrega.ai/docs/install/#connect-an-mcp-client).

## Use it in your editor

The [Aggrega extension for Visual Studio Code](https://github.com/aggrega-ai/aggrega-vscode-extension)
brings validation, navigation, hover and completion to Aggrega YAML, backed by
the `aggrega` binary.

## How it works

Aggrega is an Ahead-of-Time compiler: it reads the files you author and writes
static artifacts, byte-identical for identical input, and leaves deploying them
to other tools, such as ArgoCD or Flux. It has no access to runtime
information, so it never reaches a running cluster or a cloud account; it builds
no images and reconciles no state.

## Where to go next

- [Getting Started with Authoring](https://aggrega.ai/docs/authoring/getting-started/)
  — the authoring loop, from a blank file to a validated one.
- [Runnable Examples](https://aggrega.ai/docs/language-reference/examples/) —
  complete workspaces you can build with the `aggrega` CLI.
- [CLI Overview](https://aggrega.ai/docs/language-reference/07-cli/) — every
  `aggrega` subcommand.
- [Compatibility](https://aggrega.ai/docs/language-reference/compatibility/) —
  what a release promises for the schemas, the commands and flags, the
  assistant tools, the error codes and the plugin kits.
- [Building](#building) and [Testing](#testing) below — building and testing the
  compiler from this repository.

## Building

The module root is this directory. Build the CLI with the Go toolchain:

```bash
go build ./cmd/aggrega
```

Every module this one depends on, the schemas, the Go plugin kit and the Go ABI
among them, resolves at the version `go.mod` requires, so a clone of this
repository builds on its own.

The CLI embeds the provider rendering guests. These are reproducible build
outputs generated from guest source, not tracked files, so a plain `go build`
embeds whatever guest artifacts already sit on disk from an earlier build, and
nothing in a fresh clone. In a fresh clone, and after changing a guest or the
shared render core it reuses, regenerate before building:

```bash
go generate ./internal/wasm/embedded/... && go build ./cmd/aggrega
```

This repository's `scripts/check-embedded-guests.sh` helper reports any
embedded guest that is older than its source.

## Testing

Run the fast suite with:

```bash
go test ./...
```

The integration suite is build-tagged `integration` and is excluded from the
fast run (its in-process OCI registry harness pulls in test-only dependencies
that never enter the production binary):

```bash
go test -tags=integration ./...
```

Some tests in both suites also read a companion repository: the standard
catalog, the worked examples, the plugin kit's examples or the shared ABI wire
vectors. They expect it checked out **as a sibling directory** next to this one,
under its repository name, which is load-bearing:

```text
<parent>/
  <this-repo>/     # the compiler and CLI (this module)
  aggrega-hub/     # the standard catalog (under aggrega-catalog/)
  aggrega-docs/    # the language-reference worked examples
  aggrega-pdk/     # the plugin kit examples some fixtures are built from
  aggrega-abi/     # the shared wire vectors and the generated guest codecs
```

A test that needs a sibling skips when it is absent, and its skip message names
the repository and what the test reads from it. A sibling that is present but
lacks what the test reads fails the test. Full coverage needs all four present.

The Plugin entities of the core plugins the binary embeds are generated from the
plugin overlays in `aggrega-hub`, by `scripts/gen-embedded-plugin-entities.sh`,
and are not tracked files. Without `aggrega-hub` the binary embeds the core
plugins' guests but none of their entities, and the tests that read what the
binary resolves through them skip, saying so.

### Regenerating the guest codecs

The guest-side ABI codecs committed in `aggrega-abi` are generated from the
canonical schemas by this repository's type generator,
`go generate ./internal/tools/typegen`. That step writes
into `aggrega-abi`, so it needs both checkouts: check out `aggrega-abi` beside
this repository first. Without it the generator stops with an error naming the
directory it expected.

## License

The Aggrega compiler and CLI are released under the Apache License, Version 2.0; the full text is in the
[`LICENSE`](LICENSE) file.
