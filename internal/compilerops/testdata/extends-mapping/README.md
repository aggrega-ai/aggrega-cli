# Fixture: mapping a wrapper's type onto its base's Recipe

A base Component (`example/base-server`), a wrapper that extends it
(`example/wrapped-server`), and two Recipes — one implementing the base, one
implementing an unrelated type. The Aggregate uses the wrapper.

Two Release/Manifest pairs over that one catalog, forming a matched pair:

- `release-base-recipe.yaml` maps the **wrapper's** type onto the **base's**
  Recipe. That is the reduced-extends contract, so the mapping-table consistency
  gate must produce **no** finding.
- `release-unrelated-recipe.yaml` maps the same wrapper type onto a Recipe
  implementing a type the wrapper does not extend, so the gate must produce **one**
  finding.

The pair is what makes the first case non-vacuous. The gate resolves ancestry
through a resolver the caller supplies; a caller that supplies the recipe shell's
own resolver gets no Component index and therefore no ancestry, which makes every
mapping onto a base's Recipe look inconsistent. That mistake would turn the
first case red while leaving the second green, so the two together pin *which*
resolver the closure resolution wires — not merely that the gate runs.
