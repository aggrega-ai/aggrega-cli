// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// allowlistCatalog declares four sockets whose only difference is the shape of
// their inject declaration, plus one on a Feature. The Component's `open` socket
// and the Feature's `peer` socket carry DIFFERENT allowlists, so a check that
// consults the wrong entity for a Feature-authored edge produces the wrong verdict
// rather than accidentally the right one.
//
//   - open      allowlist [env file]  — dictation permitted, up to those two
//   - narrow    allowlist [env]       — one member, so a second mechanism is outside
//   - opaque    no inject key at all  — the Black-Box declaration
//   - emptied   inject: []            — the same declaration written explicitly
//
// `wild` is declared on neither entity: it is the ad-hoc case.
func allowlistCatalog() resolver.CatalogLookup {
	compUses := types.OrderedMap[types.UsesSchemaEntry]{}
	compUses.Set("open", types.UsesSchemaEntry{Selector: "a=b", Inject: []string{"env", "file"}})
	compUses.Set("narrow", types.UsesSchemaEntry{Selector: "a=b", Inject: []string{"env"}})
	compUses.Set("opaque", types.UsesSchemaEntry{Selector: "a=b"})
	compUses.Set("emptied", types.UsesSchemaEntry{Selector: "a=b", Inject: []string{}})

	featUses := types.OrderedMap[types.UsesSchemaEntry]{}
	featUses.Set("peer", types.UsesSchemaEntry{Selector: "a=b", Inject: []string{"arg"}})
	featUses.Set("open", types.UsesSchemaEntry{Selector: "a=b"})

	return resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			if typeURI == "core/consumer" {
				return types.Component{
					Metadata: types.ComponentMetadata{Name: typeURI, Version: "1.0.0"},
					Spec:     types.ComponentSpec{UsesSchema: compUses},
				}, true
			}
			if typeURI == "core/target" {
				return types.Component{
					Metadata: types.ComponentMetadata{Name: typeURI, Version: "1.0.0"},
				}, true
			}
			return types.Component{}, false
		},
		Feature: func(typeURI string) (types.Feature, bool) {
			if typeURI == "core/sidecar" {
				return types.Feature{
					Metadata: types.FeatureMetadata{Name: typeURI, Version: "1.0.0"},
					Spec:     types.FeatureSpec{UsesSchema: featUses},
				}, true
			}
			return types.Feature{}, false
		},
	}
}

func envInject() *types.InjectBlock {
	return &types.InjectBlock{Env: map[string]any{"DB_URL": "host"}}
}

// buildOneEdge drives the real IR builder over a single edge, which is the site
// the allowlist verdict is taken at.
func buildOneEdge(t *testing.T, e topology.NormalizedEdge) error {
	t.Helper()
	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", &topology.TopologyNode{InstanceID: "api", ComponentType: "core/consumer"})
	nodes.Set("db", &topology.TopologyNode{InstanceID: "db", ComponentType: "core/target"})

	_, _, _, err := BuildIR(BuildIRConfig{
		Topology: &topology.TopologyResult{
			Nodes:              nodes,
			Edges:              []topology.NormalizedEdge{e},
			FeatureAttachments: map[string][]topology.FeatureAttachment{},
		},
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test-app", Version: "1.0.0"}},
			Manifest:      &types.Manifest{Metadata: types.ManifestMetadata{Version: "1.0.0"}},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{"api": {BoundaryPath: "local/default"}},
		},
		CatalogLookup:   allowlistCatalog(),
		CompilerVersion: "0.1.0",
	})
	return err
}

