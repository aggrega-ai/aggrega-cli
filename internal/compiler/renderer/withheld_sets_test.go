// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for the two sets one assembly of a node's render context yields — the set
// a capture is withheld by and the set a failure's text is withheld by — and for
// the trace's re-render of a workload, read through the capture's.
package renderer

import (
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/trace"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// ownDeclarationsNode is a node whose own entities declare beyond its values: a
// values leaf and a provides key its Component declares, and the leaves the Feature
// attached to it declares in each of the attachment's three pillars.
func ownDeclarationsNode() nodeDeclaration {
	return nodeDeclaration{
		component: resolver.SensitiveLeaves{
			Values:   map[string]bool{"admin_token": true},
			Provides: map[string]bool{"session": true},
		},
		own: map[string]redact.AttachmentKeys{"admin_key": {
			Values:   map[string]bool{"origin": true},
			Infra:    map[string]bool{"rotation_token": true},
			Provides: map[string]bool{"secret": true},
		}},
	}
}

// ownAttachmentOnlyNode is a node whose own values declare nothing and whose
// attachment does: the shape where the capture's set holds nothing BUT the
// widening, so a capture set assembled without it re-renders nothing at all.
func ownAttachmentOnlyNode() nodeDeclaration {
	return nodeDeclaration{
		own: map[string]redact.AttachmentKeys{"admin_key": {Values: map[string]bool{"origin": true}}},
	}
}

// TestWithheldSets_TheCapturesSetIsTheFailuresThroughTheTraceMode is the table
// over the one call both sets come from. Each row names a node, the set the
// assembler built for it and the trace mode, and states which references the
// capture's set and the failure's reach; a withholding capture's set must be the
// failure's set exactly, and an unredacted capture's must be empty while the
// failure's is unchanged.
func TestWithheldSets_TheCapturesSetIsTheFailuresThroughTheTraceMode(t *testing.T) {
	t.Parallel()
	declared := []string{
		".features.admin_key.values.origin",
		".features.admin_key.infra.rotation_token",
		".features.admin_key.provides.secret",
		".provides.session",
		".values.admin_token",
	}
	undeclared := []string{
		".features.admin_key.values.key",
		".features.admin_key.infra.rotation_window",
		".features.admin_key.provides.name",
		".provides.host",
		".values.region",
	}

	cases := []struct {
		name      string
		node      nodeDeclaration
		assembled redact.Keys
		mode      trace.Mode
		// reached is what the failure's set must reach; the capture's set reaches
		// it too unless the mode is unredacted, where it reaches nothing.
		reached []string
		// emptyFailure says the node declares nothing, so neither set may name
		// anything: an entry kept for nothing would re-render every workload of a
		// node that declares nothing.
		emptyFailure bool
	}{
		{
			name:      "withholding, a node declaring its own attachment and its provides",
			node:      ownDeclarationsNode(),
			assembled: redact.Keys{Values: map[string]bool{"admin_token": true}},
			reached:   declared,
		},
		{
			name:      "unredacted, the same node",
			node:      ownDeclarationsNode(),
			assembled: redact.Keys{Values: map[string]bool{"admin_token": true}},
			mode:      trace.Mode{Unredacted: true},
			reached:   declared,
		},
		{
			name:    "withholding, a node whose only declaration is its attachment's",
			node:    ownAttachmentOnlyNode(),
			reached: []string{".features.admin_key.values.origin"},
		},
		{
			name:         "withholding, a node declaring nothing",
			emptyFailure: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			capture, failure := c.node.withheldSets(c.assembled, c.mode)

			if c.emptyFailure {
				if !failure.Empty() || !capture.Empty() {
					t.Errorf("a node declaring nothing must yield two empty sets; capture=%+v failure=%+v", capture, failure)
				}
				return
			}
			if c.mode.Unredacted {
				if !capture.Empty() {
					t.Errorf("an unredacted capture's set must be empty; got %+v", capture)
				}
			} else if !reflect.DeepEqual(capture, failure) {
				t.Errorf("a withholding capture's set must be the failure's set\ncapture: %+v\nfailure: %+v", capture, failure)
			}
			for _, ref := range c.reached {
				if !failure.ReachedBy([]string{ref}) {
					t.Errorf("the failure's set must reach %s", ref)
				}
				if got, want := capture.ReachedBy([]string{ref}), !c.mode.Unredacted; got != want {
					t.Errorf("the capture's set reaching %s = %v, want %v", ref, got, want)
				}
			}
			for _, ref := range undeclared {
				if failure.ReachedBy([]string{ref}) || capture.ReachedBy([]string{ref}) {
					t.Errorf("neither set may reach the undeclared %s", ref)
				}
			}
		})
	}
}

