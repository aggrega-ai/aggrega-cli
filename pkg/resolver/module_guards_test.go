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

// moduleGuardCatalog holds the types the module-document fixtures author
// against: a component type permitting delivery through env alone and publishing
// one key, one permitting env and file, one permitting no delivery at all, and a
// Feature declaring no socket.
func moduleGuardCatalog(t *testing.T) CatalogLookup {
	t.Helper()
	parse := func(doc string) types.Component {
		t.Helper()
		var c types.Component
		if err := yaml.Unmarshal([]byte(doc), &c); err != nil {
			t.Fatalf("component fixture parse: %v\n%s", err, doc)
		}
		return c
	}
	envOnly := parse(`apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: app/env-only
  version: "1.0.0"
spec:
  inject: [env]
  usesSchema:
    backend:
      selector: "component.aggrega.ai/type=db"
      inject: [env]
  schema:
    provides:
      host: { type: string }
`)
	twoMechanisms := parse(`apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: app/two-mechanisms
  version: "1.0.0"
spec:
  inject: [env, file]
`)
	sealed := parse(`apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: app/sealed
  version: "1.0.0"
spec: {}
`)
	broken := parse(`apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: app/broken-contract
  version: "1.0.0"
spec:
  schema:
    values:
      field:
        $ref: "NoSuchLibrary#/schemas/Nothing"
`)
	var scrape types.Feature
	if err := yaml.Unmarshal([]byte(`apiVersion: aggrega.ai/v1beta1
kind: Feature
metadata:
  name: app/scrape
  version: "1.0.0"
spec: {}
`), &scrape); err != nil {
		t.Fatalf("feature fixture parse: %v", err)
	}
	return CatalogLookup{
		Component: func(u string) (types.Component, bool) {
			switch u {
			case "app/env-only:1.0.0":
				return envOnly, true
			case "app/two-mechanisms:1.0.0":
				return twoMechanisms, true
			case "app/sealed:1.0.0":
				return sealed, true
			case "app/broken-contract:1.0.0":
				return broken, true
			}
			return types.Component{}, false
		},
		Feature: func(u string) (types.Feature, bool) {
			if u == "app/scrape:1.0.0" {
				return scrape, true
			}
			return types.Feature{}, false
		},
	}
}

// moduleDocument wraps a spec block into a whole module document named
// mods/data.
func moduleDocument(spec string) string {
	return "apiVersion: aggrega.ai/v1beta1\nkind: Aggregate\nmetadata:\n  name: mods/data\nspec:\n" + indent(spec, 2)
}

