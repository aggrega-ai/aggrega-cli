// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// edgeCoverageCatalog declares one consuming Component with two sockets that
// exercise the two per-edge contracts independently:
//
//   - "databases" is required and carries an infraSchema, so it drives the
//     required-socket rule and the operator-side conformance rule;
//   - "caches" is optional and carries a valueSchema with a required key, so it
//     drives the developer-side conformance rule without interacting with the
//     required-socket rule.
func edgeCoverageCatalog(t *testing.T) CatalogLookup {
	t.Helper()
	const svcYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: app/svc
  version: "1.0.0"
spec:
  usesSchema:
    databases:
      required: true
      selector: "component.aggrega.ai/type=db"
      infraSchema:
        storage_class: { type: string, enum: ["fast", "slow"] }
    caches:
      selector: "component.aggrega.ai/type=cache"
      valueSchema:
        pool_size: { type: integer, required: true }
`
	const dbYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: app/db
  version: "1.0.0"
spec: {}
`
	var svc, db types.Component
	if err := yaml.Unmarshal([]byte(svcYAML), &svc); err != nil {
		t.Fatalf("svc fixture parse: %v", err)
	}
	if err := yaml.Unmarshal([]byte(dbYAML), &db); err != nil {
		t.Fatalf("db fixture parse: %v", err)
	}
	return CatalogLookup{
		Component: func(u string) (types.Component, bool) {
			switch u {
			case "app/svc:1.0.0":
				return svc, true
			case "app/db:1.0.0":
				return db, true
			}
			return types.Component{}, false
		},
	}
}

// nodePlacements are the two shapes the Aggregate can declare a component in
// that the resolver can see. Both address the component by its bare instance
// name, which is the identity a Release names it by and the identity the
// flattened graph mints — so both must be inspected by every guard that reads
// the Release edge surface, and neither may be inspected by a different key.
//
// A sub-aggregate component is the third shape and is deliberately absent here:
// its document loads only when the graph is flattened, so no resolver-stage
// fixture can express it. It is covered where it becomes visible.
var nodePlacements = []struct {
	name string
	// wrap places the given component block into a whole Aggregate document.
	wrap func(componentBlock string) string
	// field is the authoring locator a diagnostic against the placed component
	// must point at, which differs per placement even though the identity does
	// not.
	field string
}{
	{
		name: "top level",
		wrap: func(block string) string {
			return "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: demo\nspec:\n  components:\n" + indent(block, 4)
		},
		field: `.spec.components["api"]`,
	},
	{
		name: "declared subsystem",
		wrap: func(block string) string {
			return "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: demo\nspec:\n  subsystems:\n    data:\n      components:\n" + indent(block, 8)
		},
		field: `.spec.subsystems.data.components["api"]`,
	},
	{
		name: "nested subsystem",
		wrap: func(block string) string {
			return "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: demo\nspec:\n  subsystems:\n    data:\n      subsystems:\n        inner:\n          components:\n" + indent(block, 12)
		},
		field: `.spec.subsystems.data.subsystems.inner.components["api"]`,
	},
}

