// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore

import cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"

// ErrEmptySource is emitted when a Recipe has `source: ""` (empty string).
// Nil/absent source = sourceless (generative) mode; non-empty string = sourced.
// Empty string is never valid.
func ErrEmptySource() error { return errEmptySource }

var errEmptySource = cerrors.Register("compiler-renderer-helm/empty-source", cerrors.Meta{
	Message: "recipe source is empty string; use nil for sourceless mode or a non-empty path for sourced mode",
})

// ErrRenderedFileCollision is emitted when two distinct rendered resources
// derive the same output filename with differing content. Output filenames are
// derived from each resource's identity (kind, namespace, name), so two
// templates that render resources sharing one identity would occupy a single
// output slot; emitting only one would silently drop the other. The render
// halts instead, naming both source templates and the shared output path so the
// author can give the resources distinct identities. Byte-identical duplicates
// are not a collision — one copy is kept.
func ErrRenderedFileCollision() error { return errRenderedFileCollision }

var errRenderedFileCollision = cerrors.Register("compiler-renderer-helm/rendered-file-collision", cerrors.Meta{
	Message: "two rendered resources derive the same output filename with differing content",
})

// StatefulSet governing-Service halt sentinels. A synthesized StatefulSet (any
// Workload declaring a persistentTemplate volume) requires exactly one headless
// Service to govern it — the synth sets spec.serviceName from that Service's
// rendered name. Zero or several headless Services halt loudly rather than emit
// a StatefulSet K8s would reject. Surfaced wrapped in a CompilerError via
// cerrors so errors.Is matches.
// ErrStatefulSetNoHeadlessService is emitted when a Workload synthesizing a
// StatefulSet declares no headless Service to govern it (K8s requires
// spec.serviceName).
func ErrStatefulSetNoHeadlessService() error { return errStatefulSetNoHeadlessService }

var errStatefulSetNoHeadlessService = cerrors.Register("compiler-renderer-helm/stateful-set-no-headless-service", cerrors.Meta{
	Message: "synthesized StatefulSet has no headless governing Service",
})

// ErrStatefulSetAmbiguousHeadlessService is emitted when a Workload
// synthesizing a StatefulSet declares more than one headless Service, so the
// governing Service is ambiguous.
func ErrStatefulSetAmbiguousHeadlessService() error { return errStatefulSetAmbiguousHeadlessService }

var errStatefulSetAmbiguousHeadlessService = cerrors.Register("compiler-renderer-helm/stateful-set-ambiguous-headless-service", cerrors.Meta{
	Message: "synthesized StatefulSet has more than one headless Service",
})
