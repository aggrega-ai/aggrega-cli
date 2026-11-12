// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Tests for what a template's failure may say when the template read a value an
// author declared sensitive from inside a list — a boundary's connections and
// attachments, the connections made to a node. Each entry is governed by its own
// entity, so every withheld row sits beside a row reading the same key off an entry
// whose entity declares nothing, and beside rows that must be byte-identical to the
// same failure evaluated with nothing declared.
package renderer

import (
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

const (
	// The declared-sensitive values the list fixtures carry, and the value of the
	// same provides key on an entry whose target declares nothing.
	listTargetSecret     = "list-target-secret-plaintext"
	listAttachmentSecret = "list-attachment-token-plaintext"
	listUndeclaredRef    = "cache-ref-public"
)

// listEntryStrings is an ordered string map of key, value pairs.
func listEntryStrings(kv ...string) types.OrderedMap[string] {
	out := types.OrderedMap[string]{}
	for i := 0; i+1 < len(kv); i += 2 {
		out.Set(kv[i], kv[i+1])
	}
	return out
}

// listEntryInfra is an infra pillar of key, value pairs.
func listEntryInfra(kv ...string) types.OrderedMap[any] {
	out := types.OrderedMap[any]{}
	for i := 0; i+1 < len(kv); i += 2 {
		out.Set(kv[i], kv[i+1])
	}
	return out
}

// listEntryDeclarations is a graph of three nodes whose Components declare
// differently — the store declares its secret_ref provides key sensitive, the web
// and the cache declare nothing — and a Feature declaring its token infra leaf.
func listEntryDeclarations() listDeclarations {
	graph := &types.IRGraph{}
	graph.Spec.Nodes.Set("web", types.IRNode{Component: "test/web"})
	graph.Spec.Nodes.Set("store", types.IRNode{Component: "test/store"})
	graph.Spec.Nodes.Set("cache", types.IRNode{Component: "test/cache"})
	return listDeclarations{
		graph: graph,
		component: func(componentType string) resolver.SensitiveLeaves {
			if componentType == "test/store" {
				return resolver.SensitiveLeaves{Provides: map[string]bool{"secret_ref": true}}
			}
			return resolver.SensitiveLeaves{}
		},
		feature: func(featureType string) resolver.SensitiveLeaves {
			if featureType == "test/guard" {
				return resolver.SensitiveLeaves{Infra: map[string]bool{"token": true}}
			}
			return resolver.SensitiveLeaves{}
		},
	}
}

// listEntryBoundaryContext is a boundary-scope context whose view lists two
// connections publishing the same provides keys — one to the store, one to the
// cache — a crossing connection to the store, and an attachment carrying a
// declared infra leaf beside an undeclared one.
func listEntryBoundaryContext() *types.TemplateContext {
	toStore := types.EnrichedEdge{Source: "web", Target: "store", Socket: "db",
		Provides: listEntryStrings("host", "store-host", "secret_ref", listTargetSecret)}
	toCache := types.EnrichedEdge{Source: "web", Target: "cache", Socket: "cache",
		Provides: listEntryStrings("host", "cache-host", "secret_ref", listUndeclaredRef)}
	return &types.TemplateContext{
		Values: types.OrderedMap[any]{},
		Locals: types.OrderedMap[string]{},
		Boundary: &types.BoundaryContext{
			Path:                           "shop",
			BoundaryName:                   "shop",
			InternalUsesEdges:              []types.EnrichedEdge{toStore, toCache},
			OutgoingCrossBoundaryUsesEdges: []types.EnrichedEdge{toStore},
			CrossBoundaryUsesEdges:         []types.EnrichedEdge{toStore},
			FeatureAttachments: []types.TemplateAttachment{{
				Type:  "test/guard",
				Infra: listEntryInfra("token", listAttachmentSecret, "region", "eu-west-1"),
			}},
		},
	}
}

// listEntryFR is a boundary-scope FeatureRecipe whose locals read a connection's
// provides directly, read that local again, and read nothing declared.
func listEntryFR() *types.FeatureRecipe {
	fr := &types.FeatureRecipe{}
	fr.Spec.Locals = listEntryStrings(
		"ref", `{{ range .boundary.internal_uses_edges }}{{ .provides.secret_ref }}{{ end }}`,
		"shouted", `{{ .locals.ref | upper }}`,
		"label", `{{ .boundary.path }}`,
	)
	return fr
}

// TestBoundaryFRRefusal_WithholdsADeclaredValueInsideAList is the boundary-scope
// table, over the set the boundary-scope render builds. byteIdentical rows must
// produce exactly the failure the same template produces with nothing declared;
// every row must name no declared value and carry what want states, so no row
// passes on a failure text that says nothing.
func TestBoundaryFRRefusal_WithholdsADeclaredValueInsideAList(t *testing.T) {
	t.Parallel()
	ctx := listEntryBoundaryContext()
	keys := boundaryFRKeys(listEntryFR(), resolver.SensitiveLeaves{}, ctx.Boundary, listEntryDeclarations())
	placeholder := redact.Placeholder()

	cases := []struct {
		name          string
		body          string
		byteIdentical bool
		want          string
	}{
		{
			name: "a refusal ranging the connections quotes a declared provides value",
			body: `{{- range $e := .boundary.internal_uses_edges }}{{ with $e.provides }}{{ with .secret_ref }}` +
				`{{ if not (hasPrefix "ref-" .) }}{{ fail (printf "target secret_ref %q must start with ref-" .) }}{{ end }}` +
				`{{ end }}{{ end }}{{ end -}}`,
			want: `target secret_ref "` + placeholder + `" must start with ref-`,
		},
		{
			name: "a refusal indexing the connection whose target declares the key",
			body: `{{- fail (printf "store ref %s" (index .boundary.internal_uses_edges 0).provides.secret_ref) -}}`,
			want: "store ref " + placeholder,
		},
		{
			name:          "the same key on a connection whose target declares nothing",
			body:          `{{- fail (printf "cache ref %s" (index .boundary.internal_uses_edges 1).provides.secret_ref) -}}`,
			byteIdentical: true,
			want:          "cache ref " + listUndeclaredRef,
		},
		{
			name:          "an undeclared key on the connection whose target declares one",
			body:          `{{- fail (printf "store host %s" (index .boundary.internal_uses_edges 0).provides.host) -}}`,
			byteIdentical: true,
			want:          "store host store-host",
		},
		{
			name: "a refusal over the outgoing crossing connections",
			body: `{{- range .boundary.outgoing_cross_boundary_uses_edges }}{{ fail (printf "crossing %s" .provides.secret_ref) }}{{ end -}}`,
			want: "crossing " + placeholder,
		},
		{
			name: "a refusal over every crossing connection",
			body: `{{- range .boundary.cross_boundary_uses_edges }}{{ fail (printf "crossing %s" .provides.secret_ref) }}{{ end -}}`,
			want: "crossing " + placeholder,
		},
		{
			name: "a refusal quoting a boundary attachment's declared infra leaf",
			body: `{{- range .boundary.feature_attachments }}{{ fail (printf "token %s" .infra.token) }}{{ end -}}`,
			want: "token " + placeholder,
		},
		{
			name:          "a boundary attachment's undeclared infra leaf",
			body:          `{{- range .boundary.feature_attachments }}{{ fail (printf "region %s" .infra.region) }}{{ end -}}`,
			byteIdentical: true,
			want:          "region eu-west-1",
		},
		{
			name:          "a refusal reading no list",
			body:          `{{- fail (printf "boundary %s" .boundary.path) -}}`,
			byteIdentical: true,
			want:          "boundary shop",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := listEntryFailure(t, c.body, ctx, keys)
			if c.byteIdentical {
				if plain := listEntryFailure(t, c.body, ctx, redact.Keys{}); got != plain {
					t.Errorf("must be byte-identical to the failure with nothing declared:\n got: %s\nwant: %s", got, plain)
				}
			}
			for _, secret := range []string{listTargetSecret, listAttachmentSecret} {
				if strings.Contains(got, secret) {
					t.Errorf("the failure quotes a declared value: %s", got)
				}
			}
			if !strings.Contains(got, c.want) {
				t.Errorf("the failure must carry %q, got: %s", c.want, got)
			}
		})
	}
}

