// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package trace

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// connectionCtx is a render context whose one connection carries a credential
// attachment, beside an own attachment of the same Feature on the node.
func connectionCtx() *types.TemplateContext {
	ctx := makeCtx()
	conn := types.OrderedMap[types.TemplateAttachment]{}
	conn.Set("credential", types.TemplateAttachment{
		Type:     "feature.test/db-credential",
		Values:   om2("literalPassword", "conn-password-plaintext", "secretName", "conn-locator-public"),
		Provides: edgeProvides("password", "conn-provided-plaintext", "name", "conn-locator-public"),
	})
	uses := types.OrderedMap[types.EnrichedEdge]{}
	uses.Set("gpt", types.EnrichedEdge{Target: "openai", Socket: "models", Features: conn})
	ctx.Context.Uses = uses

	own := types.OrderedMap[types.TemplateAttachment]{}
	own.Set("admin_key", types.TemplateAttachment{
		Type:   "feature.test/db-credential",
		Values: om2("literalPassword", "own-password-plaintext"),
	})
	ctx.Features = own
	return ctx
}

// TestPillarSnapshot_WithholdsAConnectionAttachmentsDeclaredData runs the per-pass
// projection at every pass that shows the uses map and reads, per pass, a declared
// datum of the connection's attachment (withheld), an undeclared one beside it
// (kept), and the node's own attachment (not displayed at all, as before).
func TestPillarSnapshot_WithholdsAConnectionAttachmentsDeclaredData(t *testing.T) {
	t.Parallel()
	keys := redact.Keys{UsesAttachments: map[string]map[string]redact.AttachmentKeys{
		"gpt": {"credential": {
			Values:   map[string]bool{"literalPassword": true},
			Provides: map[string]bool{"password": true},
		}},
	}}

	for _, pass := range []Pass{PassPhaseA, PassPhaseB0, PassPhaseB1, PassPhaseB2, PassPhaseB3, PassPhaseB4, PassPhaseB6} {
		t.Run(string(pass), func(t *testing.T) {
			t.Parallel()
			snap := PillarSnapshot(connectionCtx(), pass, keys)

			shown, ok := snap["context.uses"].(types.OrderedMap[types.EnrichedEdge])
			if !ok {
				t.Fatalf("the uses projection must be present and stay an ordered edge map; got %T", snap["context.uses"])
			}
			edge, _ := shown.Get("gpt")
			attachment, _ := edge.Features.Get("credential")

			rows := []struct {
				name string
				got  string
				want string
			}{
				{"declared values leaf", stringAt(attachment.Values, "literalPassword"), redact.Placeholder()},
				{"declared provides key", providesAt(attachment.Provides, "password"), redact.Placeholder()},
				{"undeclared values leaf", stringAt(attachment.Values, "secretName"), "conn-locator-public"},
				{"undeclared provides key", providesAt(attachment.Provides, "name"), "conn-locator-public"},
				{"attachment type", attachment.Type, "feature.test/db-credential"},
			}
			for _, r := range rows {
				if r.got != r.want {
					t.Errorf("%s: got %q, want %q", r.name, r.got, r.want)
				}
			}

			// The node's own attachments are not part of this projection, so their
			// data is absent from it rather than withheld in it, exactly as before.
			if _, present := snap["features"]; present {
				t.Errorf("the per-pass projection must not show the node's own attachments; got keys %v", keysOf(snap))
			}
		})
	}
}

// TestPillarSnapshot_ConnectionViewUnderAnEmptySetIsShownAsIs is the control half:
// the projection withholds because the set names the datum, never because the
// datum sits on a connection.
func TestPillarSnapshot_ConnectionViewUnderAnEmptySetIsShownAsIs(t *testing.T) {
	t.Parallel()
	snap := PillarSnapshot(connectionCtx(), PassPhaseA, redact.Keys{})
	shown, _ := snap["context.uses"].(types.OrderedMap[types.EnrichedEdge])
	edge, _ := shown.Get("gpt")
	attachment, _ := edge.Features.Get("credential")
	if got := stringAt(attachment.Values, "literalPassword"); got != "conn-password-plaintext" {
		t.Errorf("an empty set must show the connection view as it is; got %q", got)
	}
}

func stringAt(m types.OrderedMap[any], key string) string {
	v, _ := m.Get(key)
	s, _ := v.(string)
	return s
}

func providesAt(m types.OrderedMap[string], key string) string {
	v, _ := m.Get(key)
	return v
}
