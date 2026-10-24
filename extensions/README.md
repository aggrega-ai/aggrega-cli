# Extensions

`mcpb/` holds the source of an installable MCP bundle for Aggrega. Its `manifest.json` describes a
Model Context Protocol server as a single file that a client installs and adds from its own extension
settings, which is how some clients take a local server. The client table on the
[setup page](https://aggrega.ai/docs/authoring/setup/#where-each-client-keeps-its-server-list) says
which clients take it this way and how every other client connects.

**The bundle carries no Aggrega program.** It launches the `aggrega` executable already installed on
the machine, at a path the person installing it supplies, and speaks the protocol over that process's
standard input and output — the same server `aggrega mcp` runs with no listen address. Because the
manifest names no executable of any platform, the same bundle applies to every platform it declares:
macOS, Windows and Linux.

**It asks for two settings, and both are required.** **Path to the aggrega executable** is the program
it launches. **Workspace folder** is the workspace the server serves: the folder that holds the
project's `.aggrega-config.yaml`, handed to the server as `AGGREGA_WORKSPACE_ROOT` in its environment
rather than as an argument, so the folder does not pass through the quoting of the command line the
client composes. A client starts a local server in a folder of its own choosing, often a system folder,
so a request naming no workspace of its own is answered in the folder this setting names, or refused,
and never answered in that one. The folder is taken as the
workspace root itself, as `--workspace-root` is on every command. A folder that does not exist does not
stop the server: it writes its refusal to standard error, starts all the same, and answers every request
that acts in the workspace with that refusal, which names `AGGREGA_WORKSPACE_ROOT` as the variable the
**Workspace folder** setting sets and quotes the value it was given — so the client receives it in the
answer to each such request rather than only in its log. A setting left empty, which a client may save
and launch the server with, is answered the same way: a server launched over standard input and output
with `AGGREGA_BUNDLE_VERSION` in its environment and no root named refuses every request naming no
workspace of its own with a refusal naming the empty **Workspace folder** setting, where any other
launch reads an empty variable as unset. A Go test in this repository holds the variable's spelling and
the setting's title in the manifest to the ones the program reads.

## Building the bundle

One script builds it. From the repository root:

```
scripts/build-mcp-bundle.sh --output-dir <dir>
```

It checks the manifest against the format's schema with the format's own packer, `@anthropic-ai/mcpb`,
run through `npx` at the exact version the script pins — after refusing, itself, any
`${user_config.KEY}` the manifest writes that names a setting it does not declare, which the packer
accepts — then writes the installable file
`aggrega-<version>.mcpb` into `<dir>` and prints its path. The directory must lie outside the
repository — the built file is a build output and does not belong in it. Node.js and `npx` must be
installed. The release workflow runs the same script and attaches the file to each release beside the
binaries.

**The `version` in `mcpb/manifest.json` is a placeholder that the build replaces.** The script stamps
the bundle with the version the `aggrega` binary built from the same commit reports — the release tag
on a clean checkout of a tagged commit, a development version anywhere else — into a staged copy of the
manifest, so the checked-in file is never edited for a release. With `--expect-version <version>` the
build refuses, naming both versions, when the stamped one differs.

**So is the `AGGREGA_BUNDLE_VERSION` entry in the manifest's launch environment.** The checked-in entry
carries the same placeholder as `version`, and the script writes the same stamped version into both, so
the bundle hands the server it launches the version the bundle itself carries. The server reads it once
when it starts and reports it beside its own version on `describe-capabilities` called with no
arguments; where the two differ, that answer and the guidance the server offers a model on connecting
say the executable and the bundle come from different releases. Nothing refuses on a difference. The
build refuses a manifest whose launch environment carries no such entry, naming it, and a Go test in
this repository holds the entry's spelling in the manifest to the one the program reads, and its value
to the placeholder `version` carries.

The built file holds the manifest and nothing else, so `mcpb/` holds the manifest and nothing else, and
the build refuses that directory when it holds anything more. These instructions sit one level above it
for that reason.
