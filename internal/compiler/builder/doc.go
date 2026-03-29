// Package builder is Intent 003: it ingests Domain 1 (Aggregate) and Domain 4 (Manifest)
// YAML, resolves abstract types against the manifest lockfile, and emits a target-agnostic
// types.IRGraph for downstream security translation and provider engines. It performs no
// physical rendering (Helm, Crossplane, etc.).
package builder
