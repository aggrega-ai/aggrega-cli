// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for the coordinates a set derives for the data it names, the reading of a
// template's references over them, and the one substitution of a template's data
// that writes at them.
package redact

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// templateTree is the plain-map tree a template reads v by. The template boundary
// keys every field by its json tag, so the JSON form of a render context is that
// tree, addressed exactly as a template reads it.
func templateTree(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var tree map[string]any
	if err := json.Unmarshal(raw, &tree); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return tree
}

// asTree is templateTree for any JSON-encodable value.
func asTree(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

// dig returns the value at a path in a tree, nil where the path does not resolve.
func dig(tree any, path ...string) any {
	cur := tree
	for _, seg := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[seg]
	}
	return cur
}

// parityContext is a render context carrying data at every position a set can
// name, beside an undeclared sibling at each, so a substitution reaching too far
// or not far enough differs from the display's appliers somewhere.
func parityContext() *types.TemplateContext {
	db := om("password", "pw-plaintext", "user", "app")
	ctx := &types.TemplateContext{
		Values: om("adminPassword", "admin-plaintext", "appName", "shop", "db", &db, "tuning", om("pool", 5)),
		Infra:  om("apiKey", "apikey-plaintext", "region", "eu-west-1"),
		Locals: sm("dsn", "postgres://app:pw-plaintext@db", "label", "shop"),
		Source: map[string]any{"url": "oci://reg/app:admin-plaintext", "version": "1.0.0"},
	}
	ctx.Context.Inject.Env = sm("DB_PASSWORD", "pw-plaintext", "DB_NAME", "shop")
	ctx.Context.Inject.File = sm("token.txt", "token-plaintext", "ca.pem", "ca-public")
	ctx.Context.Inject.All = sm("DB_PASSWORD", "pw-plaintext", "DB_NAME", "shop", "token.txt", "token-plaintext", "ca.pem", "ca-public")
	ctx.Context.Inject.Additional.Set("custom", sm("SECRET_HEADER", "header-plaintext", "PUBLIC_HEADER", "header-public"))
	ctx.Context.Uses = connectionEdges()
	return ctx
}

// parityKeys names, through the one assembler, a datum at every position
// parityContext carries one: declared leaves (one on an object), a derived local and
// source field, a delivery key under each typed and custom mechanism, and an edge's
// two halves.
func parityKeys() Keys {
	return NodeKeys(
		map[string]bool{"adminPassword": true, "db.password": true, "tuning": true},
		map[string]bool{"apiKey": true},
		types.OrderedMap[types.IRFeatureEntry]{},
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
		DeliveryKeys{"file": {"token.txt": true}, "env": {"DB_PASSWORD": true}},
		DeliveryKeys{"custom": {"SECRET_HEADER": true}},
		Derived{Locals: map[string]bool{"dsn": true}, Source: map[string]bool{"url": true}},
	)
}

// TestSubstituteTree_AgreesWithTheDisplayAppliers holds the one substitution of a
// template's data and the display's pillar appliers to one answer over one fixture.
// The two are separate implementations by necessity — the display keeps the author
// order a template's data does not — so their agreement is asserted per pillar
// rather than assumed: a coordinate the walker spells differently from the
// position an applier writes at fails here, naming the pillar.
func TestSubstituteTree_AgreesWithTheDisplayAppliers(t *testing.T) {
	t.Parallel()
	ctx := parityContext()
	keys := parityKeys()

	tree := templateTree(t, ctx)
	keys.SubstituteTree(tree)

	cases := []struct {
		name    string
		walker  any
		applier any
	}{
		{"values", dig(tree, "values"), asTree(t, Pillar(ctx.Values, keys.Values))},
		{"infra", dig(tree, "infra"), asTree(t, Pillar(ctx.Infra, keys.Infra))},
		{"locals", dig(tree, "locals"), asTree(t, Locals(ctx.Locals, keys.Locals))},
		{"source", dig(tree, "source"), asTree(t, Source(ctx.Source, keys.Source))},
		{"context.inject", dig(tree, "context", "inject"), asTree(t, keys.InjectPipe(ctx.Context.Inject))},
		{"context.uses", dig(tree, "context", "uses"), asTree(t, keys.Uses(ctx.Context.Uses))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if !reflect.DeepEqual(c.walker, c.applier) {
				t.Errorf("the substitution and the display's applier disagree about %s:\nwalker:  %v\napplier: %v",
					c.name, c.walker, c.applier)
			}
		})
	}

	// The fixture is not vacuous: the walker did withhold something at each pillar,
	// and kept an undeclared sibling at each.
	for _, at := range [][]string{
		{"values", "adminPassword"}, {"values", "db", "password"}, {"values", "tuning"},
		{"infra", "apiKey"}, {"locals", "dsn"}, {"source", "url"},
		{"context", "inject", "env", "DB_PASSWORD"}, {"context", "inject", "file", "token.txt"},
		{"context", "inject", "custom", "SECRET_HEADER"}, {"context", "inject", "all", "DB_PASSWORD"},
		{"context", "uses", "gpt", "provides", "zzTargetSecret"},
		{"context", "uses", "gpt", "features", "credential", "values", "literalPassword"},
	} {
		if got := dig(tree, at...); got != placeholder {
			t.Errorf("%s: want the placeholder, got %v", strings.Join(at, "."), got)
		}
	}
	for _, at := range [][]string{
		{"values", "appName"}, {"values", "db", "user"}, {"infra", "region"}, {"locals", "label"},
		{"source", "version"}, {"context", "inject", "env", "DB_NAME"}, {"context", "inject", "custom", "PUBLIC_HEADER"},
		{"context", "uses", "gpt", "provides", "endpoint"},
	} {
		if got := dig(tree, at...); got == placeholder || got == nil {
			t.Errorf("%s: an undeclared datum must survive, got %v", strings.Join(at, "."), got)
		}
	}
}