// ownDeclarationsContext is a render context carrying a datum at every position
// ownDeclarationsNode declares, beside an undeclared sibling at each.
func ownDeclarationsContext() *types.TemplateContext {
	values := types.OrderedMap[any]{}
	values.Set("admin_token", "node-token-plaintext")
	values.Set("region", "eu-west-1")
	provides := types.OrderedMap[string]{}
	provides.Set("session", "sess-node-token-plaintext")
	provides.Set("host", "router")
	ownValues := types.OrderedMap[any]{}
	ownValues.Set("origin", "own-origin-plaintext")
	ownValues.Set("key", "own-key-public")
	ownInfra := types.OrderedMap[any]{}
	ownInfra.Set("rotation_token", "own-rotation-plaintext")
	ownInfra.Set("rotation_window", "own-window-public")
	ownProvides := types.OrderedMap[string]{}
	ownProvides.Set("secret", "prov-own-origin-plaintext")
	ownProvides.Set("name", "own-key-public")
	features := types.OrderedMap[types.TemplateAttachment]{}
	features.Set("admin_key", types.TemplateAttachment{
		Type:     "core/referenced-secret",
		Values:   ownValues,
		Infra:    ownInfra,
		Provides: ownProvides,
	})
	return &types.TemplateContext{Values: values, Provides: provides, Features: features}
}

// ownDeclarationsWorkload reads every position ownDeclarationsContext carries
// except the node's own declared values leaf, so the re-render is triggered by the
// node's own attachment and its provides alone.
const ownDeclarationsWorkload = "apiVersion: aggrega.ai/v1alpha1\nkind: Workload\ncontainers:\n  main:\n    image: router:1.0\n" +
	"    variables:\n" +
	"      OWN_ORIGIN: '{{ .features.admin_key.values.origin }}'\n" +
	"      OWN_KEY: '{{ .features.admin_key.values.key }}'\n" +
	"      OWN_ROTATION: '{{ .features.admin_key.infra.rotation_token }}'\n" +
	"      OWN_WINDOW: '{{ .features.admin_key.infra.rotation_window }}'\n" +
	"      OWN_SECRET: '{{ .features.admin_key.provides.secret }}'\n" +
	"      OWN_NAME: '{{ .features.admin_key.provides.name }}'\n" +
	"      SESSION: '{{ .provides.session }}'\n" +
	"      HOST: '{{ .provides.host }}'\n" +
	"      REGION: '{{ .values.region }}'\n"

// TestRenderWorkloadForDisplay_WithholdsTheNodesOwnDeclarations is the trace's
// re-render read through the capture's set: the workload reading the node's own
// attachment and its provides is re-rendered, each declared datum reads as the
// replacement text and each undeclared sibling as rendered. Under the unredacted
// mode the reading answers no, so the emitted document is shown instead.
func TestRenderWorkloadForDisplay_WithholdsTheNodesOwnDeclarations(t *testing.T) {
	t.Parallel()
	ctx := ownDeclarationsContext()
	assembled := redact.Keys{Values: map[string]bool{"admin_token": true}}
	capture, _ := ownDeclarationsNode().withheldSets(assembled, trace.Mode{})

	if !workloadReadsWithheld(ownDeclarationsWorkload, capture) {
		t.Fatal("a workload reading the node's own declarations must be re-rendered")
	}
	shown, produced := renderWorkloadForDisplay("router", ownDeclarationsWorkload, ctx, capture, validator.WorkloadSlots{})
	if !produced {
		t.Fatal("the re-render must be produced")
	}
	main, ok := shown.Containers.Get("main")
	if !ok {
		t.Fatal("the re-render lost its container")
	}
	rows := []struct {
		variable string
		want     string
	}{
		{"OWN_ORIGIN", redact.Placeholder()},
		{"OWN_ROTATION", redact.Placeholder()},
		{"OWN_SECRET", redact.Placeholder()},
		{"SESSION", redact.Placeholder()},
		{"OWN_KEY", "own-key-public"},
		{"OWN_WINDOW", "own-window-public"},
		{"OWN_NAME", "own-key-public"},
		{"HOST", "router"},
		{"REGION", "eu-west-1"},
	}
	for _, r := range rows {
		if got, _ := main.Variables.Get(r.variable); got != r.want {
			t.Errorf("%s = %v, want %q", r.variable, got, r.want)
		}
	}

	// The live context is what the artifacts render from.
	attachment, _ := ctx.Features.Get("admin_key")
	if got, _ := attachment.Values.Get("origin"); got != "own-origin-plaintext" {
		t.Errorf("the live context's attachment must not be mutated; it now reads %v", got)
	}
	if got, _ := ctx.Provides.Get("session"); got != "sess-node-token-plaintext" {
		t.Errorf("the live context's provides must not be mutated; it now reads %v", got)
	}

	unredacted, _ := ownDeclarationsNode().withheldSets(assembled, trace.Mode{Unredacted: true})
	if workloadReadsWithheld(ownDeclarationsWorkload, unredacted) {
		t.Error("an unredacted capture must show the emitted document, so the reading must answer no")
	}
}
