// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for the schema-import substitution every schema-bearing catalog entity
// shares: an authored `$import:` on a schema surface is replaced, at load, with
// the content of the file it names, resolved against the directory of the entity
// that declared it.
//
// The load-time site is what makes a contract behind a pointer readable by every
// consumer at once, so these tests assert the RESOLVED ENTITY rather than the
// authored bytes: what a consumer sees is the whole property of interest, and a
// test asserting the pointer survived would pass against the state where nothing
// read it.
//
// Each negative row asserts the ONE fault its fixture carries. The rows are
// distinct faults rather than variations on "an error happened", because the
// substitution runs several gates in sequence and a row asserting any error would
// keep passing once an earlier gate began claiming its fixture.

package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/refcycle"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"go.yaml.in/yaml/v3"
)

// schemaImportFixture writes a Component at <root>/entity/component.yaml with the
// given spec body, drops each named file beside it with the given content, and
// chdirs the test into a separate directory carrying a DECOY copy of every one of
// those relative names.
//
// The decoy is what separates the two candidate resolution bases. Without it a
// resolution that kept the authored relative path would merely fail to find the
// file, which is indistinguishable from several other faults; with it, such a
// resolution succeeds and reads the wrong contract, which the assertions catch.
func schemaImportFixture(t *testing.T, specBody string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	entityDir := filepath.Join(root, "entity")
	runDir := filepath.Join(root, "elsewhere")
	for _, dir := range []string{entityDir, runDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, content := range files {
			target := filepath.Join(dir, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatal(err)
			}
			body := content
			if dir == runDir {
				body = "greeting: { type: string, enum: [DECOY] }\n"
			}
			if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	path := filepath.Join(entityDir, "component.yaml")
	doc := `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: local/compute/importer
  version: 1.0.0
  description: A component whose contract is imported from a file beside it.
  labels:
    component.aggrega.ai/type: server
    component.aggrega.ai/category: compute
  maintainers:
    - name: Test Fixture
      email: fixture@example.com
spec:
` + specBody
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(runDir)
	return path
}

// pillarLeafType reads schema.<pillar>.<leaf>.type out of a resolved schema node,
// which is what a consumer of the entity reads. Returning the empty string for
// anything missing keeps each caller's failure message about its own subject.
func pillarLeafType(t *testing.T, schema *yaml.Node, pillar, leaf string) string {
	t.Helper()
	var decoded map[string]map[string]map[string]any
	if err := schema.Decode(&decoded); err != nil {
		return ""
	}
	return stringAt(decoded[pillar][leaf]["type"])
}

// stringAt renders a decoded YAML scalar as a string, or the empty string when it
// is absent or not one.
func stringAt(v any) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

// TestSchemaImport_WholeSchemaReplacedFromFile covers the entity contract's
// single-file replacement mode: the whole `spec.schema` comes from one file.
func TestSchemaImport_WholeSchemaReplacedFromFile(t *testing.T) {
	path := schemaImportFixture(t, `  schema:
    $import: ./contract/schema.yaml
`, map[string]string{"contract/schema.yaml": `values:
  greeting: { type: string, enum: [hello] }
infra:
  image_tag: { type: string }
`})

	comp, err := parseComponentEntity(mustRead(t, path), path)
	if err != nil {
		t.Fatalf("parse Component: %v", err)
	}
	if got := pillarLeafType(t, &comp.Spec.Schema, "values", "greeting"); got != "string" {
		t.Errorf("values.greeting not read from the imported file: type=%q", got)
	}
	if got := pillarLeafType(t, &comp.Spec.Schema, "infra", "image_tag"); got != "string" {
		t.Errorf("infra.image_tag not read from the imported file: type=%q", got)
	}
}

// TestSchemaImport_PillarReplacedFromFile covers one pillar imported on its own,
// with its siblings authored inline beside it.
func TestSchemaImport_PillarReplacedFromFile(t *testing.T) {
	path := schemaImportFixture(t, `  schema:
    values:
      $import: ./contract/values.yaml
    infra:
      image_tag: { type: string }
`, map[string]string{"contract/values.yaml": `greeting: { type: string, enum: [hello] }
`})

	comp, err := parseComponentEntity(mustRead(t, path), path)
	if err != nil {
		t.Fatalf("parse Component: %v", err)
	}
	if got := pillarLeafType(t, &comp.Spec.Schema, "values", "greeting"); got != "string" {
		t.Errorf("values.greeting not read from the imported file: type=%q", got)
	}
	if got := pillarLeafType(t, &comp.Spec.Schema, "infra", "image_tag"); got != "string" {
		t.Errorf("the inline sibling pillar did not survive the import: type=%q", got)
	}
}

// TestSchemaImport_ResolvesAgainstEntityDirectory pins the resolution base. The
// fixture puts a DECOY of the same relative name in the process working
// directory, so a resolution that keeps the authored path reads the decoy's enum
// rather than failing — which is why the assertion reads the enum rather than
// asserting only that the import succeeded.
func TestSchemaImport_ResolvesAgainstEntityDirectory(t *testing.T) {
	path := schemaImportFixture(t, `  schema:
    values:
      $import: ./contract/values.yaml
`, map[string]string{"contract/values.yaml": `greeting: { type: string, enum: [BESIDE_THE_ENTITY] }
`})

	comp, err := parseComponentEntity(mustRead(t, path), path)
	if err != nil {
		t.Fatalf("parse Component: %v", err)
	}
	out, err := yaml.Marshal(&comp.Spec.Schema)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "BESIDE_THE_ENTITY") {
		t.Errorf("import did not resolve against the entity's own directory; schema:\n%s", out)
	}
	if strings.Contains(string(out), "DECOY") {
		t.Errorf("import resolved against the process working directory; schema:\n%s", out)
	}
}

// TestSchemaImport_SocketContractsReplacedFromFile covers the per-edge socket
// contracts, which the entity's own godoc says accept the same authoring modes as
// the entity-level schema field. Both pillars are asserted so a change reaching
// one and not the other cannot pass.
func TestSchemaImport_SocketContractsReplacedFromFile(t *testing.T) {
	path := schemaImportFixture(t, `  usesSchema:
    db:
      selector: "component.aggrega.ai/type=database"
      valueSchema:
        $import: ./contract/edge-values.yaml
      infraSchema:
        $import: ./contract/edge-infra.yaml
`, map[string]string{
		"contract/edge-values.yaml": "pool_size: { type: integer, default: 10 }\n",
		"contract/edge-infra.yaml":  "storage_class: { type: string }\n",
	})

	comp, err := parseComponentEntity(mustRead(t, path), path)
	if err != nil {
		t.Fatalf("parse Component: %v", err)
	}
	entry, ok := comp.Spec.UsesSchema.Get("db")
	if !ok {
		t.Fatal("usesSchema socket not loaded")
	}
	var vals map[string]map[string]any
	if err := entry.ValueSchema.Decode(&vals); err != nil {
		t.Fatalf("decode valueSchema: %v", err)
	}
	if stringAt(vals["pool_size"]["type"]) != "integer" {
		t.Errorf("valueSchema not read from the imported file: %v", vals)
	}
	var infra map[string]map[string]any
	if err := entry.InfraSchema.Decode(&infra); err != nil {
		t.Fatalf("decode infraSchema: %v", err)
	}
	if stringAt(infra["storage_class"]["type"]) != "string" {
		t.Errorf("infraSchema not read from the imported file: %v", infra)
	}
}

// TestSchemaImport_ChainResolves covers an imported file that is itself an
// import. The chain is what makes the cycle guard below meaningful.
func TestSchemaImport_ChainResolves(t *testing.T) {
	path := schemaImportFixture(t, `  schema:
    values:
      $import: ./a.yaml
`, map[string]string{
		"a.yaml": "$import: ./b.yaml\n",
		"b.yaml": "greeting: { type: string }\n",
	})

	comp, err := parseComponentEntity(mustRead(t, path), path)
	if err != nil {
		t.Fatalf("parse Component: %v", err)
	}
	if got := pillarLeafType(t, &comp.Spec.Schema, "values", "greeting"); got != "string" {
		t.Errorf("chained import not followed: type=%q", got)
	}
}

// TestSchemaImport_NoImportLeavesSchemaAsAuthored is the positive control for
// every negative row below: an entity that imports nothing must round-trip
// untouched, so a failure in one of those rows is about the import path and not
// about the substitution reaching entities it should not.
func TestSchemaImport_NoImportLeavesSchemaAsAuthored(t *testing.T) {
	path := schemaImportFixture(t, `  schema:
    values:
      greeting: { type: string, enum: [hello] }
`, nil)

	comp, err := parseComponentEntity(mustRead(t, path), path)
	if err != nil {
		t.Fatalf("parse Component: %v", err)
	}
	if got := pillarLeafType(t, &comp.Spec.Schema, "values", "greeting"); got != "string" {
		t.Errorf("an inline schema was disturbed: type=%q", got)
	}
}

// TestSchemaImport_InheritedMarkerUntouched covers the one non-map schema an
// entity may author. The substitution must pass over it rather than reject it:
// what a schema surface may be is the pillar validator's rule, not this one's.
func TestSchemaImport_InheritedMarkerUntouched(t *testing.T) {
	path := schemaImportFixture(t, `  extends: local/compute/base:1.0.0
  schema: inherited
`, nil)

	comp, err := parseComponentEntity(mustRead(t, path), path)
	if err != nil {
		t.Fatalf("parse Component: %v", err)
	}
	if comp.Spec.Schema.Value != "inherited" {
		t.Errorf("inheritance marker disturbed: %q", comp.Spec.Schema.Value)
	}
}

// TestSchemaImport_Refusals covers one fault per row, each asserted against the
// text naming that fault rather than against "an error occurred".
func TestSchemaImport_Refusals(t *testing.T) {
	cases := []struct {
		name    string
		spec    string
		files   map[string]string
		wantMsg string
	}{
		{
			name:    "file does not exist",
			spec:    "  schema:\n    values:\n      $import: ./missing.yaml\n",
			wantMsg: "read imported schema",
		},
		{
			name:    "imported content is not a map",
			spec:    "  schema:\n    values:\n      $import: ./prose.yaml\n",
			files:   map[string]string{"prose.yaml": "this is prose, not a schema\n"},
			wantMsg: "must be a YAML map of schema content",
		},
		{
			name:    "import is not the only key",
			spec:    "  schema:\n    values:\n      $import: ./c.yaml\n      greeting: { type: string }\n",
			files:   map[string]string{"c.yaml": "greeting: { type: string }\n"},
			wantMsg: "must be the only key",
		},
		{
			name:    "import value is not a path",
			spec:    "  schema:\n    values:\n      $import: {}\n",
			wantMsg: "value must be a path to a file",
		},
		{
			name:    "import value carries a registry host",
			spec:    "  schema:\n    values:\n      $import: oci://hub.example.com/schemalibrary/x#/schemas/Y\n",
			wantMsg: "not a registry location",
		},
		{
			name:    "import value is a library identity",
			spec:    "  schema:\n    values:\n      $import: Datastores#/schemas/PostgresPayload\n",
			wantMsg: "with `$ref:` instead",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := schemaImportFixture(t, tc.spec, tc.files)
			_, err := parseComponentEntity(mustRead(t, path), path)
			if err == nil {
				t.Fatalf("expected a refusal naming %q; the entity parsed", tc.wantMsg)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("refusal did not name this fault\n got: %v\nwant substring: %q", err, tc.wantMsg)
			}
		})
	}
}

