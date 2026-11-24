// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"encoding/json"
	"strings"
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

// remedyEntry returns the settings entry a component-exists warning prints.
func remedyEntry(t *testing.T, message string) string {
	t.Helper()
	_, rest, ok := strings.Cut(message, "the entry ")
	entry, _, closed := strings.Cut(rest, ", then fill")
	if !ok || !closed || !json.Valid([]byte(entry)) {
		t.Fatalf("the warning %q prints no settings entry", message)
	}
	return entry
}

// TestHeldBackRemedyReferencesTheComponentOnceApplied applies exactly the
// entry a held-back resource's warning prints, and the resource is mapped to
// the Component it names.
func TestHeldBackRemedyReferencesTheComponentOnceApplied(t *testing.T) {
	for _, tc := range []struct {
		name   string
		labels map[string]string
	}{
		{"category and type", cacheLabels},
		{"category alone", map[string]string{"component.aggrega.ai/category": "datastore"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := fixtureFormat()
			held := component(schemadraft.NameFor(fixtureView(""), f.Draft, cacheType), tc.labels)
			_, diags := Classify(fixtureView("", held), f, cacheGraph())
			exists := withCode(diags, CodeComponentExists)
			if len(exists) != 1 {
				t.Fatalf("component-exists %+v, want one", exists)
			}
			entry := remedyEntry(t, exists[0].Message)
			out, diags := Classify(fixtureView(entry, held), f, cacheGraph())
			noHalt(t, diags)
			c := classOf(t, out, "cache")
			if c.Class != ClassMapped || c.Entity.Mode != "reference" || c.Entity.Name != held.Name {
				t.Fatalf("with %s, cache is %s as %+v, want mapped referencing %s", entry, c.Class, c.Entity, held.Name)
			}
			if again := withCode(diags, CodeComponentExists); len(again) != 0 {
				t.Errorf("with the printed entry applied, component-exists %+v", again)
			}
		})
	}

	t.Run("no category label", func(t *testing.T) {
		f := fixtureFormat()
		held := component(schemadraft.NameFor(fixtureView(""), f.Draft, cacheType),
			map[string]string{"component.aggrega.ai/type": "cache"})
		_, diags := Classify(fixtureView("", held), f, cacheGraph())
		exists := withCode(diags, CodeComponentExists)
		if len(exists) != 1 {
			t.Fatalf("component-exists %+v, want one", exists)
		}
		mentions(t, "the warning", exists[0].Message, "carries no component.aggrega.ai/category label")
		remedyEntry(t, exists[0].Message)
	})
}
