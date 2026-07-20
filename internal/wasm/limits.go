// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

// DefaultMaxMemoryPages is the platform default cap on a plugin's linear memory,
// in Wasm pages (64 KiB per page). 1024 pages is 64 MiB — generous headroom for
// a rendering or policy plugin's working set while still bounding a runaway
// allocation. The loader applies this cap to any plugin whose spec leaves
// MaxMemoryPages zero, so a zero is "apply the default cap", never "unbounded".
// It is a tunable platform value, not a fundamental limit: a plugin with a
// legitimately larger footprint raises its own cap through the entity's runtime
// block.
const DefaultMaxMemoryPages uint32 = 1024

// DescribeMaxMemoryPages is the memory cap applied when describing a plugin that
// carries no explicit limit. The platform default cap is generous for a running
// render but too small to instantiate the largest built-in provider guests for a
// describe() call, so describe-time paths raise the ceiling to the value the
// provider catalog entities themselves declare. It is a describe-time convenience
// only — the render path applies each plugin's own authored limits.
const DescribeMaxMemoryPages uint32 = 4096

// DefaultTimeoutMs is the platform default cap on a single plugin Call's wall
// time, in milliseconds. 5000 ms is a wide ceiling for a per-step render or lint
// while still turning a hung guest (an infinite loop, a frozen clock) into a
// loud cancellation rather than an indefinite block. The loader applies this cap
// to any plugin whose spec leaves TimeoutMs zero, so a zero is "apply the default
// cap", never "uncancellable". It is a tunable platform value: a plugin with a
// legitimately longer-running export raises its own ceiling through the entity's
// runtime block.
const DefaultTimeoutMs uint64 = 5000

// effectiveMaxPages returns the memory cap the loader applies for spec: the
// spec's MaxMemoryPages when non-zero, else DefaultMaxMemoryPages. Centralising
// the zero-to-default substitution here keeps a single place where "absent limit
// means platform default cap" is decided, so every load path — entity, linter,
// and test — gets the same cap and a zero is never unbounded.
func effectiveMaxPages(spec PluginSpec) uint32 {
	if spec.MaxMemoryPages != 0 {
		return spec.MaxMemoryPages
	}
	return DefaultMaxMemoryPages
}

// effectiveTimeoutMs returns the per-call wall-time cap the loader applies for
// spec: the spec's TimeoutMs when non-zero, else DefaultTimeoutMs. Centralising
// the zero-to-default substitution here keeps a single place where "absent limit
// means platform default cap" is decided, so every load path gets the same
// ceiling and a zero is never uncancellable.
func effectiveTimeoutMs(spec PluginSpec) uint64 {
	if spec.TimeoutMs != 0 {
		return spec.TimeoutMs
	}
	return DefaultTimeoutMs
}
