// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package types

import (
	"regexp"
	"strings"
)

// RequiredAttachmentTypeAnnotationPrefix is the annotation-key prefix a
// Component uses to declare a presence mandate: the component instance must
// carry at least one attachment whose type is the annotation's value.
//
// A key is the prefix followed by an author-chosen label —
// features.aggrega.ai/requiredType.<label> — and the label's only job is to
// name the requirement in the diagnostic a violation produces. Labelled keys
// are how a Component declares more than one mandate: annotations are a flat
// string map and cannot carry a duplicate key.
//
// A mandate is not a binding point. It constrains what must be present; it
// names nothing a template or an inject can dereference, and it is satisfied
// by any attachment of the required type at any scope — a component-level
// fill, a cascaded subsystem or boundary attachment, or an attachment produced
// by annotation-driven expansion. featuresSchema declares the separate binding
// surface, whose slot names ARE dereferenceable and whose cardinality is
// exactly one.
const RequiredAttachmentTypeAnnotationPrefix = "features.aggrega.ai/requiredType."

// FeatureNameShape matches a Feature entity's name: two kebab-case segments
// separated by "/". Example matches: "core/prometheus-exporter",
// "example/db-binding".
//
// This is the in-process home of the pattern: the Go compiler consults it
// wherever a Feature type URI arrives on an authoring surface that carries no
// structural type of its own — an annotation value is an opaque string, so
// nothing upstream has checked its shape.
//
// A second spelling of the same pattern lives in pkg/catalog, where it exists
// to be read as source text by the publishing linter's cross-runtime parity
// check — that check pins both the file it reads and the literal form of the
// declaration, so the two spellings cannot be folded into one by moving or
// aliasing either of them. They are instead held byte-identical by a parity
// test, which fails naming both sides on any divergence. Do not "simplify"
// this by deleting one side: doing so either blinds the cross-runtime pin or
// makes this pattern unreachable from the compiler, which sits below
// pkg/catalog in the import graph.
const FeatureNameShape = `^[a-z][a-z0-9-]*/[a-z][a-z0-9-]*$`

// FeatureNameShapeRe is the compiled form of FeatureNameShape.
var FeatureNameShapeRe = regexp.MustCompile(FeatureNameShape)

// RequiredAttachmentType is one presence mandate read off a Component's
// metadata.annotations.
//
// Malformed is empty for a mandate that can be evaluated. When it is
// non-empty it carries the reason the mandate cannot be evaluated, and the
// consumer reports that reason instead of testing the requirement — a mandate
// whose declaration is broken must never be reported as satisfied.
type RequiredAttachmentType struct {
	// Key is the annotation key exactly as authored.
	Key string
	// Label is the author-chosen suffix that names the requirement.
	Label string
	// Type is the Feature type URI at least one attachment must carry.
	Type string
	// Malformed names why the mandate cannot be evaluated, or is empty.
	Malformed string
}

// RequiredAttachmentTypes extracts every presence mandate declared on a
// Component's annotation map, in the map's author order, so a diagnostic set
// derived from it is byte-identical across runs of the same input.
//
// A key that names the mandate family but carries no label — the prefix with
// its trailing separator dropped, or with an empty label after it — is
// returned as malformed rather than ignored. Silently skipping it would turn a
// declaration the author wrote into an absent constraint, which is the one
// outcome a presence mandate must never produce: a broken requirement reading
// as a satisfied one. A returned entry likewise carries a non-empty Malformed
// when its value is not a Feature type URI. Keys outside the family are
// ignored.
func RequiredAttachmentTypes(annotations OrderedMap[string]) []RequiredAttachmentType {
	labelless := strings.TrimSuffix(RequiredAttachmentTypeAnnotationPrefix, ".")

	var out []RequiredAttachmentType
	for key, value := range annotations.All() {
		labelled := strings.HasPrefix(key, RequiredAttachmentTypeAnnotationPrefix)
		if !labelled && key != labelless {
			continue
		}
		m := RequiredAttachmentType{Key: key, Type: value}
		if labelled {
			m.Label = strings.TrimPrefix(key, RequiredAttachmentTypeAnnotationPrefix)
		}
		switch {
		case m.Label == "":
			m.Malformed = "the key needs a label naming the requirement, as in " +
				RequiredAttachmentTypeAnnotationPrefix + "database"
		case !FeatureNameShapeRe.MatchString(value):
			m.Malformed = "the value must be a Feature type URI of the form <publisher>/<name> " +
				"in lower-case kebab segments, as in core/prometheus-exporter"
		}
		out = append(out, m)
	}
	return out
}
