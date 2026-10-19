# CrewAI project fixtures

Three CrewAI projects, authored for these tests to the layouts CrewAI documents.
Neither is copied from another project, and no running CrewAI reads or writes
them.

- `classic/` — a project in the classic layout `crewai create crew --classic`
  scaffolds: `src/market_brief/config/agents.yaml` and
  `src/market_brief/config/tasks.yaml`, read by the import, and
  `src/market_brief/crew.py`, the crew's program, which the import never reads.
  Both agents name `openai/gpt-4o`, so one model serves both; the second
  declares no `verbose` and a member, `max_iter`, the CrewAI agent Component
  takes no value for.
- `jsonc/` — a project in the JSON-first layout `crewai create crew` scaffolds:
  `crew.jsonc` and `agents/<name>.jsonc`, written with line and block comments
  and trailing commas, and `tools/changelog_reader.py`, a custom tool of the
  program, which the import never reads. The analyst declares its `llm` as an
  object carrying a setting and a key-shaped value planted for the credential
  tests: `sk-planted-crewai-llm-needle-7c41e9d2` is no real key.
- `json/` — the same crew in the layout's strict-JSON spelling, `crew.json` and
  `agents/<name>.json`, which CrewAI reads where no `.jsonc` file of the same
  crew or agent is present. Its analyst's role differs from the `jsonc/`
  analyst's, and its `llm` object plants a key-shaped value of its own,
  `sk-planted-crewai-json-needle-3a90b1e5`, no real key either — so a test laying
  the `jsonc/` analyst's file beside it reads off the draft which spelling was
  read.

Neither project holds an environment file: CrewAI reads its keys from one when
the crew runs, and the import reads no such file.
