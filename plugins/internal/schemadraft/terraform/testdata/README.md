# Terraform schema fixtures

The fixtures here are synthetic: each is written in the format `terraform providers schema -json` writes, and every
description in them is written for these tests. The provider `example/ledgers` and its types do not exist.

- `ledger.json` is an export of one provider holding the resources `ex_ledger` and `ex_journal`, a data source, an
  ephemeral resource and a function, which a listing leaves out. `ex_ledger` carries a spread of attribute types
  (primitives, collections, objects, a tuple, `dynamic`), a nested-type attribute, read-back, sensitive, write-only
  and deprecated attributes, and blocks of each nesting mode with bounds on their item count.
