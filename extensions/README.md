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

The bundle format publishes its own packer, `@anthropic-ai/mcpb`. Run it from this directory:

```
npx @anthropic-ai/mcpb validate mcpb/manifest.json
npx @anthropic-ai/mcpb pack mcpb aggrega.mcpb
```

The first command checks the manifest against the format's schema. The second writes the installable
file, and its second argument names where that file is written — it is a build output and does not
belong in the repository.

Everything under `mcpb/` is packed into the built file, so that directory holds the manifest and
nothing else. These instructions sit one level above it for that reason.
