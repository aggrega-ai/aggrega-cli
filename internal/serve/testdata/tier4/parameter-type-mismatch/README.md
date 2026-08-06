# Tier-4 fixture: parameter-type-mismatch

Minimal Aggregate + Release + Manifest exercising the parameter-contract
type-mismatch path in the resolution stage of the compiler pipeline.

The Aggregate declares `spec.parameters.port` with `type: integer` and
`required: true`. The Release supplies `spec.parameters.port:
"not-an-integer"` (a string). `ValidateParametersAgainstContract`
detects the type mismatch and emits a `*ResolverError` carrying
`Field == ".spec.parameters.port"` and the sentinel
`ErrParameterContractViolation`.

The daemon's `runTier4Validation` consumes this through
`RunTier4Pipeline`'s `Tier4Result.ResolveDiagnostics`, walks the
parsed YAML to the matching key, and emits a `Diagnostic` positioned
at the `port` key in the Aggregate (line 8, column 5).

Used by integration tests in `tier4_validation_test.go`.