func indent(block string, n int) string {
	pad := strings.Repeat(" ", n)
	lines := strings.Split(strings.TrimRight(block, "\n"), "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = pad + l
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

func parseAggregate(t *testing.T, y string) *types.Aggregate {
	t.Helper()
	var a types.Aggregate
	if err := yaml.Unmarshal([]byte(y), &a); err != nil {
		t.Fatalf("aggregate parse: %v\n%s", err, y)
	}
	return &a
}

func parseRelease(t *testing.T, y string) *types.Release {
	t.Helper()
	var r types.Release
	if err := yaml.Unmarshal([]byte(y), &r); err != nil {
		t.Fatalf("release parse: %v\n%s", err, y)
	}
	return &r
}

// plainComponents is the component block every placement wraps: the consumer and
// a target for it to reach.
const plainComponents = `api:
  type: app/svc:1.0.0
db:
  type: app/db:1.0.0
`

// releaseNaming builds a Release whose boundary "prod" carries one `uses` entry
// on node "api" under the given socket and alias.
func releaseNaming(t *testing.T, socket, alias, body string) *types.Release {
	t.Helper()
	return parseRelease(t, `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      components:
        api:
          uses:
            `+socket+`:
              `+alias+`:
`+indent(body, 16))
}

// TestReleaseUsesSocketGuard_InspectsEveryPlacement proves the socket-allowlist
// gate reaches a component wherever the Aggregate declares it, in BOTH
// directions: a Release naming a socket the consuming component does not declare
// halts, and a Release naming one it does declare passes. The passing half is
// the load-bearing one — a gate that skipped the component silently would also
// report no error for the legitimate input, and the two results together are
// what distinguishes inspection from omission.
func TestReleaseUsesSocketGuard_InspectsEveryPlacement(t *testing.T) {
	t.Parallel()
	for _, pl := range nodePlacements {
		t.Run(pl.name, func(t *testing.T) {
			t.Parallel()
			agg := parseAggregate(t, pl.wrap(plainComponents))
			cat := edgeCoverageCatalog(t)

			undeclared := releaseNaming(t, "nope", "main", "target: db\n")
			errs := guardReleaseUsesSocket(normalized(t, agg), undeclared, cat)
			if len(errs) != 1 || !errors.Is(errs[0], ErrUnknownUsesSocket()) {
				t.Fatalf("undeclared socket must halt; got %v", errs)
			}

			declared := releaseNaming(t, "databases", "main", "target: db\n")
			if errs := guardReleaseUsesSocket(normalized(t, agg), declared, cat); len(errs) != 0 {
				t.Fatalf("declared socket must pass; got %v", errs)
			}
		})
	}
}

// TestEdgeConformanceGuard_InspectsEveryPlacement proves the per-edge
// conformance gate reaches a component wherever the Aggregate declares it, in
// both directions: operator per-edge infra that violates the socket's
// infraSchema halts, and conforming infra of the same shape passes.
func TestEdgeConformanceGuard_InspectsEveryPlacement(t *testing.T) {
	t.Parallel()
	for _, pl := range nodePlacements {
		t.Run(pl.name, func(t *testing.T) {
			t.Parallel()
			agg := parseAggregate(t, pl.wrap(plainComponents))
			cat := edgeCoverageCatalog(t)

			bad := releaseNaming(t, "databases", "main", "target: db\ninfra:\n  storage_class: nonsense\n")
			errs := guardEdgeConformance(normalized(t, agg), bad, cat, nil)
			if len(errs) != 1 || !errors.Is(errs[0], ErrSchemaConformance()) {
				t.Fatalf("non-conforming per-edge infra must halt; got %v", errs)
			}

			good := releaseNaming(t, "databases", "main", "target: db\ninfra:\n  storage_class: fast\n")
			if errs := guardEdgeConformance(normalized(t, agg), good, cat, nil); len(errs) != 0 {
				t.Fatalf("conforming per-edge infra must pass; got %v", errs)
			}
		})
	}
}

// TestRequiredSocketGuard_InspectsEveryPlacement proves the required-socket rule
// reaches a component wherever the Aggregate declares it, in both directions: a
// required socket nothing binds halts, and the same socket bound from the
// Release passes.
//
// The passing half also pins the node identity the rule matches a Release
// binding on. A Release addresses a component by its bare instance name at every
// placement, so crediting the binding is the correct outcome — and a rule keyed
// on anything else would reject this document while accepting one that binds
// nothing.
func TestRequiredSocketGuard_InspectsEveryPlacement(t *testing.T) {
	t.Parallel()
	for _, pl := range nodePlacements {
		t.Run(pl.name, func(t *testing.T) {
			t.Parallel()
			agg := parseAggregate(t, pl.wrap(plainComponents))
			cat := edgeCoverageCatalog(t)

			errs := guardRequiredUsesSockets(normalized(t, agg), nil, cat)
			if len(errs) != 1 || !errors.Is(errs[0], ErrMissingRequiredSocket()) {
				t.Fatalf("unbound required socket must halt; got %v", errs)
			}
			if !strings.Contains(errs[0].Field, pl.field) {
				t.Errorf("diagnostic must locate the component at %s; got %s", pl.field, errs[0].Field)
			}

			bound := releaseNaming(t, "databases", "main", "target: db\n")
			if errs := guardRequiredUsesSockets(normalized(t, agg), bound, cat); len(errs) != 0 {
				t.Fatalf("required socket bound by the Release must pass; got %v", errs)
			}
		})
	}
}

// TestEdgeConformance_RequiredPerEdgeKeyAgainstAnEmptyEdge proves a socket's
// required per-edge key is enforced against an edge that authors no per-edge
// config at all — in both authoring forms — and that an edge authoring the key
// passes.
//
// The empty edge is the case such a key exists to catch: enforcing the contract
// only where the author wrote something enforces it everywhere except where it
// matters.
func TestEdgeConformance_RequiredPerEdgeKeyAgainstAnEmptyEdge(t *testing.T) {
	t.Parallel()
	cat := edgeCoverageCatalog(t)

	cases := []struct {
		name      string
		usesBlock string
		wantHalt  bool
	}{
		{
			name:      "verbose entry authoring no values",
			usesBlock: "caches:\n  main:\n    target: db\n",
			wantHalt:  true,
		},
		{
			name:      "compact entry, which has nowhere to write per-edge values",
			usesBlock: "caches: [db]\n",
			wantHalt:  true,
		},
		{
			name:      "verbose entry authoring the required key",
			usesBlock: "caches:\n  main:\n    target: db\n    values:\n      pool_size: 5\n",
			wantHalt:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			agg := parseAggregate(t, `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: demo
spec:
  components:
    api:
      type: app/svc:1.0.0
      uses:
`+indent(tc.usesBlock, 8)+`    db:
      type: app/db:1.0.0
`)
			errs := guardEdgeConformance(normalized(t, agg), nil, cat, nil)
			if tc.wantHalt {
				if len(errs) != 1 || !errors.Is(errs[0], ErrSchemaConformance()) {
					t.Fatalf("edge authoring no required per-edge key must halt; got %v", errs)
				}
				if !strings.Contains(errs[0].Message, "pool_size") {
					t.Errorf("diagnostic must name the missing key; got %s", errs[0].Message)
				}
				return
			}
			if len(errs) != 0 {
				t.Fatalf("edge authoring the required per-edge key must pass; got %v", errs)
			}
		})
	}
}

// TestEdgeConformance_SocketWithoutAContractStillSkips pins the boundary of the
// change above: what is opt-in is the SOCKET's contract, not the edge's content.
// A socket declaring no per-edge schema is skipped whatever its edges author, so
// validating the empty case did not turn every edge in the corpus into a
// candidate for a violation.
func TestEdgeConformance_SocketWithoutAContractStillSkips(t *testing.T) {
	t.Parallel()
	agg := parseAggregate(t, `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: demo
spec:
  components:
    api:
      type: app/svc:1.0.0
      uses:
        databases: [db]
    db:
      type: app/db:1.0.0
`)
	// "databases" declares an infraSchema but no valueSchema, so the developer
	// per-edge values pillar has no contract to be checked against.
	if errs := guardEdgeConformance(normalized(t, agg), nil, edgeCoverageCatalog(t), nil); len(errs) != 0 {
		t.Fatalf("socket declaring no valueSchema must skip its edges; got %v", errs)
	}
}
