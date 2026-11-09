// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// The export-list grid, driven through the real build.
//
// Which module a dotted target passes into depends on the document that wrote
// it, and which surface drew a connection is settled while the authoring tiers
// are read — so every row writes documents and compiles them, and asserts on the
// refusal a real build produces. Each refused row carries one fault: the target
// reaches a component by a name its module does not export. Each row that builds
// is the control for the refusals beside it.
//
// The catalog is the shared fixture directory (the stateful `core/db` type, its
// recipe and the renderer plugin), the consumer and attachment the alias grid
// declares, and two modules: `db-module`, holding `store` and `cache` and
// exporting `store` as `primary`; and `outer-module`, which imports `db-module`
// as `inner`, holds a consumer `user`, and re-exports `inner.primary` as
// `reexported`.

const exportDBModule = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: db-module
  version: "1.0.0"
spec:
  components:
    store:
      type: core/db
    cache:
      type: core/db
  exportedComponents:
    primary: store
`

// exportOuterModule is the module importing db-module, its consumer wired to
// userTarget, a name written in this module's own document.
func exportOuterModule(userTarget string) string {
	return `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: outer-module
  version: "1.0.0"
spec:
  components:
    user:
      type: core/consumer
      uses:
        databases:
          main:
            target: ` + userTarget + `
    peer:
      type: core/db
  aggregates:
    inner:
      type: db-module:1.0.0
  exportedComponents:
    reexported: inner.primary
`
}

// exportCase is one row: the consuming component's block in the root Aggregate,
// the Release's entry for it, the module the root imports, and the target the
// nested module's consumer names.
type exportCase struct {
	name string

	// app is the consuming component's block, beneath its `type:`.
	app string
	// releaseApp is the Release's entry for the component, beneath its key.
	releaseApp string
	// outer imports outer-module as `o` instead of db-module as `m`.
	outer bool
	// userTarget is what outer-module's consumer names; outer rows only.
	userTarget string
	// releaseUser, when set, is the target the Release wires outer-module's
	// consumer to under the alias `extra`, on the consumer's entry inside the
	// routing entry that places the module.
	releaseUser string

	// refusedAt is the key path the finding is positioned at, and module and
	// exports what it names; empty refusedAt is a row that must build.
	refusedAt string
	category  cerrors.Category
	written   string
	module    string
	exports   string
	// otherwise, when set, is the condition a row refused for another reason
	// halts on, with no finding of this rule beside it.
	otherwise error
}

func (c exportCase) aggregate() string {
	module := "    m:\n      type: db-module:1.0.0\n"
	if c.outer {
		module = "    o:\n      type: outer-module:1.0.0\n"
	}
	return `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: export-app
  version: "1.0.0"
spec:
  components:
    app:
      type: core/consumer
` + indented(c.app, 6) + `  aggregates:
` + module
}

func (c exportCase) release() string {
	module := "m"
	if c.outer {
		module = "o"
	}
	doc := `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: export-release
  environment: local
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: alias-manifest
  boundaries:
    cluster:
      directory: ./dist/cluster
`
	if c.releaseUser != "" {
		// The routing entry places the whole module and carries an entry for the
		// consumer inside it, under the local name the module gives it; the target
		// is named by the id it carries in the composition, the Release's own
		// spelling.
		return doc + `      aggregates:
        o:
          components:
            user:
              uses:
                databases:
                  extra:
                    target: ` + c.releaseUser + `
      components:
        app: {}
