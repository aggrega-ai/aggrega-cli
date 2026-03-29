// Package provider defines the Intent 002 rendering boundary: pluggable engines that turn a
// target-agnostic types.IRGraph plus a types.Release scope into physical artifact bytes (Helm,
// Compose, etc.). Selection is by scope provider name via GetEngine.
package provider
