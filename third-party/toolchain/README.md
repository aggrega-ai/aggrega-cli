# Toolchain notices

The embedded Rust plugins are built for `wasm32-wasip1`, which links the Rust standard library and wasi-libc
(the toolchain's `libc.a`) into each guest. This directory holds the license texts of those components, pinned to
the toolchain each plugin's `rust-toolchain.toml` names. `scripts/gen-third-party-licenses.sh` reads them offline
and lists the components in the third-party notices.

- `toolchains.tsv`: one row per Rust channel: the channel, the wasi-sdk release its Rust release builds the WASI
  target with, and the wasi-libc commit that wasi-sdk release pins.
- `rust-<channel>/COPYRIGHT`: the Rust project's `COPYRIGHT` file at the release tag `<channel>`.
- `wasi-libc-<commit>/`: wasi-libc's `LICENSE`, `LICENSE-MIT`, `LICENSE-APACHE-LLVM` and the license files of
  the works it derives from that the default build links: musl, cloudlibc and musl-fts.

Every file is a verbatim copy of the upstream file at the pinned ref. dlmalloc (CC0-1.0, which asks for no
attribution) and emmalloc (not linked by default) are not listed.

When a plugin moves to a channel that has no row, fetch its texts with:

```sh
scripts/fetch-toolchain-notices.sh <channel>
```

Then regenerate the attribution with `scripts/gen-third-party-licenses.sh`.
