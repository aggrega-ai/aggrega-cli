# Terraform schema fixtures

The fixtures here are synthetic: each is written in the format `terraform providers schema -json` writes, and every
description in them is written for these tests. The provider `example/ledgers` and its types do not exist.

- `ledger.json` is an export of one provider holding the resources `ex_ledger` and `ex_journal`, a data source, an
  ephemeral resource and a function, which a listing leaves out. `ex_ledger` carries a spread of attribute types
  (primitives, collections, objects, a tuple, `dynamic`), a nested-type attribute, read-back, sensitive, write-only
  and deprecated attributes, and blocks of each nesting mode with bounds on their item count.
- `aws-example-ledger.json` is an export of the provider `hashicorp/aws` holding one resource, `aws_example_ledger`,
  which the provider does not carry: the required `identifier` and `subnet_group` and the optional `name` and
  `kms_key_id`. Its attributes mirror four properties of the CloudFormation fixture `AWS::Example::Ledger`, so the
  drafts of the two place them alike.
- `golden/` holds what the Terraform writer drafts: `vault.main.tf.json` and `vault.recipe.yaml` for a resource the
  tests build in code, and `ledger.main.tf.json` and `ledger.recipe.yaml` for `ex_ledger` imported from `ledger.json`.
  The tests comparing a draft with them write the draft into them instead when run with `UPDATE_GOLDEN=1`.