// TestSubstituteTree_RefusalWidening covers the two pillars a node's set is
// widened by before it governs a template's text — the node's own attachments and
// its declared provides — beside their undeclared siblings, and that an absent
// declared datum stays absent.
func TestSubstituteTree_RefusalWidening(t *testing.T) {
	t.Parallel()
	tree := map[string]any{
		"features": map[string]any{
			"admin_key": map[string]any{
				"type":     "core/referenced-secret",
				"values":   map[string]any{"origin": "own-origin-plaintext", "key": "password"},
				"provides": map[string]any{"name": "admin-secret"},
			},
		},
		"provides": map[string]any{"dsn": "postgres://app:pw@db", "host": "db"},
		"values":   map[string]any{},
	}
	keys := Keys{
		Features: map[string]AttachmentKeys{"admin_key": {Values: map[string]bool{"origin": true}}},
		Provides: map[string]bool{"dsn": true},
		Values:   map[string]bool{"unsetPassword": true},
	}
	keys.SubstituteTree(tree)

	cases := []struct {
		name string
		at   []string
		want any
	}{
		{"an own attachment's declared leaf", []string{"features", "admin_key", "values", "origin"}, placeholder},
		{"an own attachment's undeclared leaf", []string{"features", "admin_key", "values", "key"}, "password"},
		{"an own attachment's type", []string{"features", "admin_key", "type"}, "core/referenced-secret"},
		{"an own attachment's undeclared provides", []string{"features", "admin_key", "provides", "name"}, "admin-secret"},
		{"a declared provides key", []string{"provides", "dsn"}, placeholder},
		{"an undeclared provides key", []string{"provides", "host"}, "db"},
		{"a declared leaf the author left unset stays absent", []string{"values", "unsetPassword"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := dig(tree, c.at...); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

// ownDeclarationsKeys is a node's set as it governs a template's text: the set the
// one assembler builds — here the node's own declared values leaf — widened by what
// the Feature attached to the node itself declares and by the provides keys the
// node's Component declares.
func ownDeclarationsKeys() Keys {
	keys := NodeKeys(map[string]bool{"admin_token": true}, nil, types.OrderedMap[types.IRFeatureEntry]{},
		CrossNode{}, nil, nil, Derived{})
	keys.Features = map[string]AttachmentKeys{"admin_key": {
		Values:   map[string]bool{"origin": true, "rotation.token": true},
		Infra:    map[string]bool{"rotation_token": true},
		Provides: map[string]bool{"secret": true},
	}}
	keys.Provides = map[string]bool{"session": true}
	return keys
}

// ownDeclarationsTree is a template's data carrying a datum at every position
// ownDeclarationsKeys names, beside an undeclared sibling at each, and a second
// attachment whose leaves share the first one's names and are declared by nothing.
func ownDeclarationsTree() map[string]any {
	return map[string]any{
		"values":   map[string]any{"admin_token": "node-token-plaintext", "region": "eu-west-1"},
		"provides": map[string]any{"session": "sess-node-token-plaintext", "host": "router"},
		"features": map[string]any{
			"admin_key": map[string]any{
				"type": "core/referenced-secret",
				"values": map[string]any{
					"origin":   "own-origin-plaintext",
					"key":      "own-key-public",
					"rotation": map[string]any{"token": "own-nested-plaintext", "window": "7d"},
				},
				"infra":    map[string]any{"rotation_token": "own-rotation-plaintext", "rotation_window": "own-window-public"},
				"provides": map[string]any{"secret": "prov-own-origin-plaintext", "name": "own-key-public"},
			},
			"monitoring": map[string]any{
				"type":   "core/monitoring",
				"values": map[string]any{"origin": "monitoring-origin-public"},
			},
		},
	}
}

// TestKeys_NodesOwnDeclarations is the set's two readings over the pillars a node's
// own entities declare beyond its values and infra. Each row is a reference a
// template spells and the position it reads: whether the reference reaches a
// withheld datum, and what the one substitution leaves at that position. Every
// withheld row sits beside an undeclared row of the same shape, and the second
// attachment's leaves share the first one's names, so a reading keyed on a leaf's
// name rather than on the attachment it belongs to fails a row.
func TestKeys_NodesOwnDeclarations(t *testing.T) {
	t.Parallel()
	keys := ownDeclarationsKeys()
	tree := ownDeclarationsTree()
	keys.SubstituteTree(tree)

	attachment := []string{"features", "admin_key"}
	cases := []struct {
		name     string
		ref      string
		at       []string
		withheld bool
		kept     any
	}{
		{"an own attachment's declared values leaf", ".features.admin_key.values.origin", append(attachment, "values", "origin"), true, nil},
		{"its undeclared values leaf", ".features.admin_key.values.key", append(attachment, "values", "key"), false, "own-key-public"},
		{"an own attachment's declared nested leaf", ".features.admin_key.values.rotation.token", append(attachment, "values", "rotation", "token"), true, nil},
		{"its undeclared nested sibling", ".features.admin_key.values.rotation.window", append(attachment, "values", "rotation", "window"), false, "7d"},
		{"an own attachment's declared infra leaf", ".features.admin_key.infra.rotation_token", append(attachment, "infra", "rotation_token"), true, nil},
		{"its undeclared infra leaf", ".features.admin_key.infra.rotation_window", append(attachment, "infra", "rotation_window"), false, "own-window-public"},
		{"an own attachment's declared provides key", ".features.admin_key.provides.secret", append(attachment, "provides", "secret"), true, nil},
		{"its undeclared provides key", ".features.admin_key.provides.name", append(attachment, "provides", "name"), false, "own-key-public"},
		{"an own attachment's type", ".features.admin_key.type", append(attachment, "type"), false, "core/referenced-secret"},
		{"another attachment's leaf of the same name", ".features.monitoring.values.origin", []string{"features", "monitoring", "values", "origin"}, false, "monitoring-origin-public"},
		{"a provides key the Component declares", ".provides.session", []string{"provides", "session"}, true, nil},
		{"a provides key the Component leaves undeclared", ".provides.host", []string{"provides", "host"}, false, "router"},
		{"the node's own declared values leaf", ".values.admin_token", []string{"values", "admin_token"}, true, nil},
		{"the node's own undeclared values leaf", ".values.region", []string{"values", "region"}, false, "eu-west-1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := keys.ReachedBy([]string{c.ref}); got != c.withheld {
				t.Errorf("ReachedBy(%s) = %v, want %v", c.ref, got, c.withheld)
			}
			want := c.kept
			if c.withheld {
				want = placeholder
			}
			if got := dig(tree, c.at...); got != want {
				t.Errorf("the substitution left %v at %s, want %v", got, strings.Join(c.at, "."), want)
			}
		})
	}

	// A read of a whole object reaches every withheld datum beneath it, which is
	// what makes a template ranging over an attachment or over the provides
	// conservative rather than invisible. The control is an object holding nothing
	// declared.
	for _, ref := range []string{".features", ".features.admin_key", ".features.admin_key.infra", ".features.admin_key.provides", ".provides"} {
		if !keys.ReachedBy([]string{ref}) {
			t.Errorf("a read of %s must reach the withheld data beneath it", ref)
		}
	}
	if keys.ReachedBy([]string{".features.monitoring"}) {
		t.Error("a read of an attachment that declares nothing must reach nothing")
	}
}

// TestSubstituteTree_EmptySetIsIdentity: a set naming nothing writes nothing.
func TestSubstituteTree_EmptySetIsIdentity(t *testing.T) {
	t.Parallel()
	tree := templateTree(t, parityContext())
	before := asTree(t, tree)
	Keys{}.SubstituteTree(tree)
	if !reflect.DeepEqual(asTree(t, tree), before) {
		t.Error("an empty set must leave the tree as it was")
	}
}

// TestReachedBy_PerRoot is the reading over the same coordinates: each row is a
// reference the census spells, against the widened set, beside a control of the
// same shape that reaches nothing declared.
func TestReachedBy_PerRoot(t *testing.T) {
	t.Parallel()
	keys := parityKeys()
	keys.Features = map[string]AttachmentKeys{"admin_key": {Values: map[string]bool{"origin": true}, Provides: map[string]bool{"secret": true}}}
	keys.Provides = map[string]bool{"dsn": true}

	cases := []struct {
		name    string
		reached string
		control string
	}{
		{"a declared values leaf", ".values.adminPassword", ".values.appName"},
		{"a leaf beneath a declared object", ".values.tuning.pool", ".values.sizing.pool"},
		{"an object holding a declared leaf", ".values.db", ".values.cache"},
		{"a withheld local", ".locals.dsn", ".locals.label"},
		{"a withheld source field", ".source.url", ".source.version"},
		{"a delivery key", ".context.inject.env.DB_PASSWORD", ".context.inject.env.DB_NAME"},
		{"the merged view of a delivery key", ".context.inject.all.DB_PASSWORD", ".context.inject.all.DB_NAME"},
		{"a custom mechanism's delivery key", ".context.inject.custom.SECRET_HEADER", ".context.inject.custom.PUBLIC_HEADER"},
		{"the whole context", ".context", ".metadata"},
		{"an edge's declared provides key", ".context.uses.gpt.provides.zzTargetSecret", ".context.uses.plain.provides.zzTargetSecret"},
		{"a connection attachment's declared leaf", ".context.uses.gpt.features.credential.values.literalPassword", ".context.uses.gpt.features.credential.values.secretName"},
		{"an own attachment's declared leaf", ".features.admin_key.values.origin", ".features.admin_key.values.key"},
		{"an own attachment's declared provides key", ".features.admin_key.provides.secret", ".features.admin_key.provides.name"},
		{"an own attachment, whole", ".features.admin_key", ".features.monitoring"},
		{"a declared provides key", ".provides.dsn", ".provides.host"},
		// The census spells a template handing on the whole of its data as the data
		// root alone; the control is a field of the same depth naming nothing declared.
		{"the whole data handed on", ".", ".metadata"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if !keys.ReachedBy([]string{c.reached}) {
				t.Errorf("must reach a withheld datum: %s", c.reached)
			}
			if keys.ReachedBy([]string{c.control}) {
				t.Errorf("must NOT reach a withheld datum: %s", c.control)
			}
		})
	}
	if (Keys{}).ReachedBy([]string{".values.adminPassword"}) {
		t.Error("an empty set reaches nothing")
	}
	if (Keys{}).ReachedBy([]string{"."}) {
		t.Error("an empty set reaches nothing, not even through the whole data")
	}
}

// TestCoordinateRootsAreTheTemplateContextTags holds each root a coordinate is
// spelled with to the json tag of the render context field that carries the
// pillar, which is the name the template boundary keys it by.
func TestCoordinateRootsAreTheTemplateContextTags(t *testing.T) {
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
	pipe := reflect.TypeOf(types.InjectPipe{})
	edge := reflect.TypeOf(types.EnrichedEdge{})
	attachment := reflect.TypeOf(types.TemplateAttachment{})
	cases := []struct {
		root string
		tag  string
	}{
		{rootValues, tag(ctx, "Values")},
		{rootInfra, tag(ctx, "Infra")},
		{rootLocals, tag(ctx, "Locals")},
		{rootSource, tag(ctx, "Source")},
		{rootProvides, tag(ctx, "Provides")},
		{rootFeatures, tag(ctx, "Features")},
		{rootContext, tag(ctx, "Context")},
		{keyInject, tag(block, "Inject")},
		{keyUses, tag(block, "Uses")},
		{keyAll, tag(pipe, "All")},
		{rootProvides, tag(edge, "Provides")},
		{rootFeatures, tag(edge, "Features")},
		{rootValues, tag(attachment, "Values")},
		{rootInfra, tag(attachment, "Infra")},
		{rootProvides, tag(attachment, "Provides")},
	}
	for _, c := range cases {
		if c.root != c.tag {
			t.Errorf("coordinate root %q is not the template name %q", c.root, c.tag)
		}
	}
}
