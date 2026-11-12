// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package ir

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestBuildIR_ConnectionAttachmentConformanceNamesTheEntryThePillarWasWrittenOn
// proves the conformance of a connection attachment's merged configuration is
// reported at the connection entry the pillar was written on, in the document
// whose author wrote it — never at a component attachment block the consumer
// does not have. Values are the developer's and sit on the Aggregate's
// connection entry, in the document the component was written in; infra is the
// operator's and sits on the Release's entry; an attachment only the Release
// made has no developer entry, so even its missing values are reported at the
// Release entry the operator wrote. An attachment of the component's own block
// is reported where it always was.
//
// It drives the graph build, so the site rule the conformance gate is handed is
// the one the build constructs, not one a test assembled.
func TestBuildIR_ConnectionAttachmentConformanceNamesTheEntryThePillarWasWrittenOn(t *testing.T) {
	t.Parallel()

	gpt := &types.AttachmentConnection{Socket: "models", Alias: "gpt", Attachment: "credential"}
	name := types.JoinConnectionAttachmentName("gpt", "credential")
	lowered := func(values types.OrderedMap[any]) topology.FeatureAttachment {
		return topology.FeatureAttachment{
			InstanceName: name,
			Feature:      types.FeatureInstance{Type: "core/monitoring", Values: values},
			Connection:   gpt,
		}
	}
	// releaseOnConnection is a Release writing one attachment on node's "gpt"
	// connection, inside the boundary "prod".
	releaseOnConnection := func(node string, att types.ReleaseConnectionAttachment) *types.Release {
		entry := types.ReleaseUsesEntry{}
		entry.Features.Set("credential", att)
		aliases := types.OrderedMap[types.ReleaseUsesEntry]{}
		aliases.Set("gpt", entry)
		comp := types.BoundaryComponent{}
		comp.Uses.Set("models", aliases)
		prod := types.Boundary{}
		prod.Components.Set(node, comp)
		release := &types.Release{Metadata: types.ReleaseMetadata{Name: "demo-prod"}}
		release.Spec.Boundaries.Set("prod", prod)
		return release
	}

	type finding struct{ entity, field string }
	cases := []struct {
		name        string
		node        *topology.TopologyNode
		attachments []topology.FeatureAttachment
		release     *types.Release
		want        []finding
	}{
		{
			name:        "the developer's values, at the developer's connection entry",
			node:        &topology.TopologyNode{InstanceID: "api", BoundaryPath: "prod"},
			attachments: []topology.FeatureAttachment{lowered(vmap("level", "bogus"))},
			want: []finding{{
				entity: "Aggregate test",
				field:  ".spec.components.api.uses.models.gpt.features.credential.values.level",
			}},
		},
		{
			name:        "the operator's infra, at the operator's entry beside the developer's values",
			node:        &topology.TopologyNode{InstanceID: "api", BoundaryPath: "prod"},
			attachments: []topology.FeatureAttachment{lowered(vmap("level", "info"))},
			release:     releaseOnConnection("api", types.ReleaseConnectionAttachment{Infra: vmap("replicas", 0)}),
			want: []finding{{
				entity: "Release demo-prod",
				field:  ".spec.boundaries.prod.components.api.uses.models.gpt.features.credential.infra.replicas",
			}},
		},
		{
			name: "an attachment only the operator made, its missing values at the operator's entry",
			node: &topology.TopologyNode{InstanceID: "api", BoundaryPath: "prod"},
			release: releaseOnConnection("api", types.ReleaseConnectionAttachment{
				Type: "core/db-creds", Infra: vmap("rotation", "hourly"),
			}),
			want: []finding{{
				entity: "Release demo-prod",
				field:  ".spec.boundaries.prod.components.api.uses.models.gpt.features.credential.values.username",
			}},
		},
		{
			name: "a component a sub-module contributes, measured in the module's own document",
			node: &topology.TopologyNode{
				InstanceID: "mod.api", BoundaryPath: "prod", SourceAggregate: "mod", SourceFile: "mod.yaml",
			},
			attachments: []topology.FeatureAttachment{lowered(vmap("level", "bogus"))},
			want: []finding{{
				entity: "Aggregate mod.yaml",
				field:  ".spec.components.api.uses.models.gpt.features.credential.values.level",
			}},
		},
		{
			name:        "an attachment of the component's own block, at the component's block",
			node:        &topology.TopologyNode{InstanceID: "api", BoundaryPath: "prod"},
			attachments: []topology.FeatureAttachment{feat("mon", "core/monitoring", vmap("level", "bogus"))},
			want: []finding{{
				entity: "Aggregate test",
				field:  ".spec.components.api.features.mon.values.level",
			}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			id := tc.node.InstanceID
			nodes := types.OrderedMap[*topology.TopologyNode]{}
			nodes.Set(id, tc.node)
			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("the graph build panicked: %v", r)
					}
				}()
				_, _, _, err = buildIR(BuildIRConfig{
					Topology: &topology.TopologyResult{
						Nodes:              nodes,
						FeatureAttachments: map[string][]topology.FeatureAttachment{id: tc.attachments},
					},
					ResolveResult: &resolver.ResolveResult{
						Aggregate:     &types.Aggregate{Metadata: types.AggregateMetadata{Name: "test"}},
						Release:       tc.release,
						ReleasePath:   "release.yaml",
						BoundaryInfra: map[string]*resolver.BoundaryInfra{},
					},
					CatalogLookup: conformanceFeatureCatalog(t),
				})
			}()
			if !errors.Is(err, resolver.ErrSchemaConformance()) {
				t.Fatalf("the build must halt on the attachment conformance condition; got %v", err)
			}
			joined, ok := err.(interface{ Unwrap() []error })
			if !ok {
				t.Fatalf("the halt must carry its findings joined; got %T", err)
			}
			var got []finding
			for _, e := range joined.Unwrap() {
				var re *resolver.ResolverError
				if !errors.As(e, &re) || !errors.Is(re, resolver.ErrSchemaConformance()) {
					t.Errorf("finding %v does not carry the conformance condition", e)
					continue
				}
				got = append(got, finding{entity: re.Entity, field: re.Field})
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("findings = %+v; want %+v", got, tc.want)
			}
			for _, f := range got {
				if strings.Contains(f.field, "features."+name) {
					t.Errorf("finding at %q spells the attachment's composed name; it must name the connection entry", f.field)
				}
			}
		})
	}
}
