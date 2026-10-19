# MCP file fixtures

Every file in this directory is authored here, by hand, to the shape its client or
the MCP registry documents. None of them is copied, in whole or in part, from a
client's documentation, the registry's repository or any server's sources.

Each is kept under a plain name, because a client's configuration lives in a
directory version control commonly ignores; a test lays it down under the name its
client reads it by before importing it:

- `claude-code.mcp.json` — laid down as `.mcp.json`, Claude Code's project
  configuration: a server reached over HTTP whose address and header read
  variables, one with a default, and a server started with `npx` whose token is a
  variable reference.
- `claude_desktop_config.json` — Claude Desktop's configuration, under its own
  name: a search server holding a key in its environment, written where the
  client's documented example writes one, and a database server whose connection
  string, an argument, holds a password.
- `vscode.mcp.json` — laid down as `.vscode/mcp.json`, Visual Studio Code's
  workspace configuration: its `inputs`, a server started with `uvx` whose key
  reads an input and which names an environment file, and a server reached over
  HTTP whose header reads an input.
- `vscode-commented.mcp.json` — laid down as `.vscode/mcp.json`: the same kind of
  file written as JSON with comments, holding line and block comments, trailing
  commas, and strings holding `//`.
- `vscode-uncommented.mcp.json` — laid down as `.vscode/mcp.json`: the commented
  configuration above with its comments and trailing commas removed, which an
  import drafts exactly as it drafts the commented one.
- `cursor.mcp.json` — laid down as `.cursor/mcp.json`, Cursor's project
  configuration: a remote server whose address carries a key in its user part, in
  its query and in its fragment, and which is sent a key in one header and a
  variable reference in another, a server
  started with `node` reading Cursor's variables, and a remote server signing in
  with a static OAuth client.
- `registry-npm.server.json` — laid down as `server.json`, a registry server
  description publishing an npm package — with a named argument, a positional
  argument left for the user, a secret environment variable and one with a
  default — and an image.
- `registry-remote.server.json` — laid down as `server.json`, a registry server
  description publishing a remote reached over Streamable HTTP with a secret
  header.
- `registry-oci.server.json` — laid down as `server.json`, a registry server
  description publishing an image serving the protocol over Streamable HTTP.

The values beginning `planted`, and the key beginning `BSA-planted`, are key-shaped
credential values the tests search the drafts and the diagnostics for: each stands
where a configuration can keep a secret, so a draft or a report that carries one
where it should not is caught by name. None is a real credential.
