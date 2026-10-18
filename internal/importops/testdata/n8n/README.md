# n8n workflow fixtures

Every workflow in this directory is authored here, by hand, to the shape n8n
exports a workflow in: the workflow's `id`, `name`, `nodes` and `connections`,
each node's `name`, `type`, `typeVersion`, `position`, `parameters` and
`credentials`, and the connections keyed by the name of the node they leave, then
by connection type. None of them is copied, in whole or in part, from n8n's
repository or template library.

- `agent-workflow.json` — an AI agent started by a chat message, with a chat
  model, a memory held in the workflow, an MCP client tool, a vector store
  answering as a tool and the embeddings it reads with, an HTTP request tool, and
  a note pinned to the canvas. Its system message holds double braces n8n reads as
  an expression.
- `http-workflow.json` — a scheduled workflow fetching from one HTTP API and
  posting to another, with a node that signs the payload with a secret.

The values beginning `planted` are credential values the tests search the drafts
for: each stands where a workflow can keep a secret, so a draft that carries one
where it should not is caught by name.
