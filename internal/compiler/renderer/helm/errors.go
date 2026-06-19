// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helm

import "errors"

// ErrEmptySource is emitted when a Recipe has `source: ""` (empty string).
// Nil/absent source = sourceless (generative) mode; non-empty string = sourced.
// Empty string is never valid.
var ErrEmptySource = errors.New("recipe source is empty string; use nil for sourceless mode or a non-empty path for sourced mode")

// StatefulSet governing-Service halt sentinels. A synthesized StatefulSet (any
// Workload declaring a persistentTemplate volume) requires exactly one headless
// Service to govern it — the synth sets spec.serviceName from that Service's
// rendered name. Zero or several headless Services halt loudly rather than emit
// a StatefulSet K8s would reject. Surfaced wrapped in a CompilerError via
// cerrors so errors.Is matches.
var (
	// ErrStatefulSetNoHeadlessService is emitted when a Workload synthesizing a
	// StatefulSet declares no headless Service to govern it (K8s requires
	// spec.serviceName).
	ErrStatefulSetNoHeadlessService = errors.New("synthesized StatefulSet has no headless governing Service")

	// ErrStatefulSetAmbiguousHeadlessService is emitted when a Workload
	// synthesizing a StatefulSet declares more than one headless Service, so the
	// governing Service is ambiguous.
	ErrStatefulSetAmbiguousHeadlessService = errors.New("synthesized StatefulSet has more than one headless Service")
)
