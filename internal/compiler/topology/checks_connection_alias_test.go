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
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// The one-alias-per-component grid, driven through the real build.
//
// The rule is invoked once per connection together with the surface that drew
// it, and both the surface and the place its entry was written are settled while
// the authoring tiers are read — a coordinate no hand-assembled input carries. So
// every row writes documents and compiles them, and asserts on the refusal a real
// build produces.
//
// The catalog is the shared fixture directory, which supplies the target
// component, its recipe and the renderer plugin, plus a per-test directory
// supplying a consumer declaring two sockets and a Feature declaring the same two.

const aliasSharedCatalog = "../pipeline/testdata/delivery-key-origin/catalog/base"

const aliasConsumer = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/consumer
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  usesSchema:
    models:
      selector: "component.aggrega.ai/type=stateful-server"
    databases:
      selector: "component.aggrega.ai/type=stateful-server"
  schema:
    provides:
      host: { type: string, required: true }
`

const aliasMarker = `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: core/marker
  version: "1.0.0"
spec:
  interoperability:
    conflictsWith: []
  usesSchema:
    models:
      selector: "component.aggrega.ai/type=stateful-server"
    databases:
      selector: "component.aggrega.ai/type=stateful-server"
`

const aliasConsumerRecipe = `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: consumer-recipe
  version: "1.0.0"
spec:
  capability: compose
  implements: core/consumer
  provides:
    host: "{{ .context.instance_id }}"
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    containers:
      main:
        image: consumer:1.0
        ports:
          p: 8080
`

const aliasMarkerRecipe = `apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: marker-compose
  version: "1.0.0"
spec:
  capability: compose
  implements: core/marker
`

const aliasManifest = `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: alias-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/consumer: consumer-recipe:1.0.0
        core/db: db-recipe:1.0.0
    features:
      type:
        core/marker: marker-compose:1.0.0
`

// aliasCase is one row: the consuming component's own block in the Aggregate,
// optionally a subsystem holding the components with an attachment block of its
// own, and the Release's entry for the component and its boundary's attachment
// block. The two targets are `db` and `db2`, both of a type every socket accepts.
type aliasCase struct {
	name string

	// app is the consuming component's block, beneath its `type:`.
	app string
	// subsystemFeatures, when set, places the components in a subsystem `core`
	// whose own attachment block it is.
	subsystemFeatures string
	// releaseApp is the Release's entry for the component, beneath its key.
	releaseApp string
	// boundaryFeatures is the Release boundary's own attachment block.
	boundaryFeatures string

	// refusedAt is the key path of the later connection's entry, where the one
	// alias finding is positioned; empty for a row that must build or that halts
	// on another condition.
	refusedAt string
	// category is what the finding is filed under: the document the later
	// connection was written in.
	category cerrors.Category
	// alias is the alias the finding names, `local` where it is left empty; a
	// list entry's alias is its target's name.
	alias string
	// names are the phrases the finding must carry about the connection already
	// holding the alias, and absent the ones it must not carry.
	names  []string
	absent []string
	// impossible, when set, is the condition a cell no build can reach halts on
	// instead — the reason the cell is impossible, held as a fact rather than a
	// comment.
	impossible error
}

func indented(block string, spaces int) string {
	if block == "" {
		return ""
	}
	pad := strings.Repeat(" ", spaces)
	var b strings.Builder
	for _, line := range strings.Split(strings.Trim(block, "\n"), "\n") {
		if line == "" {
			b.WriteString("\n")
			continue
		}
		b.WriteString(pad + line + "\n")
	}
	return b.String()
}

func (c aliasCase) aggregate() string {
	head := `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: alias-app
  version: "1.0.0"
spec:
`
	components := "db:\n  type: core/db\ndb2:\n  type: core/db\napp:\n  type: core/consumer\n" + indented(c.app, 2)
	if c.subsystemFeatures == "" {
		return head + "  components:\n" + indented(components, 4)
	}
	return head + "  subsystems:\n    core:\n      features:\n" + indented(c.subsystemFeatures, 8) +
		"      components:\n" + indented(components, 8)
}

func (c aliasCase) release() string {
	doc := `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: alias-release
  environment: local
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: alias-manifest
  boundaries:
    cluster:
      directory: ./dist/cluster