// TestRunModuleGuardsAsksEachRuleOfAModuleDocument proves each rule the module
// pass asks is invoked over a sub-module's own document: one fault per row, each
// halting under its rule's condition, naming the module's document and the key
// inside it the module author wrote. Removing the rule's invocation from the
// module pass fails its row.
//
// The control row is the same document with no fault, which must draw nothing;
// without it a pass refusing every module would read as every row passing.
func TestRunModuleGuardsAsksEachRuleOfAModuleDocument(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		spec    string
		want    error
		locator string
	}{
		{
			name: "a well-formed module draws nothing",
			spec: `components:
  api:
    type: app/env-only:1.0.0
    inject:
      env:
        - { in: API_HOST, from: host }
`,
		},
		{
			name: "an injection block on a type permitting no delivery",
			spec: `components:
  api:
    type: app/sealed:1.0.0
    inject: {}
`,
			want:    ErrSelfInjectForbidden(),
			locator: ".spec.components.api.inject",
		},
		{
			name: "a mechanism the type does not permit",
			spec: `components:
  api:
    type: app/env-only:1.0.0
    inject:
      file:
        - { in: /etc/probe, value: "x" }
`,
			want:    ErrSelfInjectForbidden(),
			locator: ".spec.components.api.inject.file",
		},
		{
			name: "a key written twice under one mechanism",
			spec: `components:
  api:
    type: app/env-only:1.0.0
    inject:
      env:
        - { in: DUP, value: "a" }
        - { in: DUP, value: "b" }
`,
			want:    ErrInjectKeyCollision(),
			locator: ".spec.components.api.inject.env",
		},
		{
			name: "a reference to a key the type does not publish",
			spec: `components:
  api:
    type: app/env-only:1.0.0
    inject:
      env:
        - { in: API_HOST, from: nosuchkey }
`,
			want:    ErrInvalidInjectKey(),
			locator: ".spec.components.api.inject.env.API_HOST",
		},
		{
			name: "a key claimed by the component and by one of its connections",
			spec: `components:
  db:
    type: app/sealed:1.0.0
  api:
    type: app/env-only:1.0.0
    inject:
      env:
        - { in: DB_HOST, value: "x" }
    uses:
      backend:
        main:
          target: db
          inject:
            env:
              - { in: DB_HOST, from: host }
`,
			want:    ErrInjectKeyCollision(),
			locator: ".spec.components.api.inject.env.DB_HOST",
		},
		{
			name: "a key claimed under two mechanisms",
			spec: `components:
  api:
    type: app/two-mechanisms:1.0.0
    inject:
      env:
        - { in: TWO, value: "a" }
      file:
        - { in: TWO, value: "b" }
`,
			want:    ErrInjectKeyCollision(),
			locator: ".spec.components.api.inject.env.TWO",
		},
		{
			name: "a subsystem attachment wiring a socket its Feature does not declare",
			spec: `subsystems:
  store:
    features:
      scrape:
        type: app/scrape:1.0.0
        uses:
          invented-socket:
            link:
              target: api
    components:
      api:
        type: app/env-only:1.0.0
`,
			want:    ErrUnknownUsesSocket(),
			locator: ".spec.subsystems.store.features.scrape",
		},
		{
			name: "a literal in an attachment's own injection block",
			spec: `components:
  api:
    type: app/env-only:1.0.0
    features:
      scrape:
        type: app/scrape:1.0.0
        inject:
          env:
            - { in: TOKEN_NAME, from: name }
            - { in: LITERAL_NAME, value: a-literal }
`,
			want:    ErrAttachmentInjectValue(),
			locator: ".spec.components.api.features.scrape.inject.env",
		},
		{
			name: "a component type only the module uses whose contract does not compile",
			spec: `components:
  api:
    type: app/broken-contract:1.0.0
`,
			want:    ErrSchemaContractCompileFailure(),
			locator: ".spec.schema",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			na := normalized(t, parseAggregate(t, moduleDocument(tc.spec)))
			var errs []*ResolverError
			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("the module pass panicked: %v", r)
					}
				}()
				errs, err = RunModuleGuards(nil, na, "mods/data/aggregate.yaml", moduleGuardCatalog(t), nil)
			}()
			if err != nil {
				t.Fatalf("the module pass returned an error: %v", err)
			}
			if tc.want == nil {
				if len(errs) != 0 {
					t.Fatalf("a well-formed module was refused: %v", errs)
				}
				return
			}
			var match *ResolverError
			for _, e := range errs {
				if errors.Is(e, tc.want) && e.Field == tc.locator {
					match = e
					break
				}
			}
			if match == nil {
				t.Fatalf("want %v at %s; got %v", tc.want, tc.locator, errs)
			}
			// The contract failure names the catalog entity that declared the
			// contract; every rule's finding names the module's own document.
			wantEntity := "Aggregate mods/data"
			if errors.Is(tc.want, ErrSchemaContractCompileFailure()) {
				wantEntity = "Component app/broken-contract:1.0.0"
			}
			if match.Entity != wantEntity {
				t.Errorf("the finding names %q, want %q: %v", match.Entity, wantEntity, match)
			}
			// A rule's finding is a path inside the module's document, so it
			// carries that document's file; the contract failure names a catalog
			// entity's key and carries none of the module's.
			wantFile := "mods/data/aggregate.yaml"
			if errors.Is(tc.want, ErrSchemaContractCompileFailure()) {
				wantFile = ""
			}
			if match.File != wantFile {
				t.Errorf("the finding carries file %q, want %q: %v", match.File, wantFile, match)
			}
			if strings.Contains(match.Error(), "Aggregate root") {
				t.Errorf("the finding names an importing document: %v", match)
			}
		})
	}
}

// TestRunModuleGuardsOfAnAbsentDocumentReportsNothing pins the nil-document
// answer: a module whose load produced nothing is not a document a rule can be
// asked of.
func TestRunModuleGuardsOfAnAbsentDocumentReportsNothing(t *testing.T) {
	t.Parallel()
	errs, err := RunModuleGuards(nil, types.Normalize(nil), "", moduleGuardCatalog(t), nil)
	if err != nil || len(errs) != 0 {
		t.Fatalf("an absent document must report nothing; got %v, %v", errs, err)
	}
}
