// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import "errors"

var (
	// ErrSubAggregateLoad fires from flattenAggregate when a sub-aggregate
	// import either fails to load (the SubAggregateLoader callback returns
	// an error) or is detected as a circular reference (the Aggregate's
	// `aggregates` block names a sub-aggregate type already on the visited
	// stack). The wrapped error carries the failing type URI and the
	// accumulated dotted prefix of the offending chain so callers can point
	// the author at the specific import that caused the failure.
	ErrSubAggregateLoad = errors.New("sub-aggregate load failed")

	// ErrInvalidUsesFormat fires from normalizeUses and parseVerboseEntry
	// when a `uses` block carries a value that is neither a string array
	// nor a verbose map, or a string-array element that is not a string,
	// or a verbose-entry value that is neither a map nor a simple-string
	// shorthand, or an inject block that is not a map. The wrapped error
	// names the socket and (when applicable) the alias plus the offending
	// Go type. Socket-name validity is enforced upstream by the resolver
	// against each entity's usesSchema (the socket-allowlist and
	// required-socket checks happen at Step 1); the formats this sentinel
	// catches are structural shape errors the YAML schema accepts but the
	// builder cannot interpret.
	ErrInvalidUsesFormat = errors.New("invalid uses format")

	// ErrMaxDepth fires from flattenAggregate when sub-aggregate inclusion
	// recursion exceeds maxSubAggregateDepth (currently 10). The bound
	// caps both pathological-depth authoring mistakes and any sub-aggregate
	// cycle that would otherwise recurse to stack-overflow. The wrapped
	// error carries the depth reached and the prefix path of the offending
	// chain.
	ErrMaxDepth = errors.New("max sub-aggregate nesting depth exceeded")
)
