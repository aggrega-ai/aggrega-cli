// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
)

// AnnotationProvenance records how a synthetic Feature attachment was
// produced by the annotation-driven expansion pass: the annotation key
// that triggered the expansion and the source location where that
// annotation was authored. Explicit Feature attachments do not carry
// an AnnotationProvenance entry — their absence from the side-table is
// itself the "explicit" signal at the IR builder layer.
type AnnotationProvenance struct {
	// AnnotationKey is the annotation name that triggered the
	// synthetic Feature attachment (for example
	// "feature.myco/rewrite-host").
	AnnotationKey string

	// AnnotationSource locates the annotation declaration in source
	// form: <file-path>:<dotted-yaml-key-path>, the file being the
	// document the annotation was written in, or the key path alone
	// where the run named no file for that document. For an
	// inherited boundary-level annotation, the key-path points to the
	// boundary's annotations map (where the author wrote the
	// annotation, not where it took effect). The location carries no
	// line number.
	AnnotationSource string

	// InjectedByPlugin, when non-empty, is the URI of the
	// annotation-transform governance plugin that injected the
	// triggering annotation before expansion. It is host-written and
	// carried through expansion (the expansion pass preserves a
	// pre-seeded value rather than clobbering it), so a synthetic
	// Feature attachment produced from a host-injected annotation
	// records which plugin authored the injection. Empty for an
	// author-written annotation — its absence is the "author-declared"
	// signal, mirroring the side-table's explicit/synthetic asymmetry.
	InjectedByPlugin string
}

// injectionAttribution is the clause a refusal over an annotation a governance
// plugin injected carries to say so: which plugin injected the value, and the
// governance role it is attached at. Every refusal raised over an injected
// annotation — expansion's own and the rules asked over an authored annotation —
// spells it through here, so an operator reads one wording whichever check
// refused, and the role is named from its one declaration rather than restated.
//
// It exists because such a refusal is positioned at the Release key the value
// was set under, which the operator did not write; without the clause the
// operator is sent to correct a line that is not in the file, when the value to
// correct is the one the plugin decided.
func injectionAttribution(plugin string) string {
	return fmt.Sprintf("whose value plugin %q injected at governance role %q", plugin, plugintype.RoleAnnotationTransform)
}

// annotationProvenanceKeySeparator is the NUL byte used to separate
// composite-key segments. NUL avoids collisions with characters that
// appear naturally in boundary paths (slashes), component names
// (alphanumeric + dashes), and annotation keys (dots, slashes,
// alphanumeric).
const annotationProvenanceKeySeparator = "\x00"

// AnnotationProvenanceKey composes the side-table key from a boundary
// path, component name, and Feature instance name. On synthetic
// attachments produced by ExpandAnnotations the Feature instance name
// equals the annotation key; consumers of the side-table look up the
// trio they already have on hand at IR build time.
func AnnotationProvenanceKey(boundaryPath, componentName, featureName string) string {
	return boundaryPath + annotationProvenanceKeySeparator + componentName + annotationProvenanceKeySeparator + featureName
}
