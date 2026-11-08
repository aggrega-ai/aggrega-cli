# Google ADK fixtures

Each directory listed below holds one application declared in Google ADK's Agent Config files, authored
here to the keys ADK's Agent Config documentation and its published schema (`AgentConfig.json`)
describe. None is copied from ADK's own samples; no ADK installation reads or writes them.

- `tutor/` — an LlmAgent root (`root_agent.yaml`) naming two sub-agents by `config_path` and one by
  `code`, the built-in `google_search` tool, a tool the application's code defines with `args`, a
  sub-agent asking for another model, and `generate_content_config` HTTP options. `tutor_tools.py` is
  the application's code, which the import never reads, and `lesson_plan.yaml` a file no configuration
  names. A directory import hands the importer `root_agent.yaml` alone; the tests hand the sub-agents'
  configurations under the importer's `with.sources`.
- `pipeline/` — a SequentialAgent root naming two LlmAgent sub-agents under `agents/`, the root naming
  no model, so the first sub-agent's model is the one wired.
- `litellm/` — a lone LlmAgent root naming `openai/gpt-4o`, a model ADK serves through LiteLLM rather
  than through its Gemini class, so the draft leaves its model connection for the author.

The values that look like keys — the `api_key` a tool's `args` carries in
`tutor/code_tutor_agent.yaml`, and the header `tutor/math_tutor_agent.yaml` sends — are planted
needles shaped like keys, not real ones; the tests prove where each reaches. No fixture carries the
environment file ADK's scaffold writes beside `root_agent.yaml`, and the importer reads none.
