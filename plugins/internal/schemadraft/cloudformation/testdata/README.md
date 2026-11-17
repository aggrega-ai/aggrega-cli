# CloudFormation schema fixtures

The fixtures here are synthetic: each is written in CloudFormation's own schema format, and every description in
them is written for these tests. The types they describe, under the `AWS::Example` service and the `Example`
organization, do not exist.

- `ledger.json` is a provider schema of `AWS::Example::Ledger`. Its primary identifier `Identifier` is create-only
  and required; `Name` and `LedgerName` name the resource, and `LedgerName` carries a pattern with a lookahead,
  which Go's RE2 refuses. `SubnetGroup` is required and create-only, `KmsKeyId` optional and create-only.
  `MasterPassword` is a write-only string named for a credential. `Arn` is read back, and so is every member of
  `Endpoint`: `Address`, `Port`, and `Token`, a string named for a credential. `Capacity` is a required closed
  object resolved from `definitions`, `Rules` an array of a definition that refers back to itself through its
  `Children`, `LegacyMode` deprecated, and `DeletionProtection` an optional boolean.
- `ledger-describe-type.json` is the output of `aws cloudformation describe-type` for the same type: its `Schema`
  member holds `ledger.json` as a string.
- `module-describe-type.json` is a `describe-type` output whose `Type` is `MODULE`.
- `module-schema.json` is the schema of that module, whose type name ends in `::MODULE`.
- `unprovisionable-describe-type.json` is a `describe-type` output of `AWS::Example::Archive` whose
  `ProvisioningType` is `NON_PROVISIONABLE`.
- `no-properties.json` names a type and holds no `properties` object.
- `golden/ledger.component.yaml` and `golden/ledger.recipe.yaml` are the Component and the Recipe an import of
  `ledger.json` drafts. `TestCloudFormationGolden` compares a draft with them; running it with `UPDATE_GOLDEN=1`
  writes the draft into them instead.