// listEntryFailure evaluates body against ctx under keys and returns the whole
// text a reader is shown: the surface wraps the template's failure in a frame of
// its own, so the cause chain, not the frame, carries what the template said.
func listEntryFailure(t *testing.T, body string, ctx *types.TemplateContext, keys redact.Keys) string {
	t.Helper()
	_, err := EvalTemplate("edge-guard", body, ctx, keys)
	if err == nil {
		t.Fatal("the template must fail")
	}
	return cerrors.CauseChain(err)
}

// TestBoundaryFRKeys_LocalsComputedFromAListEntry: a local whose template reads a
// declared entry is withheld, and so is a local reading that local; a local reading
// nothing declared is not, and a refusal quoting the withheld one names no value.
func TestBoundaryFRKeys_LocalsComputedFromAListEntry(t *testing.T) {
	t.Parallel()
	ctx := listEntryBoundaryContext()
	keys := boundaryFRKeys(listEntryFR(), resolver.SensitiveLeaves{}, ctx.Boundary, listEntryDeclarations())

	if want := map[string]bool{"ref": true, "shouted": true}; !reflect.DeepEqual(keys.Lists.Locals, want) {
		t.Fatalf("locals computed from a list entry = %v, want %v", keys.Lists.Locals, want)
	}
	ctx.Locals = listEntryStrings("ref", listTargetSecret+listUndeclaredRef,
		"shouted", strings.ToUpper(listTargetSecret+listUndeclaredRef), "label", "shop")
	got := listEntryFailure(t, `{{- fail (printf "derived %s" .locals.shouted) -}}`, ctx, keys)
	if strings.Contains(strings.ToLower(got), listTargetSecret) || !strings.Contains(got, "derived "+redact.Placeholder()) {
		t.Errorf("a refusal quoting a local computed from a declared entry must carry the placeholder and name no value, got: %s", got)
	}
	label := listEntryFailure(t, `{{- fail (printf "label %s" .locals.label) -}}`, ctx, keys)
	plain := listEntryFailure(t, `{{- fail (printf "label %s" .locals.label) -}}`, ctx, redact.Keys{})
	if label != plain || !strings.Contains(label, "label shop") {
		t.Errorf("a local computed from nothing declared must fail byte-identically, quoting it:\n got: %s\nwant: %s", label, plain)
	}
}

