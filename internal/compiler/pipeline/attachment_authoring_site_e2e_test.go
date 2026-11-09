// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// End-to-end proofs that a finding about a Feature attachment is positioned at
// the block that wrote the attachment, in the document whose author wrote it,
// and is filed under that author — whichever of the blocks able to write an
// attachment did: the component's own block or a subsystem in the Aggregate, a
// boundary or the entry for the component in the Release. Every row compiles a
// real workspace through the entry point the validation surfaces use, writes the
// attachment in one place, and reads the finding the way those surfaces do: its
// condition by identity, its position by the file and the authoring key it
// carries, and the surface it is filed under.

package pipeline_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// authoringSiteEntities are the catalog entities the rows attach and place, added
// beside the committed connection-attachment catalog: a component type declaring
// a slot whose selector admits one grade of Feature, one declaring a slot that
// must be filled, and Features that fail an applicability, fit the slot, and
// require a value no Release can write.
var authoringSiteEntities = map[string]string{
	"components/site-app.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: site/app
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  featuresSchema:
    grant:
      selector: "site.example/grade=managed"
  schema:
    provides:
      host: { type: string, required: true }
`,
	"components/site-strict.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: site/strict
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  featuresSchema:
    grant:
      required: true
  schema:
    provides:
      host: { type: string, required: true }
`,
	"recipes/site-app-recipe.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: site-app-recipe
  version: "1.0.0"
spec:
  capability: compose
  implements: site/app
  provides:
    host: "{{ .context.instance_id }}"
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    containers:
      main:
        image: app:1.0
`,
	"recipes/site-strict-recipe.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: site-strict-recipe
  version: "1.0.0"
spec:
  capability: compose
  implements: site/strict
  provides:
    host: "{{ .context.instance_id }}"
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    containers:
      main:
        image: strict:1.0
`,
	"features/site-workers-only.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Feature
metadata:
  name: feature.site/workers-only
  version: "1.0.0"
spec:
  appliesTo: "component.aggrega.ai/type=worker"
  schema:
    infra:
      interval: { type: string }
`,
	"features/site-managed.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Feature
metadata:
  name: feature.site/managed
  version: "1.0.0"
  labels:
    site.example/grade: managed
spec:
  schema:
    values:
      note: { type: string }
`,
	"features/site-needs-level.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Feature
metadata:
  name: feature.site/needs-level
  version: "1.0.0"
spec:
  schema:
    values:
      level: { type: string, required: true }
`,
}

// authoringSiteManifest maps the two component types the rows place. No
// Feature is mapped: the validation entry point reads no FeatureRecipe.
const authoringSiteManifest = `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: authoring-sites-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        site/app: site-app-recipe:1.0.0
        site/strict: site-strict-recipe:1.0.0
`

// authoringSiteWorkspace writes one row's workspace: the committed catalog with
// the entities above beside it, the Manifest above, an Aggregate whose spec is
// the given body, and a Release placing the component "app" in the boundary
// "cluster" with the given boundary-wide attachments and the given keys under
// the component's own entry. Both strings are already indented to the depth
// their keys sit at; an empty one writes nothing there.
func authoringSiteWorkspace(t *testing.T, aggregateSpec, boundaryFeatures, appEntry string) pipelinetest.Input {
	t.Helper()
	dir := t.TempDir()
	catalog := filepath.Join(dir, "catalog")
	if err := os.CopyFS(catalog, os.DirFS(connectionAttachmentDir+"catalog/base")); err != nil {
		t.Fatalf("copy the catalog: %v", err)
	}
	for name, body := range authoringSiteEntities {
		path := filepath.Join(catalog, name)
		if _, err := os.Stat(path); err == nil {
			t.Fatalf("the committed catalog already holds %s; this row would overwrite an entity other proofs read", name)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	app := "        app: {}\n"
	if appEntry != "" {
		app = "        app:\n" + appEntry
	}
	documents := map[string]string{
		filepath.Join(dir, "manifest.yaml"): authoringSiteManifest,
		filepath.Join(dir, "aggregate.yaml"): "apiVersion: aggrega.ai/v1beta1\nkind: Aggregate\n" +
			"metadata:\n  name: authoring-sites\n  version: \"1.0.0\"\nspec:\n" + aggregateSpec,
		filepath.Join(dir, "release.yaml"): "apiVersion: aggrega.ai/v1beta1\nkind: Release\n" +
			"metadata:\n  name: authoring-sites-release\n  environment: local\n  version: \"1.0.0\"\n" +
			"spec:\n  aggregate: ./aggregate.yaml\n  manifest: ./manifest.yaml\n  boundaries:\n" +
			"    cluster:\n      directory: ./dist/cluster\n" + boundaryFeatures +
			"      components:\n" + app,
	}
	for path, body := range documents {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return pipelinetest.Input{
		ReleasePath:  filepath.Join(dir, "release.yaml"),
		CatalogPaths: []string{catalog},
	}
}

// The Aggregate spec bodies the rows write: the component alone, the component
// with an attachment block, and the component inside a subsystem carrying a
// subsystem-wide attachment block.
func appAlone(componentType string) string {
	return "  components:\n    app:\n      type: " + componentType + "\n"
}

func appAttaching(componentType, attachments string) string {
	return appAlone(componentType) + "      features:\n" + attachments
}

func subsystemAttaching(componentType, attachments string) string {
	return "  subsystems:\n    tier:\n      features:\n" + attachments +
		"      components:\n        app:\n          type: " + componentType + "\n"
}

// attachment is one attachment block entry under name, at the given depth of
// indentation, carrying the given lines beneath its key. An empty featureType
// writes no type.
func attachment(depth int, name, featureType string, lines ...string) string {
	pad := strings.Repeat(" ", depth)
	out := pad + name + ":\n"
	if featureType != "" {
		out += pad + "  type: " + featureType + "\n"
	}
	for _, line := range lines {
		out += pad + "  " + line + "\n"
	}
	if featureType == "" && len(lines) == 0 {
		out = pad + name + ": {}\n"
	}
	return out
}

// The depths an attachment entry sits at in each block a row writes it in.
const (
	inAggregateComponent = 8
	inAggregateSubsystem = 8
	inReleaseBoundary    = 8
	inReleaseComponent   = 12
)

// boundaryAttaching is the Release boundary's own attachment block.
func boundaryAttaching(attachments string) string {
	return "      features:\n" + attachments
}

// entryAttaching is the Release's entry for the component, writing an
// attachment block.
func entryAttaching(attachments string) string {
	return "          features:\n" + attachments
}

// TestAnAttachmentFindingIsPositionedWhereItsAuthorWroteIt proves the rules
// over an attachment a component carries name the block the attachment was
// written in, in the document whose author wrote it, and file the finding under
// that author: the applicability a Feature declares, the selector a slot
// declares over its fill, and the rule over a fill written under another name.
// Each condition has a row per block that can write the attachment alone, and
// the Aggregate's own block is the row that pins what a developer always saw.
//
// Two rows pin which block positions an attachment several blocks wrote: the
// developer's wherever the developer wrote one, and otherwise the block that
// named the attachment's Feature type rather than one that only sized it.
func TestAnAttachmentFindingIsPositionedWhereItsAuthorWroteIt(t *testing.T) {
	t.Parallel()

	const (
		workersOnly = "feature.site/workers-only"
		managed     = "feature.site/managed"
		token       = "feature.conn/token"
	)
	for _, row := range []struct {
		name      string
		condition error
		aggregate string
		boundary  string
		entry     string
		want      socketSlotFinding
	}{
		{
			name:      "an applicability the component fails, attached in the Aggregate's component block",
			condition: analyzer.ErrSelectorViolation(),
			aggregate: appAttaching("site/app", attachment(inAggregateComponent, "metrics", workersOnly)),
			want:      socketSlotFinding{file: "aggregate.yaml", field: ".spec.components.app.features.metrics"},
		},
		{
			name:      "the same applicability, attached on an Aggregate subsystem",
			condition: analyzer.ErrSelectorViolation(),
			aggregate: subsystemAttaching("site/app", attachment(inAggregateSubsystem, "metrics", workersOnly)),
			want:      socketSlotFinding{file: "aggregate.yaml", field: ".spec.subsystems.tier.features.metrics"},
		},
		{
			name:      "the same applicability, attached on the Release's entry for the component",
			condition: analyzer.ErrSelectorViolation(),
			aggregate: appAlone("site/app"),
			entry:     entryAttaching(attachment(inReleaseComponent, "metrics", workersOnly)),
			want: socketSlotFinding{
				file: "release.yaml", field: ".spec.boundaries.cluster.components.app.features.metrics",
			},
		},
		{
			name:      "the same applicability, attached on a Release boundary",
			condition: analyzer.ErrSelectorViolation(),
			aggregate: appAlone("site/app"),
			boundary:  boundaryAttaching(attachment(inReleaseBoundary, "metrics", workersOnly)),
			want:      socketSlotFinding{file: "release.yaml", field: ".spec.boundaries.cluster.features.metrics"},
		},
		{
			name:      "an attachment the Aggregate types and the Release sizes is the developer's",
			condition: analyzer.ErrSelectorViolation(),
			aggregate: appAttaching("site/app", attachment(inAggregateComponent, "metrics", workersOnly)),
			entry:     entryAttaching(attachment(inReleaseComponent, "metrics", "", "infra: { interval: 5s }")),
			want:      socketSlotFinding{file: "aggregate.yaml", field: ".spec.components.app.features.metrics"},
		},
		{
			name:      "an attachment a Release boundary types and the component's entry sizes is the boundary's",
			condition: analyzer.ErrSelectorViolation(),
			aggregate: appAlone("site/app"),
			boundary:  boundaryAttaching(attachment(inReleaseBoundary, "metrics", workersOnly)),
			entry:     entryAttaching(attachment(inReleaseComponent, "metrics", "", "infra: { interval: 5s }")),
			want:      socketSlotFinding{file: "release.yaml", field: ".spec.boundaries.cluster.features.metrics"},
		},
		{
			name:      "a slot filled by a Feature its selector refuses, in the Aggregate's component block",
			condition: analyzer.ErrSlotSelectorUnsatisfied(),
			aggregate: appAttaching("site/app", attachment(inAggregateComponent, "grant", token)),
			want:      socketSlotFinding{file: "aggregate.yaml", field: ".spec.components.app.features.grant"},
		},
		{
			name:      "the same fill, written on the Release's entry for the component",
			condition: analyzer.ErrSlotSelectorUnsatisfied(),
			aggregate: appAlone("site/app"),
			entry:     entryAttaching(attachment(inReleaseComponent, "grant", token)),
			want: socketSlotFinding{
				file: "release.yaml", field: ".spec.boundaries.cluster.components.app.features.grant",
			},
		},
		{
			name:      "a fill written under another name beside the empty slot it fits, in the Aggregate",
			condition: resolver.ErrMisnamedSlotFill(),
			aggregate: appAttaching("site/app", attachment(inAggregateComponent, "extra", managed)),
			want:      socketSlotFinding{file: "aggregate.yaml", field: ".spec.components.app.features.extra"},
		},
		{
			name:      "the same misnamed fill, written on the Release's entry for the component",
			condition: resolver.ErrMisnamedSlotFill(),
			aggregate: appAlone("site/app"),
			entry:     entryAttaching(attachment(inReleaseComponent, "extra", managed)),
			want: socketSlotFinding{
				file: "release.yaml", field: ".spec.boundaries.cluster.components.app.features.extra",
			},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			got := socketSlotFindings(t,
				authoringSiteWorkspace(t, row.aggregate, row.boundary, row.entry), row.condition)
			if len(got) != 1 {
				t.Fatalf("the run carries %d finding(s) for the condition %+v; want exactly one", len(got), got)
			}
			if got[0].file != row.want.file || got[0].field != row.want.field {
				t.Errorf("the finding is positioned at %s %s; want %s %s",
					got[0].file, got[0].field, row.want.file, row.want.field)
			}
			if wantCategory := filedUnder(row.want.file); got[0].category != wantCategory {
				t.Errorf("the finding, written in %s, is filed under %q; want %q",
					row.want.file, got[0].category, wantCategory)
			}
		})
	}
}

// TestARequiredSlotNobodyFilledNamesBothDocuments proves the finding about a
// required slot nothing fills keeps its position at the component's own
// attachment block in the Aggregate and its developer filing — no document wrote
// anything for the slot — and that its remedy names both documents, since either
// can fill it.
func TestARequiredSlotNobodyFilledNamesBothDocuments(t *testing.T) {
	t.Parallel()
	got := socketSlotFindings(t,
		authoringSiteWorkspace(t, appAlone("site/strict"), "", ""), analyzer.ErrRequiredSlotUnfilled())
	if len(got) != 1 {
		t.Fatalf("the run carries %d required-slot finding(s) %+v; want exactly one", len(got), got)
	}
	if got[0].file != "aggregate.yaml" || got[0].field != ".spec.components.app.features.grant" {
		t.Errorf("the finding is positioned at %s %s; want aggregate.yaml .spec.components.app.features.grant",
			got[0].file, got[0].field)
	}
	if got[0].category != cerrors.DeveloperError {
		t.Errorf("the finding is filed under %q; want %q", got[0].category, cerrors.DeveloperError)
	}
	if !strings.Contains(got[0].message, "in the Aggregate or in the Release") {
		t.Errorf("the remedy does not name both documents: %q", got[0].message)
	}
}

// conformanceFinding is one finding about an attachment's configuration not
// conforming to its Feature, read the way a surface reads a finding that halts
// the graph build: the document it names, the authoring key it is positioned
// at, the file that key is a path inside, and the surface it is filed under.
type conformanceFinding struct {
	entity   string
	field    string
	file     string
	category cerrors.Category
}

// conformanceFindings runs the workspace through the validation entry point and
// returns the conformance findings its refusal carries, failing the test where
// the run did not refuse or panicked instead of returning.
func conformanceFindings(t *testing.T, in pipelinetest.Input) []conformanceFinding {
	t.Helper()
	var err error
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("the run panicked instead of returning: %v", recovered)
			}
		}()
		_, err = pipelinetest.Tier4E(t, in)
	}()
	if !errors.Is(err, resolver.ErrSchemaConformance()) {
		t.Fatalf("the run must refuse the attachment's configuration; got %v", err)
	}
	var out []conformanceFinding
	var walk func(error)
	walk = func(e error) {
		if e == nil {
			return
		}
		var finding *resolver.ResolverError
		if direct, ok := e.(*resolver.ResolverError); ok {
			finding = direct
		}
		if finding != nil && errors.Is(finding, resolver.ErrSchemaConformance()) {
			out = append(out, conformanceFinding{
				entity:   finding.Entity,
				field:    finding.Field,
				file:     filepath.Base(finding.File),
				category: finding.ToCompilerError().Category(),
			})
			return
		}
		switch wrapped := e.(type) {
		case interface{ Unwrap() []error }:
			for _, inner := range wrapped.Unwrap() {
				walk(inner)
			}
		case interface{ Unwrap() error }:
			walk(wrapped.Unwrap())
		}
	}
	walk(err)
	return out
}

// TestAnAttachmentsMissingValueIsPositionedWhereItsAuthorWroteIt proves the
// finding about an attachment's configuration names the block the attachment
// was written in and that document, and is filed under its author. A value is
// the developer's to write, so an attachment only the Release made has no
// developer block to name, and its missing value is reported at the block the
// operator wrote.
func TestAnAttachmentsMissingValueIsPositionedWhereItsAuthorWroteIt(t *testing.T) {
	t.Parallel()

	const needsLevel = "feature.site/needs-level"
	for _, row := range []struct {
		name      string
		aggregate string
		boundary  string
		entry     string
		want      conformanceFinding
	}{
		{
			name:      "attached in the Aggregate's component block without the value",
			aggregate: appAttaching("site/app", attachment(inAggregateComponent, "check", needsLevel)),
			want: conformanceFinding{
				entity:   "Aggregate authoring-sites",
				field:    ".spec.components.app.features.check.values.level",
				file:     "aggregate.yaml",
				category: cerrors.DeveloperError,
			},
		},
		{
			name:      "attached on the Release's entry for the component",
			aggregate: appAlone("site/app"),
			entry:     entryAttaching(attachment(inReleaseComponent, "check", needsLevel)),
			want: conformanceFinding{
				entity:   "Release authoring-sites-release",
				field:    ".spec.boundaries.cluster.components.app.features.check.values.level",
				file:     "release.yaml",
				category: cerrors.OpsError,
			},
		},
		{
			name:      "attached on a Release boundary",
			aggregate: appAlone("site/app"),
			boundary:  boundaryAttaching(attachment(inReleaseBoundary, "check", needsLevel)),
			want: conformanceFinding{
				entity:   "Release authoring-sites-release",
				field:    ".spec.boundaries.cluster.features.check.values.level",
				file:     "release.yaml",
				category: cerrors.OpsError,
			},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			got := conformanceFindings(t, authoringSiteWorkspace(t, row.aggregate, row.boundary, row.entry))
			if len(got) != 1 {
				t.Fatalf("the refusal carries %d conformance finding(s) %+v; want exactly one", len(got), got)
			}
			if got[0] != row.want {
				t.Errorf("the finding = %+v; want %+v", got[0], row.want)
			}
		})
	}
}