// TestSchemaImport_RefusalsNameTheirCondition pins which condition each fault
// reports under, because the two are not one condition: a fault in the authored
// directive, or in the shape of what it yielded, is something the author got
// wrong in a document they wrote, while a file that is absent, unreadable or
// unparsable is a document that could not be loaded at all.
//
// Each row matches through the exported accessor rather than against the code the
// condition is registered under: a code is a published string that a comparison
// would keep compiling against after the condition moved behind it, and would
// then stop matching with nothing failing.
//
// Every row also asserts the condition it must NOT report. Without that half a
// row passes on a chain carrying both identities, which is exactly the state a
// split done wrongly produces.
func TestSchemaImport_RefusalsNameTheirCondition(t *testing.T) {
	cases := []struct {
		name    string
		spec    string
		files   map[string]string
		want    error
		notWant error
	}{
		{
			name:    "acquisition: the file the directive names is not there",
			spec:    "  schema:\n    values:\n      $import: ./missing.yaml\n",
			want:    resolver.ErrYAMLLoad(),
			notWant: ErrUnusableSchemaImport(),
		},
		{
			name:    "acquisition: the file was read and its text is not well-formed YAML",
			spec:    "  schema:\n    values:\n      $import: ./malformed.yaml\n",
			files:   map[string]string{"malformed.yaml": "greeting:\n\ttype: string\n"},
			want:    resolver.ErrYAMLLoad(),
			notWant: ErrUnusableSchemaImport(),
		},
		{
			name:    "shape: the directive shares its mapping with authored content",
			spec:    "  schema:\n    values:\n      $import: ./c.yaml\n      greeting: { type: string }\n",
			files:   map[string]string{"c.yaml": "greeting: { type: string }\n"},
			want:    ErrUnusableSchemaImport(),
			notWant: resolver.ErrYAMLLoad(),
		},
		{
			name:    "shape: the directive's value is not a path",
			spec:    "  schema:\n    values:\n      $import: {}\n",
			want:    ErrUnusableSchemaImport(),
			notWant: resolver.ErrYAMLLoad(),
		},
		{
			name:    "shape: what parsed is not a map of schema content",
			spec:    "  schema:\n    values:\n      $import: ./prose.yaml\n",
			files:   map[string]string{"prose.yaml": "this is prose, not a schema\n"},
			want:    ErrUnusableSchemaImport(),
			notWant: resolver.ErrYAMLLoad(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := schemaImportFixture(t, tc.spec, tc.files)
			_, err := parseComponentEntity(mustRead(t, path), path)
			if err == nil {
				t.Fatal("expected a refusal; the entity parsed")
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("refusal does not report the condition this fault belongs to: %v", err)
			}
			if errors.Is(err, tc.notWant) {
				t.Errorf("refusal also reports the condition this fault does not belong to: %v", err)
			}
		})
	}
}

// TestSchemaImport_CycleHalts covers an import chain that loops back on itself.
// It asserts the shared cycle sentinel rather than the message, so the row keeps
// covering the cycle if the wording moves.
func TestSchemaImport_CycleHalts(t *testing.T) {
	path := schemaImportFixture(t, `  schema:
    values:
      $import: ./a.yaml
`, map[string]string{
		"a.yaml": "$import: ./b.yaml\n",
		"b.yaml": "$import: ./a.yaml\n",
	})

	_, err := parseComponentEntity(mustRead(t, path), path)
	if err == nil {
		t.Fatal("expected a refusal; the cyclic chain parsed")
	}
	if !errors.Is(err, refcycle.ErrCycle()) {
		t.Errorf("cycle not reported through the shared sentinel: %v", err)
	}
}
