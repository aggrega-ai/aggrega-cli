// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// writtenPlace is one place the cascade recorded, on the given surface and in the
// given scope, naming the attachment's type or not.
func writtenPlace(surface, scope string, namesType bool) attachmentPlace {
	document := constraint.AuthoringDocumentAggregate
	if surface == constraint.ContributionReleaseBoundary || surface == constraint.ContributionReleaseComponent {
		document = constraint.AuthoringDocumentRelease
	}
	site := constraint.AuthoredSite{ScopePath: scope}
	if surface == constraint.ContributionAggregateComponent || surface == constraint.ContributionReleaseComponent {
		site.Component = "app"
	}
	return attachmentPlace{
		contribution: constraint.AttachmentContribution{Surface: surface, ScopePath: scope, Component: site.Component},
		site:         constraint.AttachmentSiteInBlock(document, site, "metrics", document+".yaml"),
		namesType:    namesType,
	}
}

// annotationPlace is a place annotation-driven expansion produced, which names
// no block.
func annotationPlace() attachmentPlace {
	return attachmentPlace{
		contribution: constraint.AttachmentContribution{
			Surface: constraint.ContributionAnnotation, AnnotationKey: "example.com/metrics",
		},
		namesType: true,
	}
}

// TestAnAttachmentWrittenInSeveralBlocksIsPositionedByOneRule proves which of the
// blocks that wrote an attachment positions it, as a whole and per pillar.
//
// As a whole: where its type was written; among those, the developer's before
// the operator's and on each side the component's own block before a scope's,
// the innermost of one scope chain. Per pillar: the pillar author's narrowest
// block, and where that author wrote none, the attachment's own position. A place
// annotation-driven expansion produced is never chosen, so an attachment only an
// annotation produced records no block at all.
func TestAnAttachmentWrittenInSeveralBlocksIsPositionedByOneRule(t *testing.T) {
	t.Parallel()

	aggregateComponent := constraint.ContributionAggregateComponent
	subsystem := constraint.ContributionSubsystem
	releaseComponent := constraint.ContributionReleaseComponent
	releaseBoundary := constraint.ContributionReleaseBoundary

	type chosen struct {
		surface, scope string
		recorded       bool
	}
	for _, row := range []struct {
		name   string
		places []attachmentPlace
		whole  chosen
		values chosen
		infra  chosen
	}{
		{
			name: "the developer types it and the operator sizes it",
			places: []attachmentPlace{
				writtenPlace(aggregateComponent, "", true),
				writtenPlace(releaseComponent, "prod", false),
			},
			whole:  chosen{aggregateComponent, "", true},
			values: chosen{aggregateComponent, "", true},
			infra:  chosen{releaseComponent, "prod", true},
		},
		{
			name: "both documents name the type",
			places: []attachmentPlace{
				writtenPlace(aggregateComponent, "", true),
				writtenPlace(releaseComponent, "prod", true),
			},
			whole:  chosen{aggregateComponent, "", true},
			values: chosen{aggregateComponent, "", true},
			infra:  chosen{releaseComponent, "prod", true},
		},
		{
			name: "the Release alone, a boundary typing it and the component's entry sizing it",
			places: []attachmentPlace{
				writtenPlace(releaseBoundary, "prod", true),
				writtenPlace(releaseComponent, "prod", false),
			},
			whole:  chosen{releaseBoundary, "prod", true},
			values: chosen{releaseBoundary, "prod", true},
			infra:  chosen{releaseComponent, "prod", true},
		},
		{
			name: "the Release alone, both its blocks naming the type",
			places: []attachmentPlace{
				writtenPlace(releaseBoundary, "prod", true),
				writtenPlace(releaseComponent, "prod", true),
			},
			whole:  chosen{releaseComponent, "prod", true},
			values: chosen{releaseComponent, "prod", true},
			infra:  chosen{releaseComponent, "prod", true},
		},
		{
			name: "the component's own block and a subsystem both write it",
			places: []attachmentPlace{
				writtenPlace(aggregateComponent, "", true),
				writtenPlace(subsystem, "tier", true),
			},
			whole:  chosen{aggregateComponent, "", true},
			values: chosen{aggregateComponent, "", true},
			infra:  chosen{aggregateComponent, "", true},
		},
		{
			name: "two levels of one subsystem chain write it",
			places: []attachmentPlace{
				writtenPlace(subsystem, "outer", true),
				writtenPlace(subsystem, "outer.inner", true),
			},
			whole:  chosen{subsystem, "outer.inner", true},
			values: chosen{subsystem, "outer.inner", true},
			infra:  chosen{subsystem, "outer.inner", true},
		},
		{
			name: "an annotation produced it and the Release wrote it explicitly",
			places: []attachmentPlace{
				annotationPlace(),
				writtenPlace(releaseComponent, "prod", true),
			},
			whole:  chosen{releaseComponent, "prod", true},
			values: chosen{releaseComponent, "prod", true},
			infra:  chosen{releaseComponent, "prod", true},
		},
		{
			name:   "only an annotation produced it",
			places: []attachmentPlace{annotationPlace()},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			for _, pillar := range []struct {
				name string
				want chosen
			}{
				{name: "", want: row.whole},
				{name: pillarValues, want: row.values},
				{name: pillarInfra, want: row.infra},
			} {
				place, recorded := positionedPlace(row.places, pillar.name)
				got := chosen{place.contribution.Surface, place.contribution.ScopePath, recorded}
				if !recorded {
					got = chosen{}
				}
				if got != pillar.want {
					t.Errorf("pillar %q: positioned at %+v; want %+v", pillar.name, got, pillar.want)
				}
			}
		})
	}
}