`
	}
	doc += "      aggregates:\n        " + module + ": {}\n      components:\n"
	if c.releaseApp == "" {
		return doc + "        app: {}\n"
	}
	return doc + "        app:\n" + indented(c.releaseApp, 10)
}

func writeExportWorkspace(t *testing.T, c exportCase) pipelinetest.Input {
	t.Helper()
	dir := t.TempDir()
	userTarget := c.userTarget
	if userTarget == "" {
		userTarget = "inner.primary"
	}
	for rel, content := range map[string]string{
		"aggregate.yaml":                       c.aggregate(),
		"manifest.yaml":                        aliasManifest,
		"release.yaml":                         c.release(),
		"catalog/components/consumer.yaml":     aliasConsumer,
		"catalog/features/marker.yaml":         aliasMarker,
		"catalog/recipes/consumer.yaml":        aliasConsumerRecipe,
		"catalog/feature-recipes/marker.yaml":  aliasMarkerRecipe,
		"catalog/aggregates/db-module.yaml":    exportDBModule,
		"catalog/aggregates/outer-module.yaml": exportOuterModule(userTarget),
	} {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return pipelinetest.Input{
		ReleasePath:  filepath.Join(dir, "release.yaml"),
		CatalogPaths: []string{filepath.Join(dir, "catalog"), aliasSharedCatalog},
	}
}

// databases writes one connection on the `databases` socket under the alias
// `main`.
func databases(target string) string {
	return "uses:\n  databases:\n    main:\n      target: " + target + "\n"
}

// TestAConnectionReachesAModulesComponentsOnlyThroughItsExports enumerates the
// surfaces that draw a connection — the component's own block, an attachment's
// block and the Release — against the names a target can reach a module's
// component by: an exported alias, an alias spelled as the component's own name,
// a re-exported alias, the component's id inside the module, a component the
// module does not export at all, and a path through the module into one nested
// inside it. A module's own document is a surface too, reaching the module it
// imports in turn.
func TestAConnectionReachesAModulesComponentsOnlyThroughItsExports(t *testing.T) {
	t.Parallel()

	const (
		own        = ".spec.components.app.uses.databases.main"
		ownFeature = ".spec.components.app.features.marker.uses.databases.main"
		releaseApp = ".spec.boundaries.cluster.components.app.uses.databases.main"
		moduleUser = ".spec.components.user.uses.databases.main"
		// A connection the Release wires on a component it reaches through a
		// routing entry is positioned at the boundary's component block, as every
		// rule over the connection set positions one.
		releaseUser = ".spec.boundaries.cluster.components"
	)
	cases := []exportCase{
		// Controls.
		{name: "control: an exported alias builds", app: databases("m.primary")},
		{name: "control: the Release wires an exported alias", releaseApp: databases("m.primary")},
		{name: "control: an attachment names an exported alias",
			app: "features:\n" + indented(attachment("marker", "databases", "main", "m.primary"), 2)},
		{name: "control: a re-exported alias reaches through the module that re-exports it",
			outer: true, app: databases("o.reexported")},
		{name: "control: a module reaches the module it imports through that module's alias",
			outer: true, userTarget: "inner.primary"},
		{name: "control: the Release wires two components of one module to each other",
			outer: true, releaseUser: "o.peer"},
		{name: "control: the Release wires a module's consumer through the alias of the module it imports",
			outer: true, releaseUser: "o.inner.primary"},

		// One fault each: a name the module does not export.
		{
			name: "the component's id inside the module, on its own block", app: databases("m.store"),
			refusedAt: own, category: cerrors.DeveloperError,
			written: "m.store", module: "m", exports: "primary",
		},
		{
			name: "a component the module does not export at all", app: databases("m.cache"),
			refusedAt: own, category: cerrors.DeveloperError,
			written: "m.cache", module: "m", exports: "primary",
		},
		{
			name:      "the component's id inside the module, on an attachment's block",
			app:       "features:\n" + indented(attachment("marker", "databases", "main", "m.store"), 2),
			refusedAt: ownFeature, category: cerrors.DeveloperError,
			written: "m.store", module: "m", exports: "primary",
		},
		{
			name: "the component's id inside the module, wired by the Release", releaseApp: databases("m.store"),
			refusedAt: releaseApp, category: cerrors.OpsError,
			written: "m.store", module: "m", exports: "primary",
		},
		{
			name: "a path through the module into the module nested inside it", outer: true,
			app:       databases("o.inner.primary"),
			refusedAt: own, category: cerrors.DeveloperError,
			written: "o.inner.primary", module: "o", exports: "reexported",
		},
		{
			name: "a module naming the component's id inside the module it imports", outer: true,
			userTarget: "inner.store",
			refusedAt:  moduleUser, category: cerrors.DeveloperError,
			written: "inner.store", module: "inner", exports: "primary",
		},
		{
			name:  "the Release wiring a module's consumer past the export list of the module it imports",
			outer: true, releaseUser: "o.inner.store",
			refusedAt: releaseUser, category: cerrors.OpsError,
			written: "o.inner.store", module: "o.inner", exports: "primary",
		},

		// A target naming nothing is the dangling-target rule's alone.
		{
			name: "a name no component answers to is reported as dangling, not as unexported",
			app:  databases("m.nothing"), otherwise: topology.ErrDanglingUsesTarget(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := pipelinetest.BuildE(t, writeExportWorkspace(t, tc))
			findings := pipelinetest.Findings(err, topology.ErrUsesTargetNotExported())

			switch {
			case tc.otherwise != nil:
				if !errors.Is(err, tc.otherwise) {
					t.Fatalf("the workspace must halt on %v; got %v", tc.otherwise, err)
				}
				if len(findings) != 0 {
					t.Errorf("the export rule reported a target no component answers to; got %v", err)
				}
				return
			case tc.refusedAt == "":
				if err != nil {
					t.Fatalf("the workspace must build, got halt: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatal("the workspace built.\n" +
					"INVARIANT: a connection reaches a module's component only through a name the module exports.\n" +
					"CONSEQUENCE: a connection binds to a component the module's author never offered, and the " +
					"author can rename or remove it without anything saying the connection depended on it.")
			}
			if len(findings) != 1 {
				t.Fatalf("the refusal carries %d export finding(s); want exactly 1; got %v", len(findings), err)
			}
			finding := findings[0]
			if got := finding.Source().Locator; got != tc.refusedAt {
				t.Errorf("the finding is positioned at %q; want %q, the entry that wrote the target", got, tc.refusedAt)
			}
			if got := finding.Category(); got != tc.category {
				t.Errorf("the finding is filed under %q; want %q, the document that wrote the target", got, tc.category)
			}
			for _, want := range []string{
				`"` + tc.written + `"`,
				`module "` + tc.module + `"`,
				`which exports "` + tc.exports + `"`,
				`wired into socket "databases"`,
			} {
				if !strings.Contains(finding.Error(), want) {
					t.Errorf("the finding does not name %s; got: %s", want, finding.Error())
				}
			}
		})
	}
}
