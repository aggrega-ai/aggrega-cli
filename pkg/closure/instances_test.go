// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package closure_test

import (
	"context"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// instancesTopAggregate declares a component carrying an attachment of its own and
// one on a connection, a second component, and a module included as data.
const instancesTopAggregate = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: top
  version: "1.0.0"
spec:
  aggregates:
    data:
      type: example/db:1.0.0
  components:
    router:
      type: app/gw
      features:
        audit: { type: obs/audit }
      uses:
        models:
          gpt:
            target: gpt
            features:
              credential: { type: sec/token }
    gpt:
      type: app/model
`

// instancesDataAggregate is the module included as data: one component, and a
// module of its own included as cache.
const instancesDataAggregate = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: example/db
  version: "1.0.0"
spec:
  aggregates:
    cache:
      type: example/cache:1.0.0
  components:
    store:
      type: app/store:1.0.0
`

// instancesCacheAggregate is the module data includes as cache.
const instancesCacheAggregate = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: example/cache
  version: "1.0.0"
spec:
  components:
    mem:
      type: app/mem
`

// instancesRelease attaches a Feature to its one boundary.
const instancesRelease = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: r
  environment: test
  version: "1.0.0"
spec:
  boundaries:
    prod:
      features:
        tls: { type: net/tls }
`

// TestClosureReportsInstancesInVisitOrder holds the walk to reporting every
// component and feature instance it visits, once each, in the order it visits
// them: the Aggregate's components with their own and their connections'
// attachments, then the Release's attachments, then the components of each module
// the Aggregate includes, depth first. A component inside a module carries the
// identity the composition gives it — the module's name, then its own — which is
// the name the Manifest's name axis matches; an attachment on a connection carries
// the name it takes on its component. Each instance names the file its type is
// written in: the files the caller loaded the Release and the Aggregate from, and
// a module's own reference at the version it resolved to.
//
// 🔴 PLANTED DEFECT: in moduleInstances (pkg/closure/sub_aggregate_descent.go),
// leave a component's name unqualified: the data.store and data.cache.mem rows
// redden.
func TestClosureReportsInstancesInVisitOrder(t *testing.T) {
	t.Parallel()
	ents := map[string]closure.Resolution{}
	for _, uri := range []string{
		"app/gw", "obs/audit", "sec/token", "app/model", "net/tls",
		"example/db:1.0.0", "app/store:1.0.0", "example/cache:1.0.0", "app/mem",
	} {
		ents[uri] = resolutionOK("1.0.0", "sha256:"+uri, "tier1-local")
	}
	lookup := &fakeLookup{entities: ents, plugins: map[string]closure.Resolution{}}
	loader := &fakeAggLoader{bodies: map[string]*types.Aggregate{
		"example/db:1.0.0":    loadAggregate(t, instancesDataAggregate),
		"example/cache:1.0.0": loadAggregate(t, instancesCacheAggregate),
	}}

	cl, err := closure.ComputeClosure(context.Background(), loadRelease(t, instancesRelease), nil,
		types.Normalize(loadAggregate(t, instancesTopAggregate)), workspaceconfig.Config{}, lookup)
	if err != nil {
		t.Fatalf("ComputeClosure: %v", err)
	}
	if err := closure.DescendSubAggregates(context.Background(), &cl, loader, lookup); err != nil {
		t.Fatalf("DescendSubAggregates: %v", err)
	}
	cl.NameInstanceFiles("release.yaml", "aggregate.yaml")

	const top = "aggregate.spec.components."
	want := []closure.InstanceReference{
		{Path: top + "router.type", Name: "router", Reference: "app/gw", File: "aggregate.yaml", Document: closure.InAggregate},
		{Path: top + "router.features.audit.type", Name: "audit", Reference: "obs/audit", File: "aggregate.yaml", Feature: true,
			Document: closure.InAggregate},
		{Path: top + "router.uses.models.gpt.features.credential.type", Name: "gpt.credential", Reference: "sec/token",
			File: "aggregate.yaml", Feature: true, Document: closure.InAggregate},
		{Path: top + "gpt.type", Name: "gpt", Reference: "app/model", File: "aggregate.yaml", Document: closure.InAggregate},
		{Path: "release.spec.boundaries.prod.features.tls.type", Name: "tls", Reference: "net/tls", File: "release.yaml", Feature: true,
			Document: closure.InRelease},
		{Path: "aggregate(example/db:1.0.0).spec.components.store.type", Name: "data.store", Reference: "app/store:1.0.0",
			File: "example/db:1.0.0", Document: closure.InModule},
		{Path: "aggregate(example/cache:1.0.0).spec.components.mem.type", Name: "data.cache.mem", Reference: "app/mem",
			File: "example/cache:1.0.0", Document: closure.InModule},
	}

	got := cl.Instances
	if len(got) != len(want) {
		t.Fatalf("the walk reported %d instances, want %d:\n got %+v", len(got), len(want), got)
	}
	for i := range want {
		g := got[i]
		w := want[i]
		if g != w {
			t.Errorf("instance %d:\n got %+v\nwant %+v", i, g, w)
		}
	}
}
