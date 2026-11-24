# Toolchain notices

A WebAssembly plugin carries code its toolchain builds in besides its own dependencies. This directory holds the
license texts of those components, pinned to the toolchain that builds them in. `scripts/gen-third-party-licenses.sh`
reads them offline and lists the components in the third-party notices.

## Rust

The embedded Rust plugins are built for `wasm32-wasip1`, which links the Rust standard library and wasi-libc
(the toolchain's `libc.a`) into each guest. Their texts are pinned to the channel each plugin's
`rust-toolchain.toml` names.

- `toolchains.tsv`: one row per Rust channel: the channel, the wasi-sdk release its Rust release builds the WASI
  target with, and the wasi-libc commit that wasi-sdk release pins.
- `rust-<channel>/COPYRIGHT` and `rust-<channel>/LICENSE-APACHE`: the Rust project's `COPYRIGHT` and
  `LICENSE-APACHE` files at the release tag `<channel>`.
- `wasi-libc-<commit>/`: wasi-libc's `LICENSE`, `LICENSE-MIT`, `LICENSE-APACHE-LLVM` and the license files of
  the works it derives from that the default build links: musl, cloudlibc and musl-fts.

dlmalloc (CC0-1.0, which asks for no attribution) and emmalloc (not linked by default) are not listed.

## extism-js

A TypeScript plugin is compiled by `extism-js`, which merges its JavaScript runtime into the plugin's `.wasm`. The
generator identifies the `extism-js` a plugin's build runs by the sha256 of its bytes, and lists what that release's
runtime carries.

- `extism-js.tsv`: one row per `extism-js` binary a release of `github.com/extism/js-pdk` publishes, one per
  platform: the release tag, the platform, the binary's sha256, the commit the tag names, and what the runtime
  carries: its rquickjs release, the QuickJS-NG commit and version that release builds in, the core-js and
  urlpattern-polyfill versions its prelude bundles with the commits their texts are read at, and the Rust release
  the runtime is built with. That Rust release's standard library and wasi-libc are listed from its row of
  `toolchains.tsv`.
- `js-pdk-<tag>/LICENSE`: the Extism JS PDK's `LICENSE` at the release tag.
- `quickjs-ng-<commit>/LICENSE`: QuickJS-NG's `LICENSE` at the commit the runtime's rquickjs release pins.
- `rquickjs-<version>/LICENSE`: rquickjs's `LICENSE` at its tag `v<version>`.
- `core-js-<version>/LICENSE`, `urlpattern-polyfill-<version>/LICENSE`: each package's `LICENSE` at the commit
  the npm registry records for that version.

The runtime's other Rust crates are not listed: the release tag carries no lockfile that names them.

## Azure Verified Modules

The arm importer embeds the rows of Microsoft's Azure Verified Modules module index
(`plugins/internal/armimport/avmindex_gen.go`), which is listed in the notices as data in the ARM importer's source.

- `azure-verified-modules-<commit>/LICENSE`: the `LICENSE` of `github.com/Azure/Azure-Verified-Modules` at the commit
  the generated index names as `AVMIndexCommit`. Regenerating the index at another commit needs this file pinned for
  that commit.

## Fetching

Every file is a verbatim copy of the upstream file at the pinned ref. When a plugin moves to a Rust channel that has
no row, or its build runs an `extism-js` that has none, fetch the texts with:

```sh
scripts/fetch-toolchain-notices.sh <channel>
scripts/fetch-toolchain-notices.sh extism-js <tag>
```

The `extism-js` form names the Rust release the runtime is built with, and the command that fetches it when it has
no row. Then regenerate the attribution with `scripts/gen-third-party-licenses.sh`.
