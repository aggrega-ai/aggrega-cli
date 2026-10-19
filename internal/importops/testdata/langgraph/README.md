# LangGraph application fixtures

Two LangGraph applications, each authored from the keys the LangGraph CLI's
configuration reference documents for `langgraph.json`. Every value that could
be a secret is a key-shaped needle, never a real one.

- `python/` — a Python application declaring two graphs in `langgraph.json`,
  written in an order that is not sorted: `triage`, declared as an object naming
  its import path under `path` and describing it, and `support`, declared as an
  import path. Its checkpointer names no backend, so LangGraph reads it as
  `default`, PostgreSQL; it configures a store's semantic-search index and time
  to live; and its `env` is an object of three variables, one whose name reads
  as a credential's (`OPENAI_API_KEY`), one whose name does not
  (`SUPPORT_QUEUE`) and one whose value is a number. `src/` holds a graph's
  code, which the import never reads.
- `envfile/` — a JavaScript application declaring one graph, its checkpointer
  backend `mongo`, MongoDB, and its `env` the path of an environment file,
  `local.env`, beside it. That file's values are needles the import must never
  carry: one plain, one after `export`, one quoted, one quoted across two lines
  whose second line reads like a variable of its own, and a line naming no
  variable. A directory import is never handed the file, and names it as not
  handed; the tests hand it under the importer's `with.sources`. `src/` holds
  the graph's code, which the import never reads.
