// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
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
	// not. It is the bracketed spelling the surviving hand-written guard renders.
	field string
	// dotted is the same locator in the spelling a declared rule renders, which
	// is the one the project's field-path reader can resolve. Both are carried
	// because both spellings are live in the tree.
	dotted string
}{
	{
		name: "top level",
		wrap: func(block string) string {
			return "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: demo\nspec:\n  components:\n" + indent(block, 4)
		},
		field:  `.spec.components["api"]`,
		dotted: ".spec.components.api",
	},
	{
		name: "declared subsystem",
		wrap: func(block string) string {
			return "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: demo\nspec:\n  subsystems:\n    data:\n      components:\n" + indent(block, 8)
		},
		field:  `.spec.subsystems.data.components["api"]`,
		dotted: ".spec.subsystems.data.components.api",
	},
	{
		name: "nested subsystem",
		wrap: func(block string) string {
			return "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: demo\nspec:\n  subsystems:\n    data:\n      subsystems:\n        inner:\n          components:\n" + indent(block, 12)
		},
		field:  `.spec.subsystems.data.subsystems.inner.components["api"]`,
		dotted: ".spec.subsystems.data.subsystems.inner.components.api",
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

// placedEdgeRule asks one rule over the edge surface at every placement, and
// returns the sockets or locators it reported.
func placedEdgeSockets(
	t *testing.T, agg *types.Aggregate, release *types.Release, catalog CatalogLookup,
) []string {
	t.Helper()
	na := normalized(t, agg)
	view := newNormalizedView(na, release, catalog, nil)
	objections, err := constraint.RunOnNodes[constraint.NormalizedView](
		nil, newCoordinateSet(view), view, releaseEdgeSocketDeclared)
	if err != nil {
		t.Fatalf("the node walk returned an error: %v", err)
	}
	return declaredSocketsOf(objections)
}

// TestTheDeploymentEdgeSocketRuleReachesEveryPlacement proves the socket rule
// reaches a component wherever the application declares it, in BOTH directions:
// a deployment naming a socket the consuming component does not declare is
// reported, and one naming a socket it does declare is not.
//
// The passing half is the load-bearing one — a rule that skipped the component
// silently would also report nothing for the legitimate input, and the two
// results together are what distinguishes inspection from omission.
func TestTheDeploymentEdgeSocketRuleReachesEveryPlacement(t *testing.T) {
	t.Parallel()
	for _, pl := range nodePlacements {
		t.Run(pl.name, func(t *testing.T) {
			t.Parallel()
			agg := parseAggregate(t, pl.wrap(plainComponents))
			cat := edgeCoverageCatalog(t)

			undeclared := releaseNaming(t, "nope", "main", "target: db\n")
			if got := placedEdgeSockets(t, agg, undeclared, cat); !equalStrings(got, []string{"nope"}) {
				t.Fatalf("an undeclared socket reported %v; want [nope]", got)
			}

			declared := releaseNaming(t, "databases", "main", "target: db\n")
			if got := placedEdgeSockets(t, agg, declared, cat); len(got) != 0 {
				t.Fatalf("a declared socket must pass; got %v", got)
			}
		})
	}
}

// TestTheConnectionSizingRuleReachesEveryPlacement proves the per-edge sizing
// rule reaches a component wherever the application declares it, in both
// directions: operator per-edge sizing that violates the socket's contract is
// reported, and conforming sizing of the same shape is not.
//
// The locator names the placement the author wrote, so it differs per placement
// even though the component's identity — the name the deployment addresses it by
// — does not.
func TestTheConnectionSizingRuleReachesEveryPlacement(t *testing.T) {
	t.Parallel()
	for _, pl := range nodePlacements {
		t.Run(pl.name, func(t *testing.T) {
			t.Parallel()
			agg := parseAggregate(t, pl.wrap(plainComponents))
			cat := edgeCoverageCatalog(t)

			ask := func(release *types.Release) []string {
				na := normalized(t, agg)
				view := newNormalizedView(na, release, cat, nil)
				objections, err := constraint.RunOnEdges[constraint.NormalizedView](
					nil, newCoordinateSet(view), view, connectionInfraConformsToTheSocket)
				if err != nil {
					t.Fatalf("the connection walk returned an error: %v", err)
				}
				return locatorsOf(objections)
			}

			bad := releaseNaming(t, "databases", "main", "target: db\ninfra:\n  storage_class: nonsense\n")
			want := []string{pl.dotted + ".uses.databases.main.infra.storage_class"}
			if got := ask(bad); !equalStrings(got, want) {
				t.Fatalf("non-conforming per-edge sizing raised %v; want %v", got, want)
			}

			good := releaseNaming(t, "databases", "main", "target: db\ninfra:\n  storage_class: fast\n")
			if got := ask(good); len(got) != 0 {
				t.Fatalf("conforming per-edge sizing must pass; got %v", got)
			}
		})
	}
}

// The required-socket rule is deliberately absent from this file. It reads three
// surfaces and the last of them is the connection set a composition produces, so
// it cannot be asked of the two authoring documents alone — it is declared over
// the composed application and proved beside that stage's presentation, over
// every placement this file enumerates and over a component a module
// contributes, which no reading taken here could reach.

// TestTheConnectionConfigurationRuleMeasuresAnEmptyEdgeInEitherAuthoringForm
// proves a socket's required per-edge key is enforced against an edge that
// authors no per-edge configuration at all — in both authoring forms — and that
// an edge authoring the key passes.
//
// The empty edge is the case such a key exists to catch: enforcing the contract
// only where the author wrote something enforces it everywhere except where it
// matters.
func TestTheConnectionConfigurationRuleMeasuresAnEmptyEdgeInEitherAuthoringForm(t *testing.T) {
	t.Parallel()
	cat := edgeCoverageCatalog(t)

	cases := []struct {
		name      string
		usesBlock string
		want      []string
	}{
		{
			name:      "verbose entry authoring no values",
			usesBlock: "caches:\n  main:\n    target: db\n",
			want:      []string{".spec.components.api.uses.caches.main.values.pool_size"},
		},
		{
			// The short form spells no alias in the file, so the report positions
			// at the socket — the deepest key the file does contain.
			name:      "compact entry, which has nowhere to write per-edge values",
			usesBlock: "caches: [db]\n",
			want:      []string{".spec.components.api.uses.caches.values.pool_size"},
		},
		{
			name:      "verbose entry authoring the required key",
			usesBlock: "caches:\n  main:\n    target: db\n    values:\n      pool_size: 5\n",
			want:      nil,
		},
		{
			// The socket declaring an infraSchema but no valueSchema is the
			// boundary of the change above: what is opt-in is the SOCKET's
			// contract on this pillar, not the edge's content.
			name:      "a socket declaring no contract on this pillar skips its edges",
			usesBlock: "databases: [db]\n",
			want:      nil,
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
			na := normalized(t, agg)
			view := newNormalizedView(na, nil, cat, nil)
			objections, err := constraint.RunOnEdges[constraint.NormalizedView](
				nil, newCoordinateSet(view), view, connectionValuesConformToTheSocket)
			if err != nil {
				t.Fatalf("the connection walk returned an error: %v", err)
			}
			if got := locatorsOf(objections); !equalStrings(got, tc.want) {
				t.Errorf("raised sequence = %v; want %v", got, tc.want)
			}
		})
	}
}
