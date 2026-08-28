// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	stderrors "errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// The injectMapping mechanism rule reads one declared fact — the pairing between
// the mechanisms a component declares and the mechanisms one attachment's
// rendered injectMapping output named — and this file runs the declared value
// through the framework's own traversal.
//
// # Why the rows are driven through the traversal rather than the predicate
//
// The rule is invoked once per attachment, and what it reports has to be
// attributed to the attachment whose mapping left the mechanism unmapped. A call
// straight into the predicate would prove the comparison and none of the
// attribution: a rule reading a sibling attachment's pairing and raising against
// this one satisfies every declaration assertion and sends the author to the
// wrong entry.
//
// # The stub embeds the view interface, and the nil embedding is the point
//
// injectMappingFactStub embeds [constraint.HydratedView] and overrides only the
// one accessor this rule reads. The embedded interface is nil, so any other
// accessor panics rather than returning a zero value — which turns "the predicate
// reads declared facts and nothing else" from a claim about the source into a
// property this file enforces.
//
// The enumerator fixture and the structural finding reader are the ones the
// sibling rule's corpus in this package already declares. They are about a node's
// attachment axis and about a halt's shape rather than about either rule, so a
// second pair here would be a copy that agrees today.

// injectMappingFactStub answers the one fact the rule reads and nothing else.
type injectMappingFactStub struct {
	constraint.HydratedView

	pairings map[string][]constraint.InjectMappingMechanism
}

func (s injectMappingFactStub) AttachmentInjectMappingMechanisms(
	node string, attachment string,
) []constraint.InjectMappingMechanism {
	return s.pairings[node+"\x00"+attachment]
}

// mapped is one declared mechanism the attachment's mapping named.
func mapped(mechanism, recipe string) constraint.InjectMappingMechanism {
	return constraint.InjectMappingMechanism{Mechanism: mechanism, Recipe: recipe, Mapped: true}
}

// unmapped is one declared mechanism the attachment's mapping did not name.
func unmapped(mechanism, recipe string) constraint.InjectMappingMechanism {
	return constraint.InjectMappingMechanism{Mechanism: mechanism, Recipe: recipe, Mapped: false}
}

// injectMappingHalt drives the declared rule over one node's attachments through
// the framework's own traversal and returns what it raised as the render's halt.
//
// It is the shape the build takes rather than a shortcut into it: the same
// traversal, the same narrowing value, the same rendering into a categorised
// halt.
func injectMappingHalt(
	t *testing.T, node string, attachments []string,
	pairings map[string][]constraint.InjectMappingMechanism,
) error {
	t.Helper()
	keyed := map[string][]constraint.InjectMappingMechanism{}
	for attachment, pairing := range pairings {
		keyed[node+"\x00"+attachment] = pairing
	}
	view := injectMappingFactStub{pairings: keyed}
	site := injectDeliverySiteFixture{
		nodes:       []constraint.NodeInstance{{ID: node, AuthoredName: node, SourceFile: "aggregate.yaml"}},
		attachments: map[string][]string{node: attachments},
	}
	return runInjectMappingMechanismRule(
		context.Background().Err, view, site, site.nodes[0],
	)
}

