# Tier-4 fixture: dangling-extends

Aggregate + Release + Manifest + minimal catalog exercising the
catalog-Component dangling-extends path.

The catalog Component `local/broken-server` declares
`spec.extends: local/non-existent-base:1.0.0` against a parent URI
that does not exist in the catalog. The Aggregate references
`broken-server` via its `web` component's `type:` field. The
workspace-config points `catalog_dirs:` at the local `./catalog`
subtree so the daemon's lazy lookup finds the broken Component.

This fixture documents the diagnostic shape the daemon produces
when the lazy-lookup encounters a dangling extends. Today's
shape: Tier-D fall-back at file:1:1 with a message prefixed
`"Dependencies: "` and mentioning `extends`. The emission flows
through `runTier4Validation`'s resolveReleaseClosure-envelope
branch (the outer `resolveReleaseClosure` short-circuits the
catalog-load failure into a `*jsonRPCError` envelope), so the
message does NOT contain the `"catalog load:"` substring that
would identify the alternate catalog-load branch. The per-
Component position is stderr-logged by the lazy lookup but not
propagated to the daemon's response shape. See the test's godoc
for the full path trace.

Used by `TestRunTier4Validation_DanglingExtendsFallsBackToTierD` in
`tier4_validation_test.go`.