`
	if c.boundaryFeatures != "" {
		doc += "      features:\n" + indented(c.boundaryFeatures, 8)
	}
	doc += "      components:\n        db: {}\n        db2: {}\n"
	if c.releaseApp == "" {
		return doc + "        app: {}\n"
	}
	return doc + "        app:\n" + indented(c.releaseApp, 10)
}

// listPosition is how a finding names a connection's place in a list: the
// caption and the opening quote of its value. A row asserting that no place is
// named looks for this and never for the bare words, because the advice every
// finding of the condition carries explains what a list entry is, so the words
// alone appear in every finding whether or not a place is named.
const listPosition = `list entry "`

func writeAliasWorkspace(t *testing.T, c aliasCase) pipelinetest.Input {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range map[string]string{
		"aggregate.yaml":                      c.aggregate(),
		"manifest.yaml":                       aliasManifest,
		"release.yaml":                        c.release(),
		"catalog/components/consumer.yaml":    aliasConsumer,
		"catalog/features/marker.yaml":        aliasMarker,
		"catalog/recipes/consumer.yaml":       aliasConsumerRecipe,
		"catalog/feature-recipes/marker.yaml": aliasMarkerRecipe,
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

// One authored connection under an attachment, spelled once so each row reads as
// which surface writes which socket and alias.
func attachment(name, socket, alias, target string) string {
	return name + ":\n  type: core/marker\n  uses:\n    " + socket + ":\n      " + alias + ":\n        target: " + target + "\n"
}

// TestOneAliasNamesOneConnectionOfAComponent enumerates the CELLS of the rule's
// grid — the first connection's origin against the second's, over the
// component's own block, an attachment's block and a Release entry — rather than
// the margins of one axis, and then the rows the widened rule adds: one alias on
// two sockets, identical targets, and the attachments written at a scope.
//
// The composition draws a component's own connections first, then its
// attachments' — the component's, then its scopes', then the Release's — and the
// Release's own entries last. So a cell whose first origin is drawn later than
// its second is the transposed cell's pair, and is not listed twice.
//
// Three cells are impossible by construction, and each is a row asserting the
// condition that makes it so: a key written twice in one block of keyed entries
// does not load, in either document; and a Release entry at a socket and alias
// another block drew configures that connection or halts for redirecting it,
// rather than drawing a second one.
func TestOneAliasNamesOneConnectionOfAComponent(t *testing.T) {
	t.Parallel()

	const (
		own        = ".spec.components.app.uses."
		ownFeature = ".spec.components.app.features."
		releaseApp = ".spec.boundaries.cluster.components.app."
	)
	cases := []aliasCase{
		{
			name: "control: distinct aliases on two sockets and on an attachment build",
			app: "uses:\n  models:\n    local:\n      target: db\n  databases:\n    store:\n      target: db2\n" +
				"features:\n" + indented(attachment("marker", "models", "fast", "db2"), 2),
		},

		// The grid, one socket and one alias.
		{
			name:      "own block × own block, a list naming one target twice: refused at the list, by position",
			app:       "uses:\n  models: [db, db]\n",
			refusedAt: own + "models",
			category:  cerrors.DeveloperError,
			alias:     "db",
			names:     []string{`list entry "2"`, `list entry "1"`, `with target "db"`},
		},
		{
			name:       "own block × own block, one key written twice: impossible, the document does not load",
			app:        "uses:\n  models:\n    local:\n      target: db\n    local:\n      target: db2\n",
			impossible: resolver.ErrYAMLLoad(),
		},
		{
			name:      "own block × an attachment's block: refused at the attachment's entry",
			app:       "uses:\n  models:\n    local:\n      target: db\nfeatures:\n" + indented(attachment("marker", "models", "local", "db2"), 2),
			refusedAt: ownFeature + "marker.uses.models.local",
			category:  cerrors.DeveloperError,
			names:     []string{`"` + own + `models.local"`, `with target "db"`, `with target "db2"`},
		},
		{
			name:       "own block × a Release entry naming another target: impossible, the entry redirects and halts",
			app:        "uses:\n  models:\n    local:\n      target: db\n",
			releaseApp: "uses:\n  models:\n    local:\n      target: db2\n",
			impossible: topology.ErrReleaseEdgeRetarget(),
		},
		{
			name:       "own block × a Release entry naming the same target: the entry configures the one connection",
			app:        "uses:\n  models:\n    local:\n      target: db\n",
			releaseApp: "uses:\n  models:\n    local:\n      target: db\n",
		},
		{
			name: "an attachment's block × another attachment's block: refused at the later attachment",
			app: "features:\n" + indented(attachment("m1", "models", "local", "db"), 2) +
				indented(attachment("m2", "models", "local", "db2"), 2),
			refusedAt: ownFeature + "m2.uses.models.local",
			category:  cerrors.DeveloperError,
			names:     []string{`"` + ownFeature + `m1.uses.models.local"`},
		},
		{
			name:       "an attachment's block × a Release entry naming another target: impossible, the entry halts",
			app:        "features:\n" + indented(attachment("marker", "models", "local", "db"), 2),
			releaseApp: "uses:\n  models:\n    local:\n      target: db2\n",
			impossible: topology.ErrReleaseEdgeRetarget(),
		},
		{
			name:       "a Release entry × a Release entry, one key written twice: impossible, the document does not load",
			releaseApp: "uses:\n  models:\n    local:\n      target: db\n    local:\n      target: db2\n",
			impossible: resolver.ErrYAMLLoad(),
		},

		// One alias on two sockets.
		{
			name:      "one alias on two sockets of the component's own block: refused",
			app:       "uses:\n  models:\n    local:\n      target: db\n  databases:\n    local:\n      target: db2\n",
			refusedAt: own + "databases.local",
			category:  cerrors.DeveloperError,
			names:     []string{`"` + own + `models.local"`, `wired into socket "models"`, `wired into socket "databases"`},
		},
		{
			name:      "one alias on two sockets, each written as a list: refused, with no list position",
			app:       "uses:\n  models: [db]\n  databases: [db]\n",
			refusedAt: own + "databases",
			category:  cerrors.DeveloperError,
			alias:     "db",
			names:     []string{`"` + own + `models"`},
			absent:    []string{listPosition},
		},
		{
			name: "one alias on two sockets of one attachment's block: refused",
			app: "features:\n  marker:\n    type: core/marker\n    uses:\n      models:\n        local:\n          target: db\n" +
				"      databases:\n        local:\n          target: db2\n",
			refusedAt: ownFeature + "marker.uses.databases.local",
			category:  cerrors.DeveloperError,
			names:     []string{`"` + ownFeature + `marker.uses.models.local"`},
		},
		{
			name:       "own block × a Release entry on another socket: refused at the entry, filed under the Release",
			app:        "uses:\n  databases:\n    local:\n      target: db2\n",
			releaseApp: "uses:\n  models:\n    local:\n      target: db\n",
			refusedAt:  releaseApp + "uses.models.local",
			category:   cerrors.OpsError,
			names:      []string{`"` + own + `databases.local"`},
		},
		{
			name:       "a Release entry × a Release entry on two sockets: refused, filed under the Release",
			releaseApp: "uses:\n  models:\n    local:\n      target: db\n  databases:\n    local:\n      target: db2\n",
			refusedAt:  releaseApp + "uses.databases.local",
			category:   cerrors.OpsError,
			names:      []string{`"` + releaseApp + `uses.models.local"`},
		},

		// The attachments written at a scope, each beside the component's own
		// connection.
		{
			name:              "own block × a subsystem's attachment: refused at the subsystem's block",
			app:               "uses:\n  models:\n    local:\n      target: db\n",
			subsystemFeatures: attachment("smarker", "models", "local", "db2"),
			refusedAt:         ".spec.subsystems.core.features.smarker.uses.models.local",
			category:          cerrors.DeveloperError,
			names:             []string{`".spec.subsystems.core.components.app.uses.models.local"`},
		},
		{
			name:       "own block × an attachment the Release writes on the component: refused, filed under the Release",
			app:        "uses:\n  models:\n    local:\n      target: db\n",
			releaseApp: "features:\n" + indented(attachment("rmarker", "models", "local", "db2"), 2),
			refusedAt:  releaseApp + "features.rmarker.uses.models.local",
			category:   cerrors.OpsError,
			names:      []string{`"` + own + `models.local"`},
		},
		{
			name:             "own block × an attachment the Release writes on the boundary: refused, filed under the Release",
			app:              "uses:\n  models:\n    local:\n      target: db\n",
			boundaryFeatures: attachment("bmarker", "models", "local", "db2"),
			refusedAt:        ".spec.boundaries.cluster.features.bmarker.uses.models.local",
			category:         cerrors.OpsError,
			names:            []string{`"` + own + `models.local"`},
		},

		// The rows beside the grid.
		{
			name:      "identical targets: refused, because presence decides and not the target",
			app:       "uses:\n  models:\n    local:\n      target: db\nfeatures:\n" + indented(attachment("marker", "models", "local", "db"), 2),
			refusedAt: ownFeature + "marker.uses.models.local",
			category:  cerrors.DeveloperError,
			names:     []string{`"` + own + `models.local"`},
		},
		{
			name: "distinct aliases on one socket: builds",
			app:  "uses:\n  models:\n    local:\n      target: db\n    other:\n      target: db2\n",
		},
		{
			name:              "one attachment name at two scopes: refused at the block the composition records, for both",
			app:               "features:\n" + indented(attachment("marker", "models", "local", "db"), 2),
			subsystemFeatures: attachment("marker", "models", "local", "db2"),
			refusedAt:         ".spec.subsystems.core.components.app.features.marker.uses.models.local",
			category:          cerrors.DeveloperError,
			names: []string{
				`at ".spec.subsystems.core.components.app.features.marker.uses.models.local", the connection already`,
				`with target "db"`, `with target "db2"`,
			},
			absent: []string{listPosition},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := pipelinetest.BuildE(t, writeAliasWorkspace(t, tc))
			findings := pipelinetest.Findings(err, topology.ErrDuplicateConnectionAlias())

			switch {
			case tc.impossible != nil:
				if err == nil {
					t.Fatal("the workspace built, so the cell this row calls impossible was reached")
				}
				if !errors.Is(err, tc.impossible) {
					t.Fatalf("the workspace halted on another condition than the one that makes the cell "+
						"impossible: %v", err)
				}
				if len(findings) != 0 {
					t.Errorf("the alias rule reported a pair the composition never drew; got %v", err)
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
					"INVARIANT: one alias names one connection of a component, whatever socket and whatever drew it.\n" +
					"CONSEQUENCE: the later connection takes the alias's slot in what the component's Recipe or " +
					"assertions see, and the other is absent with nothing said.")
			}
			if len(findings) != 1 {
				t.Fatalf("the refusal carries %d alias finding(s); want exactly 1, at the later connection; got %v",
					len(findings), err)
			}
			finding := findings[0]
			if got := finding.Source().Locator; got != tc.refusedAt {
				t.Errorf("the finding is positioned at %q; want %q, the later connection's own entry", got, tc.refusedAt)
			}
			if got := finding.Category(); got != tc.category {
				t.Errorf("the finding is filed under %q; want %q, the document the later connection was written in",
					got, tc.category)
			}
			alias := tc.alias
			if alias == "" {
				alias = "local"
			}
			for _, want := range append([]string{`("` + alias + `"`, `on component "app"`}, tc.names...) {
				if !strings.Contains(finding.Error(), want) {
					t.Errorf("the finding does not name %s; got: %s", want, finding.Error())
				}
			}
			for _, unwanted := range tc.absent {
				if strings.Contains(finding.Error(), unwanted) {
					t.Errorf("the finding names %s, which it must not; got: %s", unwanted, finding.Error())
				}
			}
		})
	}
}
