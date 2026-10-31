// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for where a halt over an imported schema is located.
//
// Content an `$import:` substitutes keeps the line numbers of the file it was read
// from, so a halt over that content is located in that file, beside the line. Each
// row asserts the file AND the line, because the defect these rows guard is the two
// coming from different documents: a row asserting only the line would pass with
// the entity's path beside it, and a row asserting only the file would pass with a
// line of the other document.
//
// Every halt that can stand over imported content is a row — a contract the
// schema-contract reading refuses, a pillar body that does not compile, and a fault
// in an import directive written inside an imported file — and each is paired with
// the same fault written in the entity itself, which must stay located in the
// entity.

package catalog

import (
	"errors"
	"path/filepath"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// haltStage names which reading of the entity raises the halt a row expects.
type haltStage int

const (
	// atParse is the per-entity parse, where import directives are substituted.
	atParse haltStage = iota
	// atPillarCompile is the pillar validation, where a pillar body that does not
	// compile is refused.
	atPillarCompile
	// atContractReading is the schema-contract reading, where a contract no
	// validator can be built from is refused.
	atContractReading
)

// TestSchemaImport_HaltsLocateTheImportedFile drives each halt through the reading
// that raises it and asserts the location it reports.
func TestSchemaImport_HaltsLocateTheImportedFile(t *testing.T) {
	cases := []struct {
		name  string
		spec  string
		files map[string]string
		stage haltStage
		// wantFile is the file the halt names, relative to the entity's directory;
		// empty means the entity itself.
		wantFile string
		wantLine int
	}{
		{
			name:     "whole schema imported: a contract no validator can be built from",
			spec:     "  schema:\n    $import: ./contract/schema.yaml\n",
			files:    map[string]string{"contract/schema.yaml": "# one\n# two\nvalues:\n  model: { type: string, pattern: \"[a-\" }\n"},
			stage:    atContractReading,
			wantFile: "contract/schema.yaml",
			wantLine: 4,
		},
		{
			name:     "one pillar imported: a contract no validator can be built from",
			spec:     "  schema:\n    values:\n      $import: ./contract/values.yaml\n",
			files:    map[string]string{"contract/values.yaml": "# one\n# two\nmodel: { type: string, pattern: \"[a-\" }\n"},
			stage:    atContractReading,
			wantFile: "contract/values.yaml",
			wantLine: 3,
		},
		{
			name: "socket contract imported: a body that does not compile",
			spec: "  usesSchema:\n    db:\n      selector: \"component.aggrega.ai/type=database\"\n" +
				"      valueSchema:\n        $import: ./contract/edge.yaml\n",
			files:    map[string]string{"contract/edge.yaml": "# one\n# two\nport: { type: object, min: 5 }\n"},
			stage:    atContractReading,
			wantFile: "contract/edge.yaml",
			wantLine: 3,
		},
		{
			name:     "whole schema imported: a pillar body that does not compile",
			spec:     "  schema:\n    $import: ./contract/schema.yaml\n",
			files:    map[string]string{"contract/schema.yaml": "# one\n# two\nvalues:\n  port: { type: object, min: 5 }\n"},
			stage:    atPillarCompile,
			wantFile: "contract/schema.yaml",
			wantLine: 4,
		},
		{
			name: "chained import: a malformed directive written in the imported file",
			spec: "  schema:\n    values:\n      $import: ./a.yaml\n",
			files: map[string]string{
				"a.yaml": "# one\n# two\n$import: ./b.yaml\nextra: 1\n",
				"b.yaml": "greeting: { type: string }\n",
			},
			stage:    atParse,
			wantFile: "a.yaml",
			wantLine: 3,
		},
		{
			name:     "control, written in the entity: a contract no validator can be built from",
			spec:     "  schema:\n    values:\n      model: { type: string, pattern: \"[a-\" }\n",
			stage:    atContractReading,
			wantLine: 16,
		},
		{
			name: "control, written in the entity: a socket body that does not compile",
			spec: "  usesSchema:\n    db:\n      selector: \"component.aggrega.ai/type=database\"\n" +
				"      valueSchema:\n        port: { type: object, min: 5 }\n",
			stage:    atContractReading,
			wantLine: 18,
		},
		{
			name:     "control, written in the entity: a pillar body that does not compile",
			spec:     "  schema:\n    values:\n      port: { type: object, min: 5 }\n",
			stage:    atPillarCompile,
			wantLine: 16,
		},
		{
			name:     "control, written in the entity: a malformed directive",
			spec:     "  schema:\n    values:\n      $import: ./c.yaml\n      extra: 1\n",
			files:    map[string]string{"c.yaml": "greeting: { type: string }\n"},
			stage:    atParse,
			wantLine: 16,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := schemaImportFixture(t, tc.spec, tc.files)
			halt := haltFor(t, path, tc.stage)
			if halt == nil {
				t.Fatal("expected a halt; the entity passed")
			}
			var located *cerrors.CompilerError
			if !errors.As(halt, &located) {
				t.Fatalf("halt carries no source location: %v", halt)
			}
			wantFile := path
			if tc.wantFile != "" {
				wantFile = filepath.Join(filepath.Dir(path), filepath.FromSlash(tc.wantFile))
			}
			got := located.Source()
			if got.File != wantFile || got.Line != tc.wantLine {
				t.Errorf("halt located at %s:%d; want %s:%d\nhalt: %v", got.File, got.Line, wantFile, tc.wantLine, halt)
			}
		})
	}
}

// haltFor reads the entity at path through the stage a row names and returns the
// halt that stage raised, or nil when it raised none.
func haltFor(t *testing.T, path string, stage haltStage) error {
	t.Helper()
	comp, err := parseComponentEntity(mustRead(t, path), path)
	if stage == atParse {
		return err
	}
	if err != nil {
		t.Fatalf("parse Component: %v", err)
	}
	switch stage {
	case atPillarCompile:
		return validator.ValidateSchemaPillars(
			"Component", comp.Metadata.Name, path, comp.Spec.Extends, &comp.Spec.Schema, nil,
		)
	case atContractReading:
		return firstContractHalt(t, path, comp)
	default:
		t.Fatalf("unknown stage %d", stage)
		return nil
	}
}

// firstContractHalt runs the schema-contract reading over one Component and
// returns its single halt. A row carries one fault, so a second halt means the
// fixture reached a fault it was not written to carry.
func firstContractHalt(t *testing.T, path string, comp types.Component) error {
	t.Helper()
	errs := UnusablePillarContractErrors(
		"Component", comp.Metadata.Name, path, comp.Spec.Extends,
		&comp.Spec.Schema, comp.Spec.UsesSchema, nil,
	)
	switch len(errs) {
	case 0:
		return nil
	case 1:
		return errs[0]
	default:
		t.Fatalf("expected one halt, got %d: %v", len(errs), errors.Join(errs...))
		return nil
	}
}
