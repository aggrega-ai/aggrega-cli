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

// authorshipCatalog declares one consuming Component whose `databases` socket
// admits several connections and declares a REQUIRED key in each per-edge
// contract: pool_size in the valueSchema, storage_class in the infraSchema. The
// required keys are what make an entry carrying nothing observable — without one,
// an empty per-edge block conforms and every row below would pass for the wrong
// reason.
func authorshipCatalog(t *testing.T) CatalogLookup {
	t.Helper()
	const svcYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: app/svc
  version: "1.0.0"
spec:
  usesSchema:
    databases:
      selector: "component.aggrega.ai/type=db"
      maxConnections: 4
      valueSchema:
        pool_size: { type: integer, required: true }
      infraSchema:
        storage_class: { type: string, required: true }
`
	const dbYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: app/db
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: db
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
		Component: func(typeURI string) (types.Component, bool) {
			switch typeURI {
			case "app/svc:1.0.0":
				return svc, true
			case "app/db:1.0.0":
				return db, true
			}
			return types.Component{}, false
		},
	}
}

// twoConnectionAggregate draws two connections on one socket, primary and
// replica, both reaching db and both authoring the required per-edge value. Their
// developer half therefore conforms, so anything the per-edge rules report for
// these components comes from the Release's own edge surface.
const twoConnectionAggregate = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: demo
spec:
  components:
    api:
      type: app/svc:1.0.0
      uses:
        databases:
          primary:
            target: db
            values:
              pool_size: 20
          replica:
            target: db
            values:
              pool_size: 20
    db:
      type: app/db:1.0.0
`

// edgeSizingLocators asks the operator-side per-edge rule over one pair of
// documents and returns what it reported.
func edgeSizingLocators(
	t *testing.T, agg *types.Aggregate, release *types.Release, catalog CatalogLookup,
) []string {
	t.Helper()
	na := normalized(t, agg)
	view := newNormalizedView(na, release, catalog, nil)
	objections, err := constraint.RunOnEdges[constraint.NormalizedView](
		nil, newCoordinateSet(view), view, connectionInfraConformsToTheSocket)
	if err != nil {
		t.Fatalf("the connection walk returned an error: %v", err)
	}
	return locatorsOf(objections)
}

// edgeConfigurationLocators is the developer-side mirror of the helper above.
func edgeConfigurationLocators(
	t *testing.T, agg *types.Aggregate, release *types.Release, catalog CatalogLookup,
) []string {
	t.Helper()
	na := normalized(t, agg)
	view := newNormalizedView(na, release, catalog, nil)
	objections, err := constraint.RunOnEdges[constraint.NormalizedView](
		nil, newCoordinateSet(view), view, connectionValuesConformToTheSocket)
	if err != nil {
		t.Fatalf("the connection walk returned an error: %v", err)
	}
	return locatorsOf(objections)
}

// TestTheOperatorPerEdgeContractIsJudgedWhereItWasWritten pins which connections
// of a socket the operator-side per-edge contract is measured against: exactly
// the ones the Release wrote an entry for, and every one of those.
//
// Both halves are load-bearing and they pull in opposite directions.
//
// An entry carrying nothing at all is still an entry, and it is the maximal
// violation of a key the socket declares required. Both fields of a Release's
// per-edge entry are optional, so an entry with neither set is a document this
// surface accepts, and a rule that demanded content before measuring would pass
// over exactly the input the required key exists to catch.
//
// A connection the Release wrote NO entry for is not this contract's business.
// The operator's per-edge block is where the operator's per-edge configuration is
// authored; a connection the Aggregate's own dependency block drew and the
// Release never configured has nothing authored here to measure, and reporting
// one would reject a document both surfaces accept.
//
// The two together fix the rule at "one judgement per entry the Release wrote",
// which is a different set from "one per connection the build draws" — including
// the row where the Release writes an entry at an alias no connection carries.
func TestTheOperatorPerEdgeContractIsJudgedWhereItWasWritten(t *testing.T) {
	t.Parallel()
	cat := authorshipCatalog(t)
	agg := parseAggregate(t, twoConnectionAggregate)

	cases := []struct {
		name string
		// alias and body are the single per-edge entry the Release writes;
		// alias is empty for the row that writes none at all.
		alias string
		body  string
		// want is the locator sequence, empty where nothing is reported.
		want []string
	}{
		{
			name: "a connection the Release configures nowhere",
			want: nil,
		},
		{
			name:  "an entry with neither field set, on the first connection",
			alias: "primary",
			body:  "{}\n",
			want:  []string{".spec.components.api.uses.databases.primary.infra.storage_class"},
		},
		{
			name:  "the same empty entry on the second connection",
			alias: "replica",
			body:  "{}\n",
			want:  []string{".spec.components.api.uses.databases.replica.infra.storage_class"},
		},
		{
			name:  "an entry at an alias no connection carries",
			alias: "spare",
			body:  "{}\n",
			want:  []string{".spec.components.api.uses.databases.spare.infra.storage_class"},
		},
		{
			name:  "an entry authoring the required key",
			alias: "primary",
			body:  "infra:\n  storage_class: fast\n",
			want:  nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var rel *types.Release
			if tc.alias != "" {
				rel = releaseNaming(t, "databases", tc.alias, tc.body)
			}
			if got := edgeSizingLocators(t, agg, rel, cat); !equalStrings(got, tc.want) {
				t.Errorf("raised sequence = %v; want %v", got, tc.want)
			}
		})
	}
}

