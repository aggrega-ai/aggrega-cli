# tier4 fixture — attachment slot requirement and shape

One Component catalog entity declaring a required Black-Box slot
(`root_password`) and an optional transparent one, with two authoring variants
of the same Aggregate:

- `aggregate.yaml` — `unbound` binds nothing, so the slot's requirement is
  unmet; `bound` binds it correctly.
- `aggregate-shape.yaml` — both bind the slot, and `bound` also authors an
  `inject:` block on it, which the slot's empty allowlist reserves for the
  Recipe.

Two variants rather than one document carrying both faults, because the two
diagnostics are produced at different pipeline stages: the shape violation
halts during resolution, so a document carrying it never reaches the
static-analysis stage that reports the requirement. The fixture exists so each
diagnostic can be observed on the language-server path, positioned on its own
authoring key rather than at the head of the file.
