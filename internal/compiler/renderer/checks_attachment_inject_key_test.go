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

// The attachment inject-key rule reads one declared fact — the attributed
// deliveries a node's injection pipe carries — and this file runs the declared
// value through the framework's own traversal.
//
// # Why the rows are driven through the traversal rather than the predicate
//
// The rule is invoked once per attachment on a node, and what it raises for one
// attachment depends on what every OTHER surface delivered into the same pipe. So
// a call straight into the predicate would prove the comparison and none of the
// attribution: a rule reading the whole node's record and raising for the wrong
// attachment satisfies every declaration assertion and reports the wrong author's
// block. Driving the traversal is what makes the subject the thing under test.
//
// # The stub embeds the view interface, and the nil embedding is the point
//
// injectDeliveryFactStub embeds [constraint.HydratedView] and overrides only the
// one accessor this rule reads. The embedded interface is nil, so any other
// accessor panics rather than returning a zero value — which turns "the predicate
// reads declared facts and nothing else" from a claim about the source into a
// property this file enforces.

// injectDeliveryFactStub answers the one fact the rule reads and nothing else.
type injectDeliveryFactStub struct {
	constraint.HydratedView

	deliveries map[string][]constraint.InjectDelivery
}

func (s injectDeliveryFactStub) NodeInjectDeliveries(node string) []constraint.InjectDelivery {
	return s.deliveries[node]
}

// injectDeliverySiteFixture implements the two enumerators the traversal needs.
//
// The attachment slice is indexed rather than ranged over a map, so the order the
// traversal observes comes from the slice and never from a map walk.
type injectDeliverySiteFixture struct {
	nodes       []constraint.NodeInstance
	attachments map[string][]string
}

func (f injectDeliverySiteFixture) Nodes() []constraint.NodeInstance { return f.nodes }

func (f injectDeliverySiteFixture) Attachments(node string) []string {
	return f.attachments[node]
}

// injectDeliveryHalt drives the declared rule over one node's attachments through
// the framework's own traversal and returns what it raised as the render's halt.
//
// It is the shape the build takes rather than a shortcut into it: the same
// traversal, the same narrowing value, the same rendering into a categorised halt.
func injectDeliveryHalt(
	t *testing.T, node string, attachments []string, deliveries []constraint.InjectDelivery,
) error {
	t.Helper()
	view := injectDeliveryFactStub{deliveries: map[string][]constraint.InjectDelivery{node: deliveries}}
	site := injectDeliverySiteFixture{
		nodes:       []constraint.NodeInstance{{ID: node, AuthoredName: node, SourceFile: "aggregate.yaml"}},
		attachments: map[string][]string{node: attachments},
	}
	return runNodeInjectDeliveryRule(
		context.Background().Err, view, site, site.nodes[0],
	)
}

// injectDeliveryFindings reads a halt's findings structurally rather than through
// its outermost text.
//
// The outermost frame of a halt joining several findings reports the envelope
// alone and drops what is underneath, so a substring assertion on it would pass on
// the envelope and see none of the detail. Both branches of the halt's
// count-branching construction are handled: one finding arrives on its own, and
// several arrive joined.
func injectDeliveryFindings(t *testing.T, halt error) []string {
	t.Helper()
	if halt == nil {
		return nil
	}
	var out []string
	var walk func(err error)
	walk = func(err error) {
		if joined, ok := err.(interface{ Unwrap() []error }); ok {
			for _, sub := range joined.Unwrap() {
				walk(sub)
			}
			return
		}
		var ce *cerrors.CompilerError
		if stderrors.As(err, &ce) && ce.Message() != "" {
			out = append(out, ce.Message())
			if inner := ce.Unwrap(); inner != nil {
				if _, joinedInner := inner.(interface{ Unwrap() []error }); joinedInner {
					out = out[:len(out)-1]
					walk(inner)
				}
			}
		}
	}
	walk(halt)
	return out
}

// delivered is one delivery that put a value under its key.
func delivered(mechanism, key, attachment string) constraint.InjectDelivery {
	return constraint.InjectDelivery{
		Mechanism: mechanism, DeliveryKey: key, Attachment: attachment, Delivered: true,
	}
}

// claimed is one delivery that put no value under its key — a duplicate, or a
// reference that resolved to nothing.
func claimed(mechanism, key, attachment string) constraint.InjectDelivery {
	return constraint.InjectDelivery{
		Mechanism: mechanism, DeliveryKey: key, Attachment: attachment,
	}
}

