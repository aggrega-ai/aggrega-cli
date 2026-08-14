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
// developer half therefore conforms, so anything the per-edge gate reports for
// these components comes from the deployment's own edge surface.
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

// TestEdgeConformance_OperatorEntryIsJudgedWhereItWasWritten pins which
// connections of a socket the operator-side per-edge contract is enforced against:
// exactly the ones the deployment wrote an entry for, and every one of those.
//
// Both halves are load-bearing and they pull in opposite directions.
//
// An entry carrying nothing at all is still an entry, and it is the maximal
// violation of a key the socket declares required — the same property the
// developer-side gate has. Both fields of a deployment's per-edge entry are
// optional, so an entry with neither set is a document this surface accepts, and a
// gate that demanded content before judging would pass over exactly the input the
// required key exists to catch.
//
// A connection the deployment wrote NO entry for is not this contract's business.
// The operator's per-edge block is where the operator's per-edge configuration is
// authored; a connection the application's own dependency block drew and the
// deployment never configured has nothing authored here to judge, and reporting
// one would reject a document both surfaces accept.
//
// The two together fix the rule at "one judgement per entry the deployment wrote",
// which is a different set from "one per connection the build draws" — the rows
// below hold those apart, including the row where the deployment writes an entry
// at an alias no connection carries.
func TestEdgeConformance_OperatorEntryIsJudgedWhereItWasWritten(t *testing.T) {
	t.Parallel()
	cat := authorshipCatalog(t)
	agg := parseAggregate(t, twoConnectionAggregate)

	cases := []struct {
		name string
		// alias and body are the single per-edge entry the deployment writes;
		// alias is empty for the row that writes none at all.
		alias    string
		body     string
		wantHalt bool
		// wantField is the authoring locator the halt must name, so a row cannot
		// pass on a halt raised for a different connection.
		wantField string
	}{
		{
			name:     "a connection the deployment configures nowhere",
			wantHalt: false,
		},
		{
			name:      "an entry with neither field set, on the first connection",
			alias:     "primary",
			body:      "{}\n",
			wantHalt:  true,
			wantField: `.spec.boundaries["prod"].components["api"].uses["databases"]["primary"]`,
		},
		{
			name:      "the same empty entry on the second connection",
			alias:     "replica",
			body:      "{}\n",
			wantHalt:  true,
			wantField: `.spec.boundaries["prod"].components["api"].uses["databases"]["replica"]`,
		},
		{
			name:      "an entry at an alias no connection carries",
			alias:     "spare",
			body:      "{}\n",
			wantHalt:  true,
			wantField: `.spec.boundaries["prod"].components["api"].uses["databases"]["spare"]`,
		},
		{
			name:     "an entry authoring the required key",
			alias:    "primary",
			body:     "infra:\n  storage_class: fast\n",
			wantHalt: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var rel *types.Release
			if tc.alias != "" {
				rel = releaseNaming(t, "databases", tc.alias, tc.body)
			}
			errs := guardEdgeConformance(normalized(t, agg), rel, cat, nil)
			if !tc.wantHalt {
				if len(errs) != 0 {
					t.Fatalf("nothing authored against the contract must pass; got %v", errs)
				}
				return
			}
			if len(errs) != 1 || !errors.Is(errs[0], ErrSchemaConformance()) {
				t.Fatalf("an entry authoring no required key must halt exactly once; got %v", errs)
			}
			if !strings.HasPrefix(errs[0].Field, tc.wantField) {
				t.Errorf("halt must name the connection the entry was written for\n got %q\nwant prefix %q",
					errs[0].Field, tc.wantField)
			}
			if !strings.Contains(errs[0].Message, "storage_class") {
				t.Errorf("diagnostic must name the missing key; got %s", errs[0].Message)
			}
		})
	}
}

// TestEdgeConformance_DeveloperEntryIsJudgedWhereItWasWritten is the mirror on the
// application's own dependency block, and it exists because the two surfaces are
// separately authored and independently absent: a rule established on one of them
// says nothing about the other, and the developer contract is enforced against the
// entries the developer wrote rather than against the connections the build draws.
//
// The second row is the one that is easy to lose. A connection the deployment drew
// into a declared-but-unbound socket exists in the built graph while the
// application's block wrote nothing for it, so its developer per-edge contract has
// nothing authored to judge — and it reads identically, in the entry's own content,
// to a developer entry that authored nothing.
func TestEdgeConformance_DeveloperEntryIsJudgedWhereItWasWritten(t *testing.T) {
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
	errs := guardEdgeConformance(normalized(t, authoredEmpty), nil, cat, nil)
	if len(errs) != 1 || !errors.Is(errs[0], ErrSchemaConformance()) {
		t.Fatalf("a developer entry authoring no required per-edge key must halt exactly once; got %v", errs)
	}
	want := `.spec.components["api"].uses["databases"]["replica"]`
	if !strings.HasPrefix(errs[0].Field, want) {
		t.Errorf("halt must name the connection the entry was written for\n got %q\nwant prefix %q", errs[0].Field, want)
	}
	if !strings.Contains(errs[0].Message, "pool_size") {
		t.Errorf("diagnostic must name the missing key; got %s", errs[0].Message)
	}

	// The same alias, drawn by the deployment instead. The application's block
	// authored nothing there, so the developer contract has nothing to judge —
	// and the deployment's entry authors the key its own contract requires, so
	// the operator half is silent too and a halt here could only be the
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
	if errs := guardEdgeConformance(normalized(t, oneConnection), rel, cat, nil); len(errs) != 0 {
		t.Fatalf("a connection the application's block never wrote must not be judged against its developer contract; got %v", errs)
	}
}
