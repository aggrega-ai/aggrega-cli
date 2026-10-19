# Compose fixtures

Two kinds of file live here.

Authored here, by hand, to the shape the Compose file reference documents:

- `authored/compose.yaml` — a shop API backed by PostgreSQL and Redis: the database
  and the cache run the official `postgres` and `redis` images, and the API runs an
  image of its own, depends on the database, reaches the cache through an
  environment value naming it, and holds the planted values described below and a
  variable reference.
- `project/` — a Compose project directory: `compose.yaml`, the
  `compose.override.yaml` Compose merges over it, changing a service's image,
  ports and environment and adding a service, and a `docker-compose.yml` Compose
  does not read, since `compose.yaml` precedes it.

Copied whole, unchanged, from the
[docker/compose-for-agents](https://github.com/docker/compose-for-agents)
repository at commit `bfd4fe952591495af757a1a737c7eacc78c75c15`, whose README
states its licence as `SPDX-License-Identifier: Apache-2.0 OR MIT`:

- `compose-for-agents/langgraph.compose.yaml` — from `langgraph/compose.yaml`: a
  PostgreSQL database, an importer and an agent built from the project's own
  Dockerfile, an MCP gateway, and a model the agent uses.
- `compose-for-agents/adk.compose.yaml` — from `adk/compose.yaml`: an agent built
  from the project, an MCP gateway, and a model with a context size.
- `compose-for-agents/crew-ai.compose.yaml` — from `crew-ai/compose.yaml`: agents
  built from the project, an MCP gateway, and a model with a context size and
  runtime flags.

The values beginning `planted` and `sk-planted` in `authored/compose.yaml` are
key-shaped values the tests search the drafts and the diagnostics for: one sits
under a variable whose name reads as a credential, one under a variable whose name
does not, so a draft or a report that treats them differently, or carries one where
it should not, is caught by name. Neither is a real credential.