// TestAttachmentInjectKeyRule walks the rule over the rows that make it
// discriminate, refusals and silences together.
//
// The silent rows are not padding. A rule that refused everything would satisfy
// every refusal row here, and the only thing that can see it is an input that
// legitimately delivers attachment inject keys and has to keep rendering.
func TestAttachmentInjectKeyRule(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		attachments []string
		deliveries  []constraint.InjectDelivery
		// want is one expected finding per entry, each a substring pair the
		// message has to carry.
		want []string
	}{
		{
			name:        "REFUSAL: an attachment takes a key the component's own block delivered",
			attachments: []string{"db-creds"},
			deliveries: []constraint.InjectDelivery{
				delivered("env", "DB_PASSWORD", ""),
				claimed("env", "DB_PASSWORD", "db-creds"),
			},
			want: []string{`"DB_PASSWORD"|mechanism "env"`},
		},
		{
			name:        "REFUSAL: the second of two attachments takes the key the first delivered",
			attachments: []string{"alpha", "beta"},
			deliveries: []constraint.InjectDelivery{
				delivered("env", "TOKEN", "alpha"),
				claimed("env", "TOKEN", "beta"),
			},
			want: []string{`"TOKEN"|mechanism "env"`},
		},
		{
			name:        "REFUSAL: two keys duplicated by one attachment are two findings",
			attachments: []string{"db-creds"},
			deliveries: []constraint.InjectDelivery{
				delivered("env", "A", ""),
				delivered("file", "B", ""),
				claimed("env", "A", "db-creds"),
				claimed("file", "B", "db-creds"),
			},
			want: []string{`"A"|mechanism "env"`, `"B"|mechanism "file"`},
		},
		{
			name:        "REFUSAL: a custom mechanism is not a special case",
			attachments: []string{"db-creds"},
			deliveries: []constraint.InjectDelivery{
				delivered("ssm_param", "PATH", ""),
				claimed("ssm_param", "PATH", "db-creds"),
			},
			want: []string{`"PATH"|mechanism "ssm_param"`},
		},
		{
			name:        "SILENT: one attachment delivering keys nobody else asked for",
			attachments: []string{"db-creds"},
			deliveries: []constraint.InjectDelivery{
				delivered("env", "OTHER", ""),
				delivered("env", "DB_PASSWORD", "db-creds"),
			},
		},
		{
			name:        "SILENT: the same key under two different mechanisms is two spaces",
			attachments: []string{"db-creds"},
			deliveries: []constraint.InjectDelivery{
				delivered("env", "SHARED", ""),
				delivered("file", "SHARED", "db-creds"),
			},
		},
		{
			name:        "SILENT: two attachments each delivering their own key",
			attachments: []string{"alpha", "beta"},
			deliveries: []constraint.InjectDelivery{
				delivered("env", "A", "alpha"),
				delivered("env", "B", "beta"),
			},
		},
		{
			name: "SILENT: an attachment takes a key an EARLIER claim left free",
			// The earlier claim's reference resolved to nothing, so it put no
			// value under the key and the later delivery is the first to hold
			// it. A rule searching the answer rather than walking it in order
			// would refuse this pair.
			attachments: []string{"alpha", "beta"},
			deliveries: []constraint.InjectDelivery{
				claimed("env", "TOKEN", "alpha"),
				delivered("env", "TOKEN", "beta"),
			},
		},
		{
			name: "SILENT: an attachment's own claim colliding with a LATER attachment's",
			// Read from the far end this looks like a duplicate for alpha too.
			// It is not: alpha delivered first and holds the key, so only beta's
			// claim is refused — which the row above this one asserts.
			attachments: []string{"alpha"},
			deliveries: []constraint.InjectDelivery{
				delivered("env", "TOKEN", "alpha"),
				claimed("env", "TOKEN", "beta"),
			},
		},
		{
			name:        "SILENT: a node whose pipe carries nothing",
			attachments: []string{"db-creds"},
			deliveries:  nil,
		},
		{
			name:        "SILENT: an attachment with no claims of its own",
			attachments: []string{"db-creds"},
			deliveries: []constraint.InjectDelivery{
				delivered("env", "A", ""),
				delivered("env", "A2", ""),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			halt := injectDeliveryHalt(t, "api", tc.attachments, tc.deliveries)
			if len(tc.want) == 0 {
				if halt != nil {
					t.Fatalf("must stay silent; raised %v", halt)
				}
				return
			}
			if halt == nil {
				t.Fatalf("must refuse; stayed silent")
			}
			if !stderrors.Is(halt, ErrAttachmentInjectKeyCollision()) {
				t.Fatalf("halt must carry the condition; got %v", halt)
			}
			got := injectDeliveryFindings(t, halt)
			if len(got) != len(tc.want) {
				t.Fatalf("want %d finding(s), got %d: %q", len(tc.want), len(got), got)
			}
			for i, want := range tc.want {
				for _, part := range strings.Split(want, "|") {
					if !strings.Contains(got[i], part) {
						t.Errorf("finding %d must carry %q; got %q", i, part, got[i])
					}
				}
			}
		})
	}
}

// TestAttachmentInjectKeyRuleIsPositionedAtTheAttachment asserts the finding is
// positioned at the attachment's own entry inside the component's block, which is
// what the author opens to remove one of the two claims.
//
// The position is the whole reason the rule is invoked per attachment rather than
// per delivery key: a rule keyed by the key would be positioned at the component's
// own injection block, which for this condition is not necessarily where either
// claim was written.
func TestAttachmentInjectKeyRuleIsPositionedAtTheAttachment(t *testing.T) {
	t.Parallel()
	halt := injectDeliveryHalt(t, "api", []string{"db-creds"}, []constraint.InjectDelivery{
		delivered("env", "DB_PASSWORD", ""),
		claimed("env", "DB_PASSWORD", "db-creds"),
	})
	var ce *cerrors.CompilerError
	if !stderrors.As(halt, &ce) {
		t.Fatalf("halt is not a compiler diagnostic: %v", halt)
	}
	if ce.Category() != cerrors.DeveloperError {
		t.Errorf("category = %s, want %s", ce.Category(), cerrors.DeveloperError)
	}
	if ce.Source().File != "aggregate.yaml" {
		t.Errorf("source file = %q, want the document the component was authored in", ce.Source().File)
	}
	if want := ".spec.components.api.features.db-creds"; ce.Source().Locator != want {
		t.Errorf("locator = %q, want %q", ce.Source().Locator, want)
	}
}
