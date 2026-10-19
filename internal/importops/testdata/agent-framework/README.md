# Agent Framework fixtures

Three Microsoft Agent Framework declarative agents, authored here to the keys Agent Framework's
declarative-agent documentation and its declarative package's object model describe. None is copied
from Agent Framework's own samples; no Agent Framework installation reads or writes them.

- `learn-agent.yaml` — an agent of the OpenAI provider naming a model id, an endpoint and a key on a
  `key` connection, the options of its requests, an MCP server reached at a `url` with an approval
  mode, the tools it allows and a key on its own connection, a function tool and a tool the model's
  provider runs.
- `docs-agent.yaml` — an agent of the OpenAI provider whose model id, key and MCP server's address
  are each written as `=Env.NAME`, a reference to an environment variable.
- `research-agent.yaml` — an agent naming no provider, so Agent Framework uses the default provider
  the application sets, on a `remote` connection, with an MCP server and a code interpreter.

The values that look like keys — the model's key and the MCP server's key in `learn-agent.yaml`, and
the session in its MCP server's address — are planted needles shaped like keys, not real ones; the
tests prove where each reaches.
