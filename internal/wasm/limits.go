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

// DefaultTimeoutMs is the platform default cap on a single plugin Call's GUEST
// execution time, in milliseconds. The loader applies this cap to any plugin
// whose spec leaves TimeoutMs zero, so a zero is "apply the default cap", never
// "uncancellable". It is a tunable platform value: a plugin with a legitimately
// longer-running export raises its own ceiling through the entity's runtime
// block.
//
// It is a LIVENESS CEILING — it separates a guest that returns from one that
// will not yield — and it must not be read as a performance budget for an
// export. Guest execution alone spends it: the time an instance spends inside
// host functions is measured and subtracted, so a guest that asks the host for a
// parsed tree is not charged for the host loading and running the parser. That
// subtraction is what lets one number stand for every export, since the guest's
// own share of a call is small and stable while the host's share is neither.
//
// 5000 ms is wide against that measurement rather than against a call's wall
// time: a guest's own share of an export is milliseconds, so the ceiling sits
// orders of magnitude above the work it bounds and a loaded machine does not
// walk into it. A ceiling sized against wall time instead would have to absorb
// however long the host takes, which is unbounded and load-dependent — the
// reason the host's share is excluded rather than budgeted for.
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

// effectiveTimeoutMs returns the per-call guest-execution cap the loader applies
// for spec: the spec's TimeoutMs when non-zero, else DefaultTimeoutMs.
// Centralising the zero-to-default substitution here keeps a single place where
// "absent limit means platform default cap" is decided, so every load path gets
// the same ceiling and a zero is never uncancellable.
func effectiveTimeoutMs(spec PluginSpec) uint64 {
	if spec.TimeoutMs != 0 {
		return spec.TimeoutMs
	}
	return DefaultTimeoutMs
}
