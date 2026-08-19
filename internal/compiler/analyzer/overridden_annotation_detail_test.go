// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// The finding this proof is about, in an author's terms.
//
// A deployment addresses a component a composition contributed by the qualified
// identifier the composition gave it, and that identifier carries the separator a
// position divides its segments with. The position therefore stops at the
// components block and gives up the component and everything written under it, so
// what an author is handed is a block and a suppressed annotation — with nothing
// saying which of the components under that block, which attachment on it, or
// which key inside that attachment's configuration.
//
// The rule makes that up in its detail. These assertions are the whole rendered
// warning rather than the values inside it, because the defect this repairs is
// exactly a message whose values are all present and none distinguishable: an
// annotation key, a component key, an attachment name and a configuration key are
// bare or dotted words alike, so a value-by-value check passes on the row of
// quoted strings an author cannot read.

// suppressedAt is the per-leaf provenance of a leaf an explicit value won,
// carrying the annotation that lost.
func suppressedAt(key, source string) constraint.LeafProvenance {
	return constraint.LeafProvenance{
		Origin:                    "explicit",
		DisplacesAnnotation:       true,
		DisplacedAnnotationKey:    key,
		DisplacedAnnotationSource: source,
	}
}

// leafProvenanceView answers the two facts the rule reads and nothing else.
//
// The embedded interface is nil, which is deliberate: a double implementing the
// whole view would let the rule come to read a third fact without this proof
// noticing, whereas a nil embed makes any such read a panic naming the method. So
// the double asserts what the rule consults as well as what it is told.
//
// The site it answers the component fact from is the SAME value the axis below
// hands the traversal, because that is the coupling the compilation has: one
// projection of per-leaf sites feeds both the position the traversal builds and
// the fact the rule reads. Two independent values here would let this proof pass
// while the two came apart.
type leafProvenanceView struct {
	constraint.GraphView
	provenance map[string]constraint.LeafProvenance
	site       constraint.AuthoredSite
}

func (v leafProvenanceView) PerLeafAttachmentProvenance(
	node, attachment, pillar, leaf string,
) constraint.LeafProvenance {
	return v.provenance[strings.Join([]string{node, attachment, pillar, leaf}, "\x00")]
}

func (v leafProvenanceView) PerLeafAttachmentAuthoredComponent(
	node, attachment, pillar, leaf string,
) string {
	return v.site.Component
}

// enumeratedAttachmentLeaf is the axis the rule is invoked over: one node, one
// attachment, one pillar, one leaf, with the site the entry was written at.
//
// The attachment name is a field rather than a constant because a deployment's
// attachment names admit the separator a position divides its segments with,
// while an application's do not — so one of the shapes below truncates on the
// attachment instead of on the component, and the axis has to be able to spell
// both.
type enumeratedAttachmentLeaf struct {
	node       constraint.NodeInstance
	attachment string
	site       constraint.AuthoredSite
	file       string
}

func (e enumeratedAttachmentLeaf) Nodes() []constraint.NodeInstance {
	return []constraint.NodeInstance{e.node}
}

func (e enumeratedAttachmentLeaf) Attachments(string) []string { return []string{e.attachment} }

func (e enumeratedAttachmentLeaf) AttachmentPillars(string, string) []constraint.AuthoredCoordinate {
	return []constraint.AuthoredCoordinate{{Value: "infra", SourceFile: e.file}}
}

func (e enumeratedAttachmentLeaf) AttachmentPillarLeaves(string, string, string) []constraint.AttachmentLeaf {
	return []constraint.AttachmentLeaf{{Value: "region", Site: e.site}}
}