// TestBuildIR_UsesInjectAllowlist enumerates the CELLS of the check's grid rather
// than the margins of its axes. The axes are the socket's inject declaration
// (non-empty allowlist / absent / explicitly empty / socket not declared at all),
// whether the edge dictates anything, whether a dictated mechanism is a member,
// and which entity owns the socket. Each row states the cell it occupies, because
// a defect in the interior — a Feature-owned socket with an absent allowlist, say —
// is reachable while every per-axis row passes.
//
// Every rejected mechanism name is a real one with a single character removed, so a
// row cannot pass because the compiler rejected a string no author would type.
func TestBuildIR_UsesInjectAllowlist(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		edge topology.NormalizedEdge
		// wantHalt is the empty string when the edge must build.
		wantHalt []string
	}{
		{
			name: "declared allowlist, dictated mechanism is a member: builds",
			edge: topology.NormalizedEdge{Source: "api", Target: "db", Socket: "open", Alias: "d1",
				Inject: envInject()},
		},
		{
			name: "declared allowlist, dictated mechanism is not a member: halts naming both",
			edge: topology.NormalizedEdge{Source: "api", Target: "db", Socket: "open", Alias: "d2",
				Inject: &types.InjectBlock{Additional: additional("fil", map[string]any{"F": "host"})}},
			wantHalt: []string{`mechanism "fil"`, `socket "open"`, `[env file]`, `component "api"`, `core/consumer`},
		},
		{
			name: "one-member allowlist, a second core mechanism is outside it: halts",
			edge: topology.NormalizedEdge{Source: "api", Target: "db", Socket: "narrow", Alias: "d3",
				Inject: &types.InjectBlock{File: map[string]any{"P": "host"}}},
			wantHalt: []string{`mechanism "file"`, `socket "narrow"`, `[env]`},
		},
		{
			name: "one-member allowlist, mixed dictation: halts on the outside one only",
			edge: topology.NormalizedEdge{Source: "api", Target: "db", Socket: "narrow", Alias: "d4",
				Inject: &types.InjectBlock{Env: map[string]any{"A": "host"}, Arg: map[string]any{"B": "host"}}},
			wantHalt: []string{`mechanism "arg"`},
		},
		{
			name: "socket declares no inject key at all, edge dictates: halts as Black-Box",
			edge: topology.NormalizedEdge{Source: "api", Target: "db", Socket: "opaque", Alias: "d5",
				Inject: envInject()},
			wantHalt: []string{`socket "opaque"`, "declares no inject allowlist", "Black-Box", `component "api"`},
		},
		{
			name: "socket declares no inject key, the SAME edge without dictation: builds",
			edge: topology.NormalizedEdge{Source: "api", Target: "db", Socket: "opaque", Alias: "d6"},
		},
		{
			name: "socket declares an explicitly empty allowlist, edge dictates: halts as Black-Box",
			edge: topology.NormalizedEdge{Source: "api", Target: "db", Socket: "emptied", Alias: "d7",
				Inject: envInject()},
			wantHalt: []string{`socket "emptied"`, "declares no inject allowlist"},
		},
		{
			name: "declared allowlist, no dictation: builds",
			edge: topology.NormalizedEdge{Source: "api", Target: "db", Socket: "open", Alias: "d8"},
		},
		{
			name: "ad-hoc socket, edge dictates: builds, because no declaration was made",
			edge: topology.NormalizedEdge{Source: "api", Target: "db", Socket: "wild", Alias: "d9",
				Inject: envInject()},
		},
		{
			name: "Feature-owned socket, dictated mechanism is a member: builds",
			edge: topology.NormalizedEdge{Source: "api", Target: "db", Socket: "peer", Alias: "d10",
				OriginFeature: "core/sidecar", OriginFeatureInstance: "side",
				Inject: &types.InjectBlock{Arg: map[string]any{"A": "host"}}},
		},
		{
			name: "Feature-owned socket, dictated mechanism is outside ITS allowlist: halts against the Feature",
			edge: topology.NormalizedEdge{Source: "api", Target: "db", Socket: "peer", Alias: "d11",
				OriginFeature: "core/sidecar", OriginFeatureInstance: "side",
				Inject: envInject()},
			wantHalt: []string{`mechanism "env"`, `socket "peer"`, `[arg]`, `feature type`, "core/sidecar",
				`attachment "side"`},
		},
		{
			// The interior cell: `open` carries an allowlist on the Component and NO
			// inject key on the Feature. A check consulting the consuming Component for
			// a Feature-authored edge would permit this.
			name: "Feature-owned socket with no inject key, shadowing a Component socket that has one: halts",
			edge: topology.NormalizedEdge{Source: "api", Target: "db", Socket: "open", Alias: "d12",
				OriginFeature: "core/sidecar", OriginFeatureInstance: "side",
				Inject: envInject()},
			wantHalt: []string{`socket "open"`, "declares no inject allowlist", "core/sidecar"},
		},
		{
			// The mirror interior cell: `narrow` is on the Component only. A check
			// consulting the Feature for an Aggregate-authored edge would call it ad-hoc
			// and permit anything.
			name: "Component-owned socket absent from the Feature, Aggregate-authored: halts against the Component",
			edge: topology.NormalizedEdge{Source: "api", Target: "db", Socket: "narrow", Alias: "d13",
				Inject: &types.InjectBlock{Arg: map[string]any{"B": "host"}}},
			wantHalt: []string{`mechanism "arg"`, `socket "narrow"`, "core/consumer"},
		},
		{
			name: "Release-wired edge into a Black-Box socket: halts, and the locator names the Release",
			edge: topology.NormalizedEdge{Source: "api", Target: "db", Socket: "opaque", Alias: "d14",
				OriginRelease: true, Inject: envInject()},
			wantHalt: []string{"Release component", "declares no inject allowlist"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := buildOneEdge(t, tc.edge)
			if len(tc.wantHalt) == 0 {
				if err != nil {
					t.Fatalf("edge must build, got halt: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("edge must halt, but the build succeeded")
			}
			if !errors.Is(err, ErrUsesInjectForbidden()) {
				t.Fatalf("halt must carry ErrUsesInjectForbidden, got %v", err)
			}
			for _, want := range tc.wantHalt {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("halt must name %q; got %q", want, err.Error())
				}
			}
		})
	}
}

