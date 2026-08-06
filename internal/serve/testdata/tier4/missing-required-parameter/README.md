# Tier-4 fixture: missing-required-parameter

Minimal Aggregate + Release + Manifest exercising the
`ErrParameterMissing` path in the resolution stage of the compiler
pipeline.

The Aggregate declares `spec.parameters.database_url` with `type:
string` and `required: true`. The Release omits any `spec.parameters:`
block — `database_url` is not supplied. `validateRequiredParameters`
detects the missing value and emits a `*ResolverError` carrying
`Field == ".spec.parameters.database_url"` and the sentinel
`ErrParameterMissing`.

The daemon's `runTier4Validation` consumes this through
`RunTier4Pipeline`'s `Tier4Result.ResolveDiagnostics`, walks the
parsed YAML to the matching key, and emits a `Diagnostic` positioned
at the `database_url` key in the Aggregate (line 8, column 5).

Used by integration tests in `tier4_validation_test.go`.