// TestTheDeveloperPerEdgeContractIsJudgedWhereItWasWritten is the mirror on the
// Aggregate's own dependency block, and it exists because the two surfaces are
// separately authored and independently absent: a rule established on one of them
// says nothing about the other.
//
// The second half is the one that is easy to lose. A connection the Release
// drew into a declared-but-unbound socket exists in the built graph while the
// Aggregate's block wrote nothing for it, so its developer per-edge contract has
// nothing authored to measure — and it reads identically, in the entry's own
// content, to a developer entry that authored nothing.
func TestTheDeveloperPerEdgeContractIsJudgedWhereItWasWritten(t *testing.T) {
	t.Parallel()
	cat := authorshipCatalog(t)

	authoredEmpty := parseAggregate(t, `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: demo
spec:
  components:
    api:
      type: app/svc:1.0.0
      uses:
        databases:
          primary:
            target: db
            values:
              pool_size: 20
          replica: {}
    db:
      type: app/db:1.0.0
`)
	got := edgeConfigurationLocators(t, authoredEmpty, nil, cat)
	want := []string{".spec.components.api.uses.databases.replica.values.pool_size"}
	if !equalStrings(got, want) {
		t.Errorf("a developer entry authoring no required per-edge key raised %v; want %v", got, want)
	}

	// The same alias, drawn by the Release instead. The Aggregate's block
	// authored nothing there, so the developer contract has nothing to measure —
	// and the Release's entry authors the key its own contract requires, so
	// the operator half is silent too and a report here could only be the
	// developer half speaking where it was not written.
	oneConnection := parseAggregate(t, `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: demo
spec:
  components:
    api:
      type: app/svc:1.0.0
      uses:
        databases:
          primary:
            target: db
            values:
              pool_size: 20
    db:
      type: app/db:1.0.0
`)
	rel := releaseNaming(t, "databases", "replica", "target: db\ninfra:\n  storage_class: fast\n")
	if got := edgeConfigurationLocators(t, oneConnection, rel, cat); len(got) != 0 {
		t.Errorf("a connection the Aggregate's block never wrote must not be measured against "+
			"its developer contract; got %v", got)
	}
	if got := edgeSizingLocators(t, oneConnection, rel, cat); len(got) != 0 {
		t.Errorf("the Release's own entry authors the key its contract requires, so the "+
			"operator half must be silent too; got %v", got)
	}
}

// TestNoPerEdgeReportNamesAnAliasTheFileDoesNotContain pins that a blank item in
// the short authoring form identifies no connection, so nothing is measured for
// it and no report names an empty alias segment.
//
// A blank item is an author leaving a list entry empty. It identifies no
// connection, so there is no connection for a per-edge contract to be measured
// against, and a required-key report locating an empty alias segment names
// something the author cannot act on.
func TestNoPerEdgeReportNamesAnAliasTheFileDoesNotContain(t *testing.T) {
	t.Parallel()
	cat := edgeCoverageCatalog(t)

	cases := []struct {
		name string
		// usesBlock is the `uses:` body of component `api`.
		usesBlock string
		// want is the locator sequence, in diagnostic order.
		want []string
	}{
		{
			name:      "a blank item on its own is judged nowhere",
			usesBlock: "caches: [\"\"]\n",
			want:      nil,
		},
		{
			// The short form gives each connection the target's own name as its
			// alias, and no key in the file spells it — so the report positions at
			// the socket, which is the deepest key the file does contain.
			name:      "a named item is still judged",
			usesBlock: "caches: [db]\n",
			want:      []string{".spec.components.api.uses.caches.values.pool_size"},
		},
		{
			name:      "a named item beside a blank one is judged once",
			usesBlock: "caches: [db, \"\"]\n",
			want:      []string{".spec.components.api.uses.caches.values.pool_size"},
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
			got := edgeConfigurationLocators(t, agg, nil, cat)
			if !equalStrings(got, tc.want) {
				t.Errorf("raised sequence = %v; want %v", got, tc.want)
			}
			// The property the whole shape turns on: no report may locate an edge
			// by an empty alias segment, whatever else was raised.
			for _, l := range got {
				if strings.Contains(l, "..") || strings.HasSuffix(l, ".") {
					t.Errorf("no report may name an empty alias segment; got %s", l)
				}
			}
		})
	}
}