// TestAttachmentRefusal_WithholdsACallersDeclaredProvides: the connections made to
// a node, which an attached Feature's context lists, each governed by the
// connecting node's Component.
func TestAttachmentRefusal_WithholdsACallersDeclaredProvides(t *testing.T) {
	t.Parallel()
	ctx := &types.TemplateContext{Values: types.OrderedMap[any]{}}
	ctx.Context.UsedBy = []types.InboundEdge{
		{ComponentID: "store", Socket: "db", Provides: listEntryStrings("secret_ref", listTargetSecret)},
		{ComponentID: "cache", Socket: "db", Provides: listEntryStrings("secret_ref", listUndeclaredRef)},
	}
	keys := redact.Keys{Lists: redact.ListKeys{Entries: usedByListEntries(ctx.Context.UsedBy, listEntryDeclarations())}}

	got := listEntryFailure(t,
		`{{- range .context.used_by }}{{ fail (printf "caller %s ref %s" .component_id .provides.secret_ref) }}{{ end -}}`, ctx, keys)
	if strings.Contains(got, listTargetSecret) || !strings.Contains(got, "caller store ref "+redact.Placeholder()) {
		t.Errorf("a caller's declared provides must be withheld, its identity kept, got: %s", got)
	}
	body := `{{- fail (printf "caller ref %s" (index .context.used_by 1).provides.secret_ref) -}}`
	undeclared := listEntryFailure(t, body, ctx, keys)
	plain := listEntryFailure(t, body, ctx, redact.Keys{})
	if undeclared != plain || !strings.Contains(undeclared, "caller ref "+listUndeclaredRef) {
		t.Errorf("a caller declaring nothing must fail byte-identically, quoting its value:\n got: %s\nwant: %s", undeclared, plain)
	}
}

// TestListEntries_LeaveThePluginMarkingAsItWas: what a set names inside lists
// governs what a failure says and never the marking a rendering plugin is handed,
// so a template reading a list marks nothing it did not mark before.
func TestListEntries_LeaveThePluginMarkingAsItWas(t *testing.T) {
	t.Parallel()
	ctx := listEntryBoundaryContext()
	keys := boundaryFRKeys(listEntryFR(), resolver.SensitiveLeaves{}, ctx.Boundary, listEntryDeclarations())
	for _, body := range []string{
		`{{ range .boundary.internal_uses_edges }}{{ .provides.secret_ref }}{{ end }}`,
		`{{ .locals.shouted }}`,
	} {
		if !templateReadsWithheld(body, keys) {
			t.Errorf("the failure reading must reach %s", body)
		}
		if templateReadsWithheld(body, keys.PluginMarking()) {
			t.Errorf("the plugin marking must not reach %s", body)
		}
	}
}

// TestListPathsAreTheTemplateContextTags holds each list path to the json tags of
// the fields a template reads the list through.
func TestListPathsAreTheTemplateContextTags(t *testing.T) {
	t.Parallel()
	tag := func(typ reflect.Type, field string) string {
		f, ok := typ.FieldByName(field)
		if !ok {
			t.Fatalf("%s has no field %s", typ.Name(), field)
		}
		return strings.Split(f.Tag.Get("json"), ",")[0]
	}
	ctx := reflect.TypeOf(types.TemplateContext{})
	block := reflect.TypeOf(types.ContextBlock{})
	boundary := reflect.TypeOf(types.BoundaryContext{})
	at := func(root reflect.Type, rootField string, typ reflect.Type, field string) string {
		return tag(root, rootField) + "." + tag(typ, field)
	}
	cases := []struct {
		path string
		want string
	}{
		{listBoundaryInternalEdges, at(ctx, "Boundary", boundary, "InternalUsesEdges")},
		{listBoundaryOutgoingCrossingEdges, at(ctx, "Boundary", boundary, "OutgoingCrossBoundaryUsesEdges")},
		{listBoundaryIncomingCrossingEdges, at(ctx, "Boundary", boundary, "IncomingCrossBoundaryUsesEdges")},
		{listBoundaryCrossingEdges, at(ctx, "Boundary", boundary, "CrossBoundaryUsesEdges")},
		{listBoundaryAttachments, at(ctx, "Boundary", boundary, "FeatureAttachments")},
		{listUsedBy, at(ctx, "Context", block, "UsedBy")},
	}
	for _, c := range cases {
		if c.path != c.want {
			t.Errorf("list path %q is not the template path %q", c.path, c.want)
		}
	}
}