// additional builds a one-entry Additional map for a custom mechanism name.
func additional(mech string, payload map[string]any) types.OrderedMap[any] {
	m := types.OrderedMap[any]{}
	m.Set(mech, payload)
	return m
}

// TestBuildIR_UsesInjectAllowlist_ReportsEveryOffendingEdge pins the collect-then-halt
// contract: an author fixing several edges sees them all in one build. A check
// returning on the first violation is indistinguishable from this one on a
// single-edge input, which is why the assertion needs more than one.
func TestBuildIR_UsesInjectAllowlist_ReportsEveryOffendingEdge(t *testing.T) {
	t.Parallel()

	nodes := types.OrderedMap[*topology.TopologyNode]{}
	nodes.Set("api", &topology.TopologyNode{InstanceID: "api", ComponentType: "core/consumer"})
	nodes.Set("db", &topology.TopologyNode{InstanceID: "db", ComponentType: "core/target"})

	_, _, _, err := BuildIR(BuildIRConfig{
		Topology: &topology.TopologyResult{
			Nodes: nodes,
			Edges: []topology.NormalizedEdge{
				{Source: "api", Target: "db", Socket: "opaque", Alias: "first", Inject: envInject()},
				{Source: "api", Target: "db", Socket: "open", Alias: "second", Inject: envInject()},
				{Source: "api", Target: "db", Socket: "narrow", Alias: "third",
					Inject: &types.InjectBlock{Arg: map[string]any{"B": "host"}}},
			},
			FeatureAttachments: map[string][]topology.FeatureAttachment{},
		},
		ResolveResult: &resolver.ResolveResult{
			Aggregate:     &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test-app", Version: "1.0.0"}},
			Manifest:      &types.Manifest{Metadata: types.ManifestMetadata{Version: "1.0.0"}},
			BoundaryInfra: map[string]*resolver.BoundaryInfra{"api": {BoundaryPath: "local/default"}},
		},
		CatalogLookup:   allowlistCatalog(),
		CompilerVersion: "0.1.0",
	})
	if err == nil {
		t.Fatal("two offending edges must halt the build")
	}
	// The permitted middle edge must not be reported, and both offenders must be.
	for _, want := range []string{`"first"`, `"third"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("halt must name offending alias %s; got %q", want, err.Error())
		}
	}
	if strings.Contains(err.Error(), `"second"`) {
		t.Errorf("halt must not name the permitted edge; got %q", err.Error())
	}
}
