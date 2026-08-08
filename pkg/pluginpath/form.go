// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginpath

import (
	"fmt"
	"strings"
)

// ReferenceForm classifies a plugin reference by its structural shape — the
// single input the per-surface acceptance policy keys on. The three forms
// partition every reference: an explicit filesystem path, a non-entity artifact
// reference, or a bare Plugin entity URI. The shape is decided from the string
// alone; no filesystem, catalog, or registry access is involved.
type ReferenceForm int

const (
	// FormEntityURI is a bare Plugin entity URI — `name:version` or
	// `provider/name:version`. It is the residual form: a reference that is
	// neither an explicit filesystem path nor a non-entity artifact reference.
	// A pipeline plugin must be this form.
	FormEntityURI ReferenceForm = iota

	// FormLocalPath is an explicit filesystem-path reference — a `./` or `../`
	// relative prefix, or an absolute path.
	FormLocalPath

	// FormArtifact is a non-entity artifact reference — an explicit `oci://`
	// scheme, a bare `.wasm` reference, or an `@sha256:` digest reference. It
	// addresses a .wasm artifact directly rather than naming a Plugin entity.
	FormArtifact
)

// ClassifyReference reports the structural form of a plugin reference. The order
// of the tests is load-bearing: an explicit filesystem path wins first — a local
// `./foo.wasm` is a path, not a bare-`.wasm` artifact — then the non-entity
// artifact shapes, and every remaining reference is a bare entity URI. It keys
// only on the reference string, never on what the reference resolves to, so it is
// a pure structural classifier.
func ClassifyReference(uri string) ReferenceForm {
	if isLocalPathRef(uri) {
		return FormLocalPath
	}
	if strings.HasPrefix(uri, "oci://") ||
		strings.Contains(uri, "@sha256:") ||
		strings.HasSuffix(uri, ".wasm") {
		return FormArtifact
	}
	return FormEntityURI
}

// SurfacePolicy is the per-surface acceptance policy the form validator applies:
// which reference forms a given plugin socket accepts. It is the single knob that
// distinguishes the entity-only pipeline-plugin surface from the permissive
// cross-cutting sockets, so the form-classification logic itself lives in exactly
// one place and only the accepted set differs per surface. A future surface with
// its own accepted set is added here as another policy, not as a second
// classifier.
type SurfacePolicy int

const (
	// SurfaceEntityOnly accepts only a bare Plugin entity URI. The pipeline-plugin
	// surface uses it: a filesystem path or a non-entity artifact reference is
	// rejected early, before entity translation, naming the offending reference.
	SurfaceEntityOnly SurfacePolicy = iota

	// SurfacePermissive accepts every form — a filesystem path, a non-entity
	// artifact reference, or a bare entity URI. The cross-cutting sockets (linter,
	// mutator, policy, importer) use it: they attach a plugin directly and route a
	// non-entity reference on to the artifact cascade rather than requiring a
	// Plugin entity.
	SurfacePermissive
)

// AcceptReferenceForm reports whether a reference is acceptable on a surface
// governed by policy: nil when it is, and a wrapped ErrPluginEntityNotFound
// naming the offending reference and its form when it is not. It is the single
// home of the "is this reference acceptable on this surface?" decision — every
// socket routes its reference through this one check, so the entity-only pipeline
// surface and the permissive cross-cutting sockets share one classifier and
// differ only by the policy they pass.
//
// Only SurfaceEntityOnly rejects, and only the two structurally-non-entity
// shapes: a filesystem-path form and an artifact form. The form check layers
// with — it never replaces — entity existence: a well-formed bare entity URI that
// names no actual Plugin entity passes this check and still halts later at entity
// lookup. The rejection wraps ErrPluginEntityNotFound so a permissive caller that
// falls back on that sentinel routes the reference on unchanged.
func AcceptReferenceForm(uri string, policy SurfacePolicy) error {
	if policy == SurfacePermissive {
		return nil
	}
	switch ClassifyReference(uri) {
	case FormLocalPath:
		return fmt.Errorf(
			"%w: %q is a filesystem path, but a pipeline plugin must be a Plugin entity URI (e.g. `name:version` or `provider/name:version`)",
			ErrPluginEntityNotFound(), uri)
	case FormArtifact:
		return fmt.Errorf(
			"%w: %q is an artifact reference (an `oci://` scheme, a bare `.wasm`, or an `@sha256:` digest), "+
				"but a pipeline plugin must be a Plugin entity URI (e.g. `name:version` or `provider/name:version`)",
			ErrPluginEntityNotFound(), uri)
	default:
		return nil
	}
}
