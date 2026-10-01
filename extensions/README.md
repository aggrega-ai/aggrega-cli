# Extensions

`mcpb/` holds the source of an installable MCP bundle for Aggrega. Its `manifest.json` describes a
Model Context Protocol server as a single file that a client installs and adds from its own extension
settings, which is how clients such as Claude Desktop take a local server.

**The bundle carries no Aggrega program.** It launches the `aggrega` executable already installed on
the machine, at a path the person installing it supplies, and speaks the protocol over that process's
standard input and output — the same server `aggrega mcp` runs with no listen address. Because the
manifest names no executable of any platform, the same bundle applies to every platform it declares:
macOS, Windows and Linux.

## Building the bundle

One script builds it. From the repository root:

```
scripts/build-mcp-bundle.sh --output-dir <dir>
```

It checks the manifest against the format's schema with the format's own packer, `@anthropic-ai/mcpb`,
run through `npx` at the exact version the script pins, then writes the installable file
`aggrega-<version>.mcpb` into `<dir>` and prints its path. The directory must lie outside the
repository — the built file is a build output and does not belong in it. Node.js and `npx` must be
installed. The release workflow runs the same script and attaches the file to each release beside the
binaries.

**The `version` in `mcpb/manifest.json` is a placeholder that the build replaces.** The script stamps
the bundle with the version the `aggrega` binary built from the same commit reports — the release tag
on a clean checkout of a tagged commit, a development version anywhere else — into a staged copy of the
manifest, so the checked-in file is never edited for a release. With `--expect-version <version>` the
build refuses, naming both versions, when the stamped one differs.

The built file holds the manifest and nothing else, so `mcpb/` holds the manifest and nothing else, and
the build refuses that directory when it holds anything more. These instructions sit one level above it
for that reason.
