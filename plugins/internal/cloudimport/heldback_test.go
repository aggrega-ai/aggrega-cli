// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// cacheGraph holds a cache of a type no row maps, a queue of another such
// type and the PostgreSQL server the table maps.
func cacheGraph() *Graph {
	return &Graph{Nodes: []*Node{
		{ID: "cache", Type: cacheType, Form: FormRaw, Name: text("cache-main"), Source: "main.json#/cache", Body: []Prop{
			leaf(text("westeurope"), "location"), leaf(text("Premium"), "properties", "sku", "name"),
			leaf(literal("6"), "properties", "sku", "capacity"),
		}},
		{ID: "queue", Type: namespaceType, Form: FormRaw, Name: text("orders"), Body: []Prop{leaf(text("westeurope"), "location")}},
		pgServer(),
	}}
}

// cacheLabels are the labels of the Component a resource-schema draft of the
// cache's type holds.
var cacheLabels = map[string]string{
	"component.aggrega.ai/category": "datastore",
	"component.aggrega.ai/type":     "cache",
}

func TestHeldBackWhenTheNameIsTaken(t *testing.T) {
	f := fixtureFormat()
	held := schemadraft.NameFor(fixtureView(""), f.Draft, cacheType)
	out, diags := Classify(fixtureView("", component(held, cacheLabels)), f, cacheGraph())
	noHalt(t, diags)

	c := classOf(t, out, "cache")
	if c.Class != ClassHeldBack || c.Name != held || len(c.Drafts) != 0 || c.Entity.Name != "" {
		t.Fatalf("cache is %s as %s with drafts %v and entity %+v; want held back under %s, nothing drafted",
			c.Class, c.Name, c.Drafts, c.Entity, held)
	}
	exists := withCode(diags, CodeComponentExists)
	if len(exists) != 1 || exists[0].Severity != aggrega.SeverityWarn || exists[0].SourceRef != "main.json#/cache" {
		t.Fatalf("component-exists %+v, want one warning on cache", exists)
	}
	mentions(t, "the warning", exists[0].Message, "cache", held, "mapping_overrides", cacheType, "infra", "Release")
	mentions(t, "the swap hint", c.Decision.SwapHint, "mapping_overrides", held)

	if q := classOf(t, out, "queue"); q.Class != ClassDrafted || len(q.Drafts) != 2 {
		t.Errorf("queue is %s with %d drafts, want drafted: the rest proceeds", q.Class, len(q.Drafts))
	}
	if s := classOf(t, out, "server"); s.Class != ClassMapped {
		t.Errorf("server is %s, want mapped: the rest proceeds", s.Class)
	}
}

func TestOverriddenTypeReferencesTheComponent(t *testing.T) {
	f := fixtureFormat()
	held := schemadraft.NameFor(fixtureView(""), f.Draft, cacheType)
	settings := `{"mapping_overrides": {"arm": {"` + cacheType + `": {"component": "` + held + `",
		"category": "datastore", "traits": {"component.aggrega.ai/type": "cache"}}}}}`
	out, diags := Classify(fixtureView(settings, component(held, cacheLabels)), f, cacheGraph())
	noHalt(t, diags)

	c := classOf(t, out, "cache")
	if c.Class != ClassMapped || c.Row != nil || c.Entity.Mode != "reference" || c.Entity.Name != held ||
		c.Name != held {
		t.Fatalf("cache is %s by %v as %+v, want mapped with no row, referencing %s", c.Class, c.Row, c.Entity, held)
	}
	if len(withCode(diags, CodeComponentExists)) != 0 {
		t.Errorf("the overridden cache is held back: %+v", diags)
	}
	paths := writtenPaths(c.Node)
	want := []string{"location", "properties.sku.name", "properties.sku.capacity"}
	if len(paths) != len(want) {
		t.Fatalf("the properties listed for the entry's comment are %q, want %q", paths, want)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("the properties listed for the entry's comment are %q, want %q", paths, want)
		}
	}
	mentions(t, "the rationale", c.Decision.Rationale, append([]string{"mapping_overrides", held}, want...)...)
}
