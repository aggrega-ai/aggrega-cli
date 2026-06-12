// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"gopkg.in/yaml.v3"
)

// The schema-default walk itself (SchemaDefaults / PillarDefaults /
// MergeSchemaDefaults) is unit-tested in pkg/compactform, its home package.
// The tests here cover the ir-level wrapper componentPillarDefaults — schema
// compile + cross-library $ref resolution + per-pillar extraction end-to-end.

// TestComponentPillarDefaults_RealSchema is the end-to-end extraction: a
// MariaDB-shaped Component schema (flat values defaults + nested infra) is
// compiled and its declared defaults surface per pillar. This is the
// materialization a rendered template reads when the Aggregate omits the key.
func TestComponentPillarDefaults_RealSchema(t *testing.T) {
	t.Parallel()
	const componentYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/datastore/mariadb
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      database:
        type: string
        required: true
      charset:
        type: string
        default: utf8mb4
      collation:
        type: string
        default: utf8mb4_general_ci
    infra:
      port:
        type: integer
        default: 3306
      compute:
        cpu:
          type: string
          default: 250m
        memory:
          type: string
          default: 512Mi
`
	var comp types.Component
	if err := yaml.Unmarshal([]byte(componentYAML), &comp); err != nil {
		t.Fatalf("unmarshal component: %v", err)
	}

	values, infra := componentPillarDefaults(comp, nil)

	if v, _ := values.Get("charset"); v != "utf8mb4" {
		t.Errorf("values default charset = %v, want utf8mb4", v)
	}
	if v, _ := values.Get("collation"); v != "utf8mb4_general_ci" {
		t.Errorf("values default collation = %v", v)
	}
	if _, present := values.Get("database"); present {
		t.Error("a required field with no default must not be materialized")
	}
	if _, present := infra.Get("port"); !present {
		t.Error("infra default port not extracted")
	}
	compute, ok := infra.Get("compute")
	if !ok {
		t.Fatal("nested infra default compute missing")
	}
	computeOM := compute.(types.OrderedMap[any])
	if v, _ := computeOM.Get("cpu"); v != "250m" {
		t.Errorf("nested infra default compute.cpu = %v, want 250m", v)
	}
}

// TestComponentPillarDefaults_WholePillarRefYieldsNothing documents the remaining
// limit for a LOCAL in-document `#/$defs/` whole-pillar ref (passed no libraries):
// the compiled pillar has no direct `properties` map, so no defaults materialize.
// Cross-LIBRARY `$ref` resolution (whole-pillar and base+flavor against the
// catalog's SchemaLibraries) IS resolved when libraries are supplied — see
// TestComponentPillarDefaults_CrossLibraryRefMaterializes. Local in-document
// `#/$defs/` inlining and allOf/oneOf/anyOf remain the deferred remainder.
func TestComponentPillarDefaults_WholePillarRefYieldsNothing(t *testing.T) {
	t.Parallel()
	const componentYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/compute/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    $defs:
      DbParams:
        charset:
          type: string
          default: utf8mb4
    values:
      $ref: "#/$defs/DbParams"
`
	var comp types.Component
	if err := yaml.Unmarshal([]byte(componentYAML), &comp); err != nil {
		t.Fatalf("unmarshal component: %v", err)
	}

	values, _ := componentPillarDefaults(comp, nil)
	if values.Len() != 0 {
		t.Errorf("local #/$defs/ whole-pillar $ref must yield no defaults; got %v", values.Keys())
	}
}

// TestComponentPillarDefaults_NoSchema returns empty without error.
func TestComponentPillarDefaults_NoSchema(t *testing.T) {
	t.Parallel()
	values, infra := componentPillarDefaults(types.Component{}, nil)
	if values.Len() != 0 || infra.Len() != 0 {
		t.Errorf("component with no schema must yield empty defaults; got values=%v infra=%v", values.Keys(), infra.Keys())
	}
}

// loadSchemaLibraryFixture parses a SchemaLibrary YAML into the typed form the
// catalog holds and componentPillarDefaults receives.
func loadSchemaLibraryFixture(t *testing.T, y string) types.SchemaLibrary {
	t.Helper()
	var lib types.SchemaLibrary
	if err := yaml.Unmarshal([]byte(y), &lib); err != nil {
		t.Fatalf("unmarshal SchemaLibrary fixture: %v", err)
	}
	return lib
}

// TestComponentPillarDefaults_CrossLibraryRefMaterializes is the headline proof:
// a Component whose values pillar is a whole-pillar cross-library $ref into a
// SchemaLibrary declaring defaults materializes those defaults when the libraries
// are supplied — previously a no-op for the composed majority of catalog Components.
func TestComponentPillarDefaults_CrossLibraryRefMaterializes(t *testing.T) {
	t.Parallel()
	libs := map[string]types.SchemaLibrary{
		"BaseComponents": loadSchemaLibraryFixture(t, `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: BaseComponents
  version: "1.0.0"
spec:
  schemas:
    BaseParams:
      charset: { type: string, default: utf8mb4 }
      collation: { type: string, default: utf8mb4_general_ci }
`),
	}
	const componentYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/datastore/mariadb
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      $ref: "BaseComponents#/schemas/BaseParams"
`
	var comp types.Component
	if err := yaml.Unmarshal([]byte(componentYAML), &comp); err != nil {
		t.Fatalf("unmarshal component: %v", err)
	}

	// Control: without the libraries, the cross-library ref does not resolve and
	// no defaults materialize (the prior behavior).
	if v, _ := componentPillarDefaults(comp, nil); v.Len() != 0 {
		t.Fatalf("control: cross-library ref must yield nothing without libraries; got %v", v.Keys())
	}

	values, _ := componentPillarDefaults(comp, libs)
	if v, _ := values.Get("charset"); v != "utf8mb4" {
		t.Errorf("cross-library default charset = %v, want utf8mb4 (materialized from BaseComponents)", v)
	}
	if v, _ := values.Get("collation"); v != "utf8mb4_general_ci" {
		t.Errorf("cross-library default collation = %v, want utf8mb4_general_ci", v)
	}
}

// TestComponentPillarDefaults_BaseFlavorMaterializes proves the base+flavor pattern:
// a base cross-library $ref plus sibling properties, both carrying defaults, both
// materialize (the expander flat-merges them; no allOf handling needed).
func TestComponentPillarDefaults_BaseFlavorMaterializes(t *testing.T) {
	t.Parallel()
	libs := map[string]types.SchemaLibrary{
		"BaseComponents": loadSchemaLibraryFixture(t, `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: BaseComponents
  version: "1.0.0"
spec:
  schemas:
    BaseParams:
      charset: { type: string, default: utf8mb4 }
`),
	}
	const componentYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/datastore/mariadb-flavored
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      $ref: "BaseComponents#/schemas/BaseParams"
      properties:
        pool_size: { type: string, default: "10" }
`
	var comp types.Component
	if err := yaml.Unmarshal([]byte(componentYAML), &comp); err != nil {
		t.Fatalf("unmarshal component: %v", err)
	}

	values, _ := componentPillarDefaults(comp, libs)
	if v, _ := values.Get("charset"); v != "utf8mb4" {
		t.Errorf("base default charset = %v, want utf8mb4", v)
	}
	if v, _ := values.Get("pool_size"); v != "10" {
		t.Errorf("flavor default pool_size = %v, want 10", v)
	}
}

// TestComponentPillarDefaults_ModelComponentPatternMaterializes verifies the shipped
// fix against the standard library's pervasive model-component shape: a pillar that
// `$ref`s BaseComponents#/schemas/BaseModelParameter (carrying a base default) plus a
// sibling flavor property carrying its own default — the shape core/ai-model/openai and
// its siblings (anthropic, gemini, mistral, bedrock, …) use. Self-contained in-test
// (uncoupled from catalog content so it cannot drift), but shaped to the real
// catalog composition. strict_response_format is openai's real sibling; its boolean
// false default also
// exercises falsy-default extraction (a default of false must still materialize).
func TestComponentPillarDefaults_ModelComponentPatternMaterializes(t *testing.T) {
	t.Parallel()
	libs := map[string]types.SchemaLibrary{
		"BaseComponents": loadSchemaLibraryFixture(t, `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: BaseComponents
  version: "1.0.0"
spec:
  schemas:
    BaseModelParameter:
      temperature: { type: string, default: "1.0" }
`),
	}
	const componentYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/ai-model/openai
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      $ref: "BaseComponents#/schemas/BaseModelParameter"
      properties:
        strict_response_format: { type: boolean, default: false }
`
	var comp types.Component
	if err := yaml.Unmarshal([]byte(componentYAML), &comp); err != nil {
		t.Fatalf("unmarshal component: %v", err)
	}

	// Control: no libraries → the base $ref does not resolve, nothing materializes.
	if v, _ := componentPillarDefaults(comp, nil); v.Len() != 0 {
		t.Fatalf("control: model-component ref must yield nothing without libraries; got %v", v.Keys())
	}

	values, _ := componentPillarDefaults(comp, libs)
	if v, _ := values.Get("temperature"); v != "1.0" {
		t.Errorf("base (BaseModelParameter) default temperature = %v, want 1.0", v)
	}
	v, ok := values.Get("strict_response_format")
	if !ok {
		t.Fatalf("flavor sibling strict_response_format absent; a false default must still materialize. keys=%v", values.Keys())
	}
	if v != false {
		t.Errorf("flavor default strict_response_format = %v, want false", v)
	}
}

// TestComponentPillarDefaults_CrossLibraryDeterministic pins the deterministic-output
// invariant for the cross-library path: the same input materializes byte-identical
// output (key order and values) across runs — the sorted-key extraction is stable.
func TestComponentPillarDefaults_CrossLibraryDeterministic(t *testing.T) {
	t.Parallel()
	libs := map[string]types.SchemaLibrary{
		"BaseComponents": loadSchemaLibraryFixture(t, `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: BaseComponents
  version: "1.0.0"
spec:
  schemas:
    BaseParams:
      charset: { type: string, default: utf8mb4 }
      collation: { type: string, default: utf8mb4_general_ci }
      engine: { type: string, default: innodb }
`),
	}
	const componentYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/datastore/mariadb
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      $ref: "BaseComponents#/schemas/BaseParams"
`
	var comp types.Component
	if err := yaml.Unmarshal([]byte(componentYAML), &comp); err != nil {
		t.Fatalf("unmarshal component: %v", err)
	}

	first, _ := componentPillarDefaults(comp, libs)
	for i := 0; i < 5; i++ {
		got, _ := componentPillarDefaults(comp, libs)
		fk, gk := first.Keys(), got.Keys()
		if len(fk) != len(gk) {
			t.Fatalf("run %d: key count drift: %v vs %v", i, gk, fk)
		}
		for j, k := range fk {
			if gk[j] != k {
				t.Fatalf("run %d: key order drift at %d: %q vs %q", i, j, gk[j], k)
			}
			fv, _ := first.Get(k)
			gv, _ := got.Get(k)
			if fv != gv {
				t.Errorf("run %d: value drift for %q: %v vs %v", i, k, gv, fv)
			}
		}
	}
}
