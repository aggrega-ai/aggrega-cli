# Dify fixtures

The Dify DSL documents the Dify importer's tests import.

## `authored/`

Both files are authored by hand for these tests. Neither was exported from a
running Dify. Each is written to the shape the export of Dify 1.17.0, the engine
version `core/ai-agent/dify` runs by default, writes: DSL version `0.7.0`, every
mapping's keys in sorted order, block style, a sequence's items at the
indentation of its key, and a string Dify's export quotes written in single
quotes.

- `workflow.yml` is a `workflow` application: a start node, a tool node calling
  a time tool, a knowledge-retrieval node naming one knowledge base, an LLM node
  whose prompt holds Dify's own `{{#...#}}` references and double quotes, an HTTP
  request node filing the answer, and an end node; and three environment
  variables — a secret whose value is written empty, as Dify's export writes a
  secret, a secret holding a value, and a plain string.
- `chat.yml` is a `chat` application: a model, one knowledge base, and a prompt
  holding a `{{#context#}}` reference and double quotes.

The values that would carry a credential are key-shaped needles planted for the
tests, never real credentials: the secret variable `ticketing_api_key` holds
`sk-planted-dify-secret-needle-4f9c2a7e`, and the HTTP request node's
`authorization.config.api_key` holds `sk-planted-dify-request-needle-8b3d61f0`.
A secret holding a value is what an export including secrets, or a hand edit,
writes. The knowledge-base identifiers and the plugin identifiers are made up.