// TestInjectMappingMechanismRule walks the rule's whole corpus: the rows it must
// refuse and the rows it must pass over in silence.
//
// The silent rows are half the corpus rather than an afterthought. A rule that
// refused every attachment would satisfy every refusal row and would refuse every
// build; only a row that must NOT halt can see that.
func TestInjectMappingMechanismRule(t *testing.T) {
	t.Parallel()

	t.Run("REFUSAL: a declared mechanism the mapping does not name", func(t *testing.T) {
		t.Parallel()
		halt := injectMappingHalt(t, "api", []string{"db-creds"},
			map[string][]constraint.InjectMappingMechanism{
				"db-creds": {unmapped("ssm_param", "env-only-resolver")},
			})
		findings := injectDeliveryFindings(t, halt)
		if len(findings) != 1 {
			t.Fatalf("expected one finding; got %d: %v", len(findings), findings)
		}
		if !strings.Contains(findings[0], `"ssm_param"`) {
			t.Errorf("the finding must name the mechanism; got %q", findings[0])
		}
		if !strings.Contains(findings[0], `recipe "env-only-resolver"`) {
			t.Errorf("the finding must name the FeatureRecipe; got %q", findings[0])
		}
	})

	t.Run("REFUSAL: two declared mechanisms one attachment maps neither of", func(t *testing.T) {
		t.Parallel()
		halt := injectMappingHalt(t, "api", []string{"db-creds"},
			map[string][]constraint.InjectMappingMechanism{
				"db-creds": {
					unmapped("ssm_param", "env-only-resolver"),
					unmapped("vault_path", "env-only-resolver"),
				},
			})
		findings := injectDeliveryFindings(t, halt)
		if len(findings) != 2 {
			t.Fatalf("expected two findings, one per unmapped mechanism; got %d: %v", len(findings), findings)
		}
		// Declaration order, which is the order the component wrote the
		// mechanisms in and the order the author reads them back in.
		if !strings.Contains(findings[0], `"ssm_param"`) || !strings.Contains(findings[1], `"vault_path"`) {
			t.Errorf("findings must arrive in the component's declaration order; got %v", findings)
		}
	})

	t.Run("REFUSAL: only the second of two attachments leaves it unmapped", func(t *testing.T) {
		t.Parallel()
		halt := injectMappingHalt(t, "api", []string{"resolver", "db-creds"},
			map[string][]constraint.InjectMappingMechanism{
				"resolver": {mapped("ssm_param", "ssm-resolver")},
				"db-creds": {unmapped("ssm_param", "env-only-resolver")},
			})
		findings := injectDeliveryFindings(t, halt)
		if len(findings) != 1 {
			t.Fatalf("expected one finding, attributed to the second attachment; got %d: %v", len(findings), findings)
		}
		if !strings.Contains(findings[0], `recipe "env-only-resolver"`) {
			t.Errorf("the finding must name the mapping that left it unmapped; got %q", findings[0])
		}
	})

	t.Run("REFUSAL: a core mechanism the mapping does not name", func(t *testing.T) {
		t.Parallel()
		halt := injectMappingHalt(t, "api", []string{"db-creds"},
			map[string][]constraint.InjectMappingMechanism{
				"db-creds": {mapped("env", "partial-resolver"), unmapped("file", "partial-resolver")},
			})
		findings := injectDeliveryFindings(t, halt)
		if len(findings) != 1 {
			t.Fatalf("expected one finding for the unmapped core mechanism; got %d: %v", len(findings), findings)
		}
		if !strings.Contains(findings[0], `"file"`) {
			t.Errorf("the finding must name the unmapped mechanism; got %q", findings[0])
		}
	})

	t.Run("SILENT: every declared mechanism is named", func(t *testing.T) {
		t.Parallel()
		if halt := injectMappingHalt(t, "api", []string{"db-creds"},
			map[string][]constraint.InjectMappingMechanism{
				"db-creds": {mapped("ssm_param", "ssm-resolver"), mapped("env", "ssm-resolver")},
			}); halt != nil {
			t.Errorf("a mapping naming every declared mechanism must render; got %v", halt)
		}
	})

	t.Run("SILENT: an attachment whose mapping named no mechanism at all", func(t *testing.T) {
		t.Parallel()
		if halt := injectMappingHalt(t, "api", []string{"flat-resolver"},
			map[string][]constraint.InjectMappingMechanism{
				"flat-resolver": nil,
			}); halt != nil {
			t.Errorf("a flat output claims nothing, so nothing is unmapped; got %v", halt)
		}
	})

	t.Run("SILENT: an attachment with no record beside one fully mapped", func(t *testing.T) {
		t.Parallel()
		if halt := injectMappingHalt(t, "api", []string{"no-mapping", "resolver"},
			map[string][]constraint.InjectMappingMechanism{
				"resolver": {mapped("ssm_param", "ssm-resolver")},
			}); halt != nil {
			t.Errorf("an attachment binding a FeatureRecipe with no mapping is not a fault; got %v", halt)
		}
	})

	t.Run("SILENT: a node with no attachments", func(t *testing.T) {
		t.Parallel()
		if halt := injectMappingHalt(t, "api", nil, nil); halt != nil {
			t.Errorf("a node with no attachments has no mapping to read; got %v", halt)
		}
	})

	t.Run("SILENT: a component declaring no injection block", func(t *testing.T) {
		t.Parallel()
		if halt := injectMappingHalt(t, "api", []string{"db-creds"},
			map[string][]constraint.InjectMappingMechanism{
				"db-creds": {},
			}); halt != nil {
			t.Errorf("a component declaring nothing dictates nothing to map; got %v", halt)
		}
	})
}

// TestInjectMappingMechanismRuleIsPositionedAtTheAttachment asserts the finding
// is positioned at the attachment's own entry inside the component's block, and
// carries the catalog attribution.
//
// The position is the whole reason the rule is invoked per attachment: the
// mapping is authored in a FeatureRecipe the Aggregate does not contain, so the
// attachment's entry is the only position in the author's own file that reaches
// it. The FeatureRecipe is named in the detail for the same reason — the locator
// structurally cannot point at a document the Aggregate does not hold.
func TestInjectMappingMechanismRuleIsPositionedAtTheAttachment(t *testing.T) {
	t.Parallel()
	halt := injectMappingHalt(t, "api", []string{"db-creds"},
		map[string][]constraint.InjectMappingMechanism{
			"db-creds": {unmapped("ssm_param", "env-only-resolver")},
		})
	var ce *cerrors.CompilerError
	if !stderrors.As(halt, &ce) {
		t.Fatalf("halt is not a compiler diagnostic: %v", halt)
	}
	if ce.Category != cerrors.CatalogError {
		t.Errorf("category = %s, want %s", ce.Category, cerrors.CatalogError)
	}
	if ce.Source.File != "aggregate.yaml" {
		t.Errorf("source file = %q, want the document the component was authored in", ce.Source.File)
	}
	if want := ".spec.components.api.features.db-creds"; ce.Source.Locator != want {
		t.Errorf("locator = %q, want %q", ce.Source.Locator, want)
	}
	if !stderrors.Is(halt, ErrUnmappedCustomMechanism()) {
		t.Errorf("the halt must carry the condition's own identity; got %v", halt)
	}
}
