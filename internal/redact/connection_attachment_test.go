// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Tests for withholding the attachments written on a connection, which a uses
// edge displays under its features entry. Every withheld position sits beside a
// control on the same attachment that must survive, because a blanket replacement
// over the edge would pass every positive assertion on its own.
package redact

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// connectionEdges builds the uses map the table below displays: two connections
// carrying the SAME Feature under the SAME slot name, so a withholding keyed on the
// slot's spelling rather than on the alias would reach both, and a third
// connection carrying nothing.
func connectionEdges() types.OrderedMap[types.EnrichedEdge] {
	tuning := om("adminToken", "conn-admin-plaintext", "poolSize", 25)
	gptFeatures := types.OrderedMap[types.TemplateAttachment]{}
	gptFeatures.Set("credential", types.TemplateAttachment{
		Type:            "feature.test/db-credential",
		ResolvedVersion: "1.0.0",
		Labels:          sm("feature.aggrega.ai/kind", "secret"),
		Values:          om("literalPassword", "conn-password-plaintext", "secretName", "conn-locator-public", "tuning", &tuning),
		Infra:           om("apiKey", "conn-apikey-plaintext", "region", "eu-west-1"),
		Provides:        sm("password", "conn-provided-plaintext", "name", "conn-locator-public"),
	})
	nimFeatures := types.OrderedMap[types.TemplateAttachment]{}
	nimFeatures.Set("credential", types.TemplateAttachment{
		Type:     "feature.test/db-credential",
		Values:   om("literalPassword", "nim-password-visible"),
		Provides: sm("password", "nim-provided-visible"),
	})

	in := types.OrderedMap[types.EnrichedEdge]{}
	in.Set("gpt", types.EnrichedEdge{
		Target:    "openai",
		Socket:    "models",
		Component: "core/model",
		Provides:  sm("zzTargetSecret", "target-secret-plaintext", "endpoint", "https://api.example"),
		Values:    om("timeout", "30s"),
		Features:  gptFeatures,
	})
	in.Set("nim", types.EnrichedEdge{Target: "nvidia", Socket: "models", Features: nimFeatures})
	in.Set("plain", types.EnrichedEdge{Target: "echo", Socket: "models", Provides: sm("endpoint", "http://echo")})
	return in
}

// connectionKeys names what the gpt connection's credential Feature declares, and
// the gpt target's own declared provides key, so both halves of the edge applier
// are exercised in one pass.
func connectionKeys() Keys {
	return NodeKeys(nil, nil, types.OrderedMap[types.IRFeatureEntry]{},
		CrossNode{
			Provides: map[string]map[string]bool{"gpt": {"zzTargetSecret": true}},
			Attachments: map[string]map[string]AttachmentKeys{
				"gpt": {"credential": {
					Values:   map[string]bool{"literalPassword": true, "tuning.adminToken": true},
					Infra:    map[string]bool{"apiKey": true},
					Provides: map[string]bool{"password": true},
				}},
			},
		},
		nil, nil, Derived{})
}

// TestUses_WithholdsAConnectionAttachmentsDeclaredData is the applier half for the
// connection view: each row names one datum the displayed edges carry and what the
// display must show for it.
func TestUses_WithholdsAConnectionAttachmentsDeclaredData(t *testing.T) {
	t.Parallel()
	in := connectionEdges()
	out := connectionKeys().Uses(in)

	gptEdge, _ := out.Get("gpt")
	plainEdge, _ := out.Get("plain")
	nimEdge, _ := out.Get("nim")
	gpt, _ := gptEdge.Features.Get("credential")
	nim, _ := nimEdge.Features.Get("credential")
	anyAt := func(m types.OrderedMap[any], key string) any {
		v, _ := m.Get(key)
		return v
	}
	stringAt := func(m types.OrderedMap[string], key string) any {
		v, _ := m.Get(key)
		return v
	}
	nested := func(m types.OrderedMap[any], object, leaf string) any {
		v, _ := m.Get(object)
		sub, ok := v.(*types.OrderedMap[any])
		if !ok {
			return v
		}
		got, _ := sub.Get(leaf)
		return got
	}

	cases := []struct {
		name string
		got  any
		want any
	}{
		{"a declared values leaf is withheld", anyAt(gpt.Values, "literalPassword"), placeholder},
		{"a declared nested values leaf is withheld", nested(gpt.Values, "tuning", "adminToken"), placeholder},
		{"a declared infra leaf is withheld", anyAt(gpt.Infra, "apiKey"), placeholder},
		{"a declared provides key is withheld", stringAt(gpt.Provides, "password"), placeholder},
		{"an undeclared values leaf is kept", anyAt(gpt.Values, "secretName"), "conn-locator-public"},
		{"an undeclared nested sibling is kept", nested(gpt.Values, "tuning", "poolSize"), 25},
		{"an undeclared infra leaf is kept", anyAt(gpt.Infra, "region"), "eu-west-1"},
		{"an undeclared provides key is kept", stringAt(gpt.Provides, "name"), "conn-locator-public"},
		{"the attachment's type is kept", gpt.Type, "feature.test/db-credential"},
		{"the attachment's version is kept", gpt.ResolvedVersion, "1.0.0"},
		{"the attachment's labels are kept", stringAt(gpt.Labels, "feature.aggrega.ai/kind"), "secret"},
		{"the same slot on a connection nothing was named for is kept", anyAt(nim.Values, "literalPassword"), "nim-password-visible"},
		{"the same provides key on that connection is kept", stringAt(nim.Provides, "password"), "nim-provided-visible"},
		{"the target's declared provides key is still withheld", stringAt(gptEdge.Provides, "zzTargetSecret"), placeholder},
		{"the target's undeclared provides key is kept", stringAt(gptEdge.Provides, "endpoint"), "https://api.example"},
		{"the edge's per-connection values are kept", anyAt(gptEdge.Values, "timeout"), "30s"},
		{"a connection carrying no attachment is kept", stringAt(plainEdge.Provides, "endpoint"), "http://echo"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if c.got != c.want {
				t.Errorf("got %v, want %v", c.got, c.want)
			}
		})
	}

	// Author order survives at every level the applier rebuilds.
	if got := out.Keys(); len(got) != 3 || got[0] != "gpt" || got[2] != "plain" {
		t.Errorf("alias order must survive; got %v", got)
	}
	if got := gpt.Values.Keys(); len(got) != 3 || got[0] != "literalPassword" {
		t.Errorf("the attachment's values must keep their author order; got %v", got)
	}

	// The input is never mutated: it is the live context the artifacts render from.
	src, _ := in.Get("gpt")
	srcAttachment, _ := src.Features.Get("credential")
	if v, _ := srcAttachment.Values.Get("literalPassword"); v != "conn-password-plaintext" {
		t.Errorf("Uses must not mutate the connection view it was handed; it now reads %v", v)
	}
	if v, _ := srcAttachment.Provides.Get("password"); v != "conn-provided-plaintext" {
		t.Errorf("Uses must not mutate the attachment's provides; it now reads %v", v)
	}
}

