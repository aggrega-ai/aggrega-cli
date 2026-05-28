# Cross-file analyzer-violations fixture

A root Aggregate (`aggregate.yaml`) and a sub-Aggregate
(`catalog/aggregates/sub-with-violation.yaml`) each attach the
`example/premium` Feature to a Component (`example/web`) carrying a
mismatching `component.aggrega.ai/type` label.

The Feature's `appliesTo` requires `component.aggrega.ai/type=premium-server`;
the Component's catalog labels carry `component.aggrega.ai/type=server`. Both
attachments therefore fail the analyzer's appliesTo check. Each produces an
`AnalysisError` whose `OriginFile` resolves to the Aggregate file that
declared the attachment — the root violation to `aggregate.yaml`, the
sub-aggregate violation to `catalog/aggregates/sub-with-violation.yaml`.

The integration test in `tier4_validation_test.go` uses the fixture to
prove the daemon's per-file diagnostic filter actually partitions
violations by source file:

1. Run `pipeline.RunTier4Pipeline` directly and assert the analyzer's
   unfiltered output contains exactly two violations with disjoint
   `OriginFile` values (one root, one sub).
2. Drive the daemon's `aggrega/validate` against the root Aggregate
   file and assert the response carries only the root-origin
   violation. The sub-origin violation is dropped by the filter.

If the fixture stops producing both violations (catalog or schema
change), step 1 surfaces the regression. If the daemon's filter
becomes a no-op, step 2 surfaces it.