// TestTheOverriddenAnnotationAdvisoryNamesTheCoordinatesThePositionGaveUp drives
// the rule the build registers, not a copy of it, so a rule that stopped naming a
// coordinate fails here.
func TestTheOverriddenAnnotationAdvisoryNamesTheCoordinatesThePositionGaveUp(t *testing.T) {
	t.Parallel()

	const (
		annotation  = "aggrega.ai/audit"
		onComponent = "releases/prod.yaml:boundaries.prod.components.billing.api.annotations.aggrega.ai/audit"
		onBoundary  = "releases/prod.yaml:boundaries.prod.annotations.aggrega.ai/audit"
		registered  = "annotation contribution overridden by an explicit value"
	)

	composed := constraint.NodeInstance{
		ID:            "billing.api",
		SourceFile:    "billing/aggregate.yaml",
		AuthoredName:  "api",
		SubsystemPath: "billing",
	}

	for _, tc := range []struct {
		name       string
		axis       enumeratedAttachmentLeaf
		declaredAt string
		want       string
	}{
		{
			// The shape a composed deployment reaches. The position stops at the
			// components block, so every coordinate under it reaches the author
			// only through the detail.
			name: "a component the deployment keys by its qualified identifier",
			axis: enumeratedAttachmentLeaf{
				node:       composed,
				attachment: "audit",
				site:       constraint.AuthoredSite{ScopePath: "prod", Component: "billing.api"},
				file:       "releases/prod.yaml",
			},
			declaredAt: onComponent,
			want: `releases/prod.yaml at .spec.boundaries.prod.components: ` + registered +
				` ("` + annotation + `", component "billing.api", attachment "audit", pillar "infra", ` +
				`key "region", annotation declared at "` + onComponent + `")`,
		},
		{
			// The entry that names no component. Its position is whole, and the
			// component is absent from the detail rather than filled in from the
			// node's identifier: the entry takes effect on every component the
			// scope holds and names none of them, so a component here would point
			// at somewhere nobody wrote.
			name: "an entry written scope-wide, whose position is whole",
			axis: enumeratedAttachmentLeaf{
				node:       composed,
				attachment: "audit",
				site:       constraint.AuthoredSite{ScopePath: "prod"},
				file:       "releases/prod.yaml",
			},
			declaredAt: onComponent,
			want: `releases/prod.yaml at .spec.boundaries.prod.features.audit.infra.region: ` + registered +
				` ("` + annotation + `", attachment "audit", pillar "infra", key "region", ` +
				`annotation declared at "` + onComponent + `")`,
		},
		{
			// The cell neither row above reaches: the coordinate the position gives
			// up is the ATTACHMENT rather than the component, because a
			// deployment's attachment names admit the separator too. The position
			// stops at the attachments block, there is no component to name, and
			// the attachment reaches the author only through the detail.
			//
			// It also carries an annotation inherited from the scope above, so the
			// annotation's own site names no component — which is what makes the
			// two values distinguishable rather than one of them a copy of the
			// other.
			name: "an attachment the deployment names with a separator in it",
			axis: enumeratedAttachmentLeaf{
				node:       composed,
				attachment: "audit.v2",
				site:       constraint.AuthoredSite{ScopePath: "prod"},
				file:       "releases/prod.yaml",
			},
			declaredAt: onBoundary,
			want: `releases/prod.yaml at .spec.boundaries.prod.features: ` + registered +
				` ("` + annotation + `", attachment "audit.v2", pillar "infra", key "region", ` +
				`annotation declared at "` + onBoundary + `")`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			view := leafProvenanceView{
				provenance: map[string]constraint.LeafProvenance{
					strings.Join([]string{"billing.api", tc.axis.attachment, "infra", "region"}, "\x00"): suppressedAt(annotation, tc.declaredAt),
				},
				site: tc.axis.site,
			}
			raised, err := constraint.RunOnNodeAttachmentPillarLeaves(
				nil, tc.axis, tc.axis, tc.axis, tc.axis,
				constraint.GraphView(view), OverriddenAnnotationAdvisory,
			)
			if err != nil {
				t.Fatalf("the traversal returned %v with no cancellation signal supplied", err)
			}
			if len(raised) != 1 {
				t.Fatalf("the rule raised %d advisory/advisories over one displaced leaf; want 1 — "+
					"with none there is nothing below to read, and a rule that never fired reads "+
					"exactly like one that ran and approved", len(raised))
			}

			// Through the conversion the build uses, so the position an author sees
			// is part of what is asserted rather than something read separately.
			got := AdvisoriesFromObjections(raised)
			if len(got) != 1 || got[0] != tc.want {
				t.Errorf("the advisory reads\n  %s\nwant\n  %s", strings.Join(got, "\n  "), tc.want)
			}
		})
	}

	t.Run("a leaf nothing displaced raises nothing", func(t *testing.T) {
		t.Parallel()

		// The calibration. Every assertion above is about what the rule says when it
		// fires; this is what makes them mean anything, because a rule that fired on
		// every leaf would satisfy them all and flood a build with advisories about
		// values nobody overrode.
		axis := enumeratedAttachmentLeaf{
			node:       composed,
			attachment: "audit",
			site:       constraint.AuthoredSite{ScopePath: "prod", Component: "billing.api"},
			file:       "releases/prod.yaml",
		}
		raised, err := constraint.RunOnNodeAttachmentPillarLeaves(
			nil, axis, axis, axis, axis,
			constraint.GraphView(leafProvenanceView{
				provenance: map[string]constraint.LeafProvenance{},
				site:       axis.site,
			}),
			OverriddenAnnotationAdvisory,
		)
		if err != nil {
			t.Fatalf("the traversal returned %v with no cancellation signal supplied", err)
		}
		if len(raised) != 0 {
			t.Errorf("the rule raised %d advisory/advisories over a leaf carrying no displacement: %v",
				len(raised), AdvisoriesFromObjections(raised))
		}
	})
}