// TestSubstituteTree_WithholdsTheConnectionViewAndNothingElse covers the
// substitution a re-rendered workload is evaluated against, over the template data
// tree of a render context. It reaches the connection view at the coordinates the
// set derives for it, and the node's own attachments — which this set names
// nothing about — pass through as they are.
func TestSubstituteTree_WithholdsTheConnectionViewAndNothingElse(t *testing.T) {
	t.Parallel()
	own := types.OrderedMap[types.TemplateAttachment]{}
	own.Set("admin_key", types.TemplateAttachment{
		Type:   "feature.test/db-credential",
		Values: om("literalPassword", "own-password-plaintext"),
	})
	ctx := &types.TemplateContext{
		Context:  types.ContextBlock{Uses: connectionEdges()},
		Features: own,
	}

	tree := templateTree(t, ctx)
	connectionKeys().SubstituteTree(tree)

	if v := dig(tree, "context", "uses", "gpt", "features", "credential", "values", "literalPassword"); v != placeholder {
		t.Errorf("the substitution must withhold the connection's declared leaf; got %v", v)
	}
	if v := dig(tree, "context", "uses", "gpt", "features", "credential", "values", "secretName"); v != "conn-locator-public" {
		t.Errorf("the substitution must keep the connection's undeclared leaf; got %v", v)
	}
	if v := dig(tree, "features", "admin_key", "values", "literalPassword"); v != "own-password-plaintext" {
		t.Errorf("the connection-view set names nothing about the node's own attachments, so they must pass through; got %v", v)
	}
	srcEdge, _ := ctx.Context.Uses.Get("gpt")
	srcConn, _ := srcEdge.Features.Get("credential")
	if v, _ := srcConn.Values.Get("literalPassword"); v != "conn-password-plaintext" {
		t.Errorf("the live context must not be mutated; it now reads %v", v)
	}
}

