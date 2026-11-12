// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The reading over the keys a Manifest's annotation binding registry declares,
// expressed as a constraint declaration. See checks.go for what a declaration
// here is.
//
// It sits in its own file rather than in checks.go because that file holds the
// reading over an Aggregate's parameter contract, and the two share a stage
// without sharing a subject: one is invoked per declared parameter of the
// Aggregate's document, the other per declared binding of the Manifest's
// registry. Grouping them under one file name would suggest a reader
// looking for either would find both.

// annotationBindingKeyAvoidsTheReservedPrefix reports each annotation binding
// key a Manifest declares under the prefix reserved for provider well-known
// annotations.
//
// An annotation binding lets an operator write one annotation on a component and
// have its value routed into a Feature attachment's pillar fields. The prefix
// reserved for the platform's own provenance keyspace is where the compiler
// stamps the keys it emits itself, so a binding claiming a key under it would
// have the compiler's own output and an author's routing competing for one
// keyspace. A binding is authored under a domain the author controls instead.
//
// It halts, and it could not be advisory. The reservation is what makes the
// provenance keyspace the compiler's alone; admitting a binding into it and
// reporting the fact afterwards would leave the collision in the build.
//
// It reads one fact, the Feature the binding routes to, and reads it for both of
// the things that fact answers. The second result establishes that the presented
// Manifest declares a binding of this key at all, and a key it does not declare
// is passed over rather than reported: the rule is about the keyspace an authored
// binding claims, so a key the registry does not answer for is not a violation of
// it but an absence the rule has nothing to say about. The first result names the
// Feature the offending binding was routing into, which is what tells the author
// what a rename has to keep working.
//
// The prefix itself is not read as a fact. It is a constant of the platform
// rather than anything a document states, so there is nothing for a view to
// present and a fact restating it would be a second home for one value.
//
// Its detail leads with the offending key, which is what the registered wording
// is about and so carries no caption. The reserved prefix, the Feature the
// binding routes to, and the shape a compliant binding is authored under follow,
// each captioned, because the registered wording names none of the three and the
// locator names the registry's own site rather than any of them.
var annotationBindingKeyAvoidsTheReservedPrefix = constraint.Check[
	constraint.DocumentView,
	constraint.DocumentAnnotationBindingSubject,
]{
	Sentinel: ErrManifestReservedPrefix(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.DocumentView,
		s constraint.DocumentAnnotationBindingSubject,
	) []constraint.Objection {
		feature, declared := v.ManifestAnnotationBindingFeature(
			s.SourceFile(), s.AnnotationBindingKey())
		if !declared {
			return nil
		}
		if !strings.HasPrefix(s.AnnotationBindingKey(), types.ReservedLabelPrefixAggregaAI) {
			return nil
		}
		return []constraint.Objection{s.RaiseWith(ErrManifestReservedPrefix(),
			constraint.Uncaptioned(s.AnnotationBindingKey()),
			constraint.Captioned("reserved prefix", types.ReservedLabelPrefixAggregaAI),
			constraint.Captioned("routing to Feature", feature),
			constraint.Captioned("author the binding under a domain of your own such as",
				"feature.<your-domain>/"),
		)}
	},
}