// TestKeys_AConnectionReadUnderItsSocket covers the one spelling of a connection
// two contexts do not share: an assertion reads it under its socket, at
// uses.<socket>.<alias>, where a render context reads it at context.uses.<alias>.
// Where the set names a connection's socket, the reading and the substitution
// reach its attachment's declared leaf at both positions; where it names none,
// the socket position is reached by nothing, so the row cannot pass on a set that
// withholds every connection by default.
func TestKeys_AConnectionReadUnderItsSocket(t *testing.T) {
	t.Parallel()
	tree := func() map[string]any {
		credential := func(password string) map[string]any {
			return map[string]any{
				"type":   "feature.test/db-credential",
				"values": map[string]any{"literalPassword": password, "secretName": "conn-locator-public"},
			}
		}
		return map[string]any{"uses": map[string]any{"models": map[string]any{
			"gpt": map[string]any{"target": "openai", "alias": "gpt", "features": map[string]any{"credential": credential("conn-password-plaintext")}},
			"nim": map[string]any{"target": "nvidia", "alias": "nim", "features": map[string]any{"credential": credential("nim-password-visible")}},
		}}}
	}
	placed := connectionKeys()
	placed.ConnectionSockets = map[string]string{"gpt": "models"}
	unplaced := connectionKeys()

	cases := []struct {
		name     string
		ref      string
		at       []string
		withheld bool
		kept     any
	}{
		{"the declared leaf under its socket", ".uses.models.gpt.features.credential.values.literalPassword",
			[]string{"uses", "models", "gpt", "features", "credential", "values", "literalPassword"}, true, nil},
		{"its undeclared sibling", ".uses.models.gpt.features.credential.values.secretName",
			[]string{"uses", "models", "gpt", "features", "credential", "values", "secretName"}, false, "conn-locator-public"},
		{"the same slot on a connection the set names nothing for", ".uses.models.nim.features.credential.values.literalPassword",
			[]string{"uses", "models", "nim", "features", "credential", "values", "literalPassword"}, false, "nim-password-visible"},
		{"the connection's identity", ".uses.models.gpt.target",
			[]string{"uses", "models", "gpt", "target"}, false, "openai"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := placed.ReachedBy([]string{c.ref}); got != c.withheld {
				t.Errorf("ReachedBy(%s) = %v, want %v", c.ref, got, c.withheld)
			}
			substituted := tree()
			placed.SubstituteTree(substituted)
			want := c.kept
			if c.withheld {
				want = placeholder
			}
			if got := dig(substituted, c.at...); got != want {
				t.Errorf("the substitution left %v at %v, want %v", got, c.at, want)
			}
			if unplaced.ReachedBy([]string{c.ref}) {
				t.Errorf("a set naming no socket reached %s", c.ref)
			}
			untouched := tree()
			unplaced.SubstituteTree(untouched)
			if got, before := dig(untouched, c.at...), dig(tree(), c.at...); got != before {
				t.Errorf("a set naming no socket substituted %v at %v", got, c.at)
			}
		})
	}

	// A read of the socket, or of the whole connection, reaches the withheld leaf
	// beneath it; the render context's spelling of the same connection is reached
	// as it always is, whether or not a socket is named.
	for _, ref := range []string{".uses", ".uses.models", ".uses.models.gpt", ".uses.models.gpt.features.credential"} {
		if !placed.ReachedBy([]string{ref}) {
			t.Errorf("a read of %s must reach the withheld leaf beneath it", ref)
		}
	}
	for _, keys := range []Keys{placed, unplaced} {
		if !keys.ReachedBy([]string{".context.uses.gpt.features.credential.values.literalPassword"}) {
			t.Error("the render context's spelling of the connection must stay reached")
		}
	}
}

// TestNodeKeys_KeepsOnlyConnectionAttachmentsThatDeclareSomething pins the
// assembly's filter. A connection attachment whose Feature declares nothing must
// leave the set empty, because the set's emptiness is what a substituted context
// and a re-rendered workload are built behind: an empty entry kept here would
// re-render every workload of a node that declares nothing.
func TestNodeKeys_KeepsOnlyConnectionAttachmentsThatDeclareSomething(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		attachments map[string]map[string]AttachmentKeys
		wantEmpty   bool
		wantSlot    bool
	}{
		{"no connection attachment", nil, true, false},
		{"an attachment whose Feature declares nothing", map[string]map[string]AttachmentKeys{"gpt": {"credential": {}}}, true, false},
		{"an attachment whose Feature declares empty sets", map[string]map[string]AttachmentKeys{
			"gpt": {"credential": {Values: map[string]bool{}, Infra: map[string]bool{}, Provides: map[string]bool{}}},
		}, true, false},
		{"an attachment declaring a values leaf", map[string]map[string]AttachmentKeys{
			"gpt": {"credential": {Values: map[string]bool{"literalPassword": true}}},
		}, false, true},
		{"an attachment declaring only a provides key", map[string]map[string]AttachmentKeys{
			"gpt": {"credential": {Provides: map[string]bool{"password": true}}},
		}, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			keys := NodeKeys(nil, nil, types.OrderedMap[types.IRFeatureEntry]{},
				CrossNode{Attachments: c.attachments}, nil, nil, Derived{})
			if keys.Empty() != c.wantEmpty || keys.AnyWithheld() == c.wantEmpty {
				t.Errorf("Empty()=%v AnyWithheld()=%v, want empty=%v", keys.Empty(), keys.AnyWithheld(), c.wantEmpty)
			}
			if _, present := keys.UsesAttachments["gpt"]["credential"]; present != c.wantSlot {
				t.Errorf("slot present=%v, want %v; got %v", present, c.wantSlot, keys.UsesAttachments)
			}
		})
	}
}

// TestUses_ConnectionViewEmptySetIsIdentity is the invisibility control: a set
// naming nothing hands the uses map back as it is, so a node whose connections
// carry nothing declared displays exactly what it displayed before.
func TestUses_ConnectionViewEmptySetIsIdentity(t *testing.T) {
	t.Parallel()
	in := connectionEdges()
	out := Keys{}.Uses(in)
	edge, _ := out.Get("gpt")
	a, _ := edge.Features.Get("credential")
	if v, _ := a.Values.Get("literalPassword"); v != "conn-password-plaintext" {
		t.Errorf("an empty set must leave the connection view untouched; got %v", v)
	}
}
