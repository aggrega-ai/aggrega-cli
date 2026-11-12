// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package lintops

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// stubComponentLookup returns a lookup serving exactly the given entities by the
// reference the caller asks for, plus a record of what it was asked. The record
// is what makes "the reference was never resolved" distinguishable from "the
// reference resolved and produced nothing".
func stubComponentLookup(entities map[string]types.Component) (func(string) (types.Component, bool), *[]string) {
	var asked []string
	return func(uri string) (types.Component, bool) {
		asked = append(asked, uri)
		c, ok := entities[uri]
		return c, ok
	}, &asked
}

func mustSchemaNode(t *testing.T, src string) yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(src), &n); err != nil {
		t.Fatalf("parse schema fixture: %v", err)
	}
	if n.Kind == yaml.DocumentNode && len(n.Content) == 1 {
		return *n.Content[0]
	}
	return n
}

func recipeHeader(implements string) lintEntityHeader {
	var h lintEntityHeader
	h.Kind = "Recipe"
	h.Metadata.Name = "broker-gateway"
	h.Metadata.Version = "1.0.0"
	h.Spec.Capability = "helm"
	h.Spec.Implements = implements
	return h
}

// TestImplementedComponentJSON_ResolvesOnlyForARecipeNamingAComponent pins the
// four cases that return nothing, each beside the case that returns something,
// so a helper that always resolved or never resolved would fail rather than look
// correct.
func TestImplementedComponentJSON_ResolvesOnlyForARecipeNamingAComponent(t *testing.T) {
	t.Parallel()
	comp := types.Component{
		Kind: "Component",
		Spec: types.ComponentSpec{
			Schema: mustSchemaNode(t, "provides:\n  endpoint: { type: string }\n"),
		},
	}
	comp.Metadata.Name = "core/ai-model/broker"
	entities := map[string]types.Component{"core/ai-model/broker:1.0.0": comp}

	t.Run("a Recipe naming a resolvable Component", func(t *testing.T) {
		lookup, asked := stubComponentLookup(entities)
		got := implementedComponentJSON(recipeHeader("core/ai-model/broker:1.0.0"), lookup)
		if len(got) == 0 {
			t.Fatal("expected the resolved Component as JSON; got nothing")
		}
		if len(*asked) != 1 || (*asked)[0] != "core/ai-model/broker:1.0.0" {
			t.Errorf("the reference is passed to the lookup verbatim, pin included; asked %v", *asked)
		}
		// The pin form reaches the lookup unmodified rather than being stripped
		// here: version selection is the lookup's, and a second stripping in this
		// helper would be a second home for it.
		var decoded map[string]any
		if err := json.Unmarshal(got, &decoded); err != nil {
			t.Fatalf("the payload value must be JSON: %v", err)
		}
		spec, ok := decoded["spec"].(map[string]any)
		if !ok {
			t.Fatalf("the payload carries the entity's spec; got %v", decoded)
		}
		schema, ok := spec["schema"].(map[string]any)
		if !ok {
			t.Fatalf("the payload carries the entity's schema; got %v", spec)
		}
		if _, ok := schema["provides"]; !ok {
			t.Errorf("the payload carries the provides pillar the rule reads; got %v", schema)
		}
	})

	t.Run("a kind other than Recipe never resolves", func(t *testing.T) {
		for _, kind := range []string{"Component", "Feature", "FeatureRecipe", "SchemaLibrary"} {
			header := recipeHeader("core/ai-model/broker:1.0.0")
			header.Kind = kind
			lookup, asked := stubComponentLookup(entities)
			if got := implementedComponentJSON(header, lookup); got != nil {
				t.Errorf("kind %s resolved a Component: %s", kind, got)
			}
			if len(*asked) != 0 {
				t.Errorf("kind %s consulted the lookup: %v", kind, *asked)
			}
		}
	})

	t.Run("a Recipe naming nothing never resolves", func(t *testing.T) {
		lookup, asked := stubComponentLookup(entities)
		if got := implementedComponentJSON(recipeHeader(""), lookup); got != nil {
			t.Errorf("expected nothing; got %s", got)
		}
		if len(*asked) != 0 {
			t.Errorf("an empty reference consulted the lookup: %v", *asked)
		}
	})

	t.Run("no lookup at all yields nothing rather than panicking", func(t *testing.T) {
		if got := implementedComponentJSON(recipeHeader("core/ai-model/broker:1.0.0"), nil); got != nil {
			t.Errorf("expected nothing; got %s", got)
		}
	})

	t.Run("a reference the lookup does not surface yields nothing", func(t *testing.T) {
		lookup, asked := stubComponentLookup(entities)
		if got := implementedComponentJSON(recipeHeader("core/ai-model/absent"), lookup); got != nil {
			t.Errorf("expected nothing; got %s", got)
		}
		if len(*asked) != 1 {
			t.Errorf("a miss still consults the lookup once; asked %v", *asked)
		}
	})
}

// TestBuildRuleInputs_PerRuleFieldsReachOnlyTheirOwnRule pins the payload
// partition. A field assigned to every input would make each rule's payload
// carry documents it never reads, and a rule added later would silently start
// receiving one.
func TestBuildRuleInputs_PerRuleFieldsReachOnlyTheirOwnRule(t *testing.T) {
	t.Parallel()
	shared := validator.Policies{AllowNonstandardNames: true}
	inputs := buildRuleInputs(ruleInputSources{
		Kind:                 "Recipe",
		Proposed:             json.RawMessage(`{"kind":"Recipe"}`),
		Previous:             json.RawMessage(`{"kind":"Recipe","previous":true}`),
		ImplementedComponent: json.RawMessage(`{"kind":"Component"}`),
		Policies:             shared,
		EntityPath:           "catalog/recipe/helm/broker/1.0.0/recipe.yaml",
		PrePublish:           true,
	})
	if len(inputs) != len(defaultRuleIDs(true)) {
		t.Fatalf("one input per dispatched rule: got %d, want %d", len(inputs), len(defaultRuleIDs(true)))
	}
	carriers := map[string][]string{}
	for _, in := range inputs {
		if len(in.ImplementedComponent) != 0 {
			carriers["implemented_component"] = append(carriers["implemented_component"], in.RuleID)
		}
		if len(in.Previous) != 0 {
			carriers["previous"] = append(carriers["previous"], in.RuleID)
		}
		if in.SourcePath != "" {
			carriers["source_path"] = append(carriers["source_path"], in.RuleID)
		}
		if len(in.Proposed) == 0 || in.Policies != shared {
			t.Errorf("rule %q lost the shared proposed/policies payload", in.RuleID)
		}
	}
	for field, wantRule := range map[string]string{
		"implemented_component": "provides-declared-by-component",
		"previous":              "schema-evolution",
		"source_path":           "category-matches-directory",
	} {
		got := carriers[field]
		if len(got) != 1 || got[0] != wantRule {
			t.Errorf("%s reached %v; exactly one rule reads it and that rule is %q", field, got, wantRule)
		}
	}
}

// seedProvidesWorkspace writes a workspace whose catalog directory holds the
// given entity documents, keyed by their path under that directory, and returns
// the resolved workspace.
func seedProvidesWorkspace(t *testing.T, docs map[string]string) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	root := t.TempDir()
	config := "workspace:\n  catalog_dirs:\n    - ./catalog\n"
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(config), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	for rel, body := range docs {
		path := filepath.Join(root, "catalog", filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	rw, err := workspaceconfig.Resolve(root, "")
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	return rw
}

const providesBaseComponent = `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/ai-model/broker-base
  version: 1.0.0
  labels:
    component.aggrega.ai/type: resource
    component.aggrega.ai/category: ai-model
spec:
  schema:
    values:
      model: { type: string }
      provider_name: { type: string }
    infra:
      endpoint: { type: string }
    provides:
      endpoint: { type: string, required: true }
      model: { type: string, required: true }
`

// providesExtendingComponent authors no pillar content of its own: it writes the
// inheritance marker and takes its contract from the base. Reading its file
// alone would find no provides pillar at all.
const providesExtendingComponent = `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/ai-model/broker
  version: 1.0.0
  labels:
    component.aggrega.ai/type: resource
    component.aggrega.ai/category: ai-model
spec:
  extends: core/ai-model/broker-base:1.0.0
  schema: inherited
`

func providesRecipe(keys map[string]string) string {
	var b strings.Builder
	b.WriteString(`apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: broker-gateway
  version: 1.0.0
spec:
  capability: helm
  implements: core/ai-model/broker:1.0.0
  provides:
`)
	// Written in a fixed order so the document bytes are the same on every run.
	for _, k := range sortedStrings(keys) {
		b.WriteString("    " + k + ": \"" + keys[k] + "\"\n")
	}
	b.WriteString(`  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    form: reference
    endpoint: "{{ .infra.endpoint }}"
`)
	return b.String()
}

func sortedStrings(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// TestProvidesDeclaredByComponentThroughTheLintCommand runs the rule the way a
// user reaches it: `aggrega lint` over a real workspace whose catalog holds both
// entities, with the real guest.
//
// It runs the same three catalog shapes twice, once per plugin-resolution mode,
// because the two modes reach the guest by different routes and the payload has
// to be assembled on both. The override mode is the one worth stating: the
// catalog is not consulted for the plugin there, and the field would be absent
// if it were assembled only where the plugin lookup happens.
//
// The Component under test writes the inheritance marker and takes its contract
// from a base. That is deliberate: its own file declares no provides pillar, so
// a payload assembled from the file bytes rather than from the resolved entity
// would report every published key as undeclared, and the conforming case below
// would fail.
func TestProvidesDeclaredByComponentThroughTheLintCommand(t *testing.T) {
	overridePath := wasmtest.EnsureFixtureBuilt(t, "publishing-linter.wasm")

	cases := []struct {
		name       string
		provides   map[string]string
		wantKeys   []string
		wantSilent bool
	}{
		{
			name: "a key beyond the inherited contract is reported",
			provides: map[string]string{
				"endpoint": "{{ .infra.endpoint }}",
				"model":    "{{ .values.model }}",
				"provider": "{{ .values.provider_name }}",
			},
			wantKeys: []string{"provider"},
		},
		{
			name: "the inherited contract exactly is silent",
			provides: map[string]string{
				"endpoint": "{{ .infra.endpoint }}",
				"model":    "{{ .values.model }}",
			},
			wantSilent: true,
		},
		{
			name: "two keys beyond the contract are reported one each",
			provides: map[string]string{
				"endpoint": "{{ .infra.endpoint }}",
				"provider": "{{ .values.provider_name }}",
				"zone":     "eu",
			},
			wantKeys: []string{"provider", "zone"},
		},
	}

	for _, mode := range []struct {
		name       string
		pluginPath string
	}{
		{"plugin override", overridePath},
		{"embedded default", ""},
	} {
		for _, tc := range cases {
			t.Run(mode.name+"/"+tc.name, func(t *testing.T) {
				recipeRel := "recipe/helm/broker-gateway/1.0.0/recipe.yaml"
				rw := seedProvidesWorkspace(t, map[string]string{
					"component/core/ai-model/broker-base/1.0.0/component.yaml": providesBaseComponent,
					"component/core/ai-model/broker/1.0.0/component.yaml":      providesExtendingComponent,
					recipeRel: providesRecipe(tc.provides),
				})
				var diag bytes.Buffer
				cfg := Config{
					EntityPath:     filepath.Join(rw.Root(), "catalog", filepath.FromSlash(recipeRel)),
					PluginPath:     mode.pluginPath,
					PolicyResolver: func(_ string, ws bool) bool { return ws },
					Workspace:      rw,
					DiagOut:        &diag,
					Out:            &bytes.Buffer{},
				}
				if err := RunEntity(t.Context(), cfg); err != nil {
					t.Fatalf("default-mode lint must not halt on an advisory: %v", err)
				}
				reported := reportedProvidesKeys(diag.String())
				if tc.wantSilent {
					if len(reported) != 0 {
						t.Errorf("expected no finding; got %v\n%s", reported, diag.String())
					}
					return
				}
				if strings.Join(reported, ",") != strings.Join(tc.wantKeys, ",") {
					t.Errorf("reported %v, want %v\n%s", reported, tc.wantKeys, diag.String())
				}
				if !strings.Contains(diag.String(), "core/ai-model/broker") {
					t.Errorf("the finding names the Component it read:\n%s", diag.String())
				}
				// The advisory row opens with its severity and carries the
				// condition's short code beside the rule identifier. The code is
				// read off the condition rather than written here, so a
				// reallocation cannot leave this asserting a number nothing
				// prints; the severity token is asserted with it, because a row
				// that lost it would read as a halt.
				wantRow := abi.SeverityWarn + ": " +
					errProvidesDeclaredByComponent.ErrorShortCode() + " " +
					ruleID(errProvidesDeclaredByComponent)
				if !strings.Contains(diag.String(), wantRow) {
					t.Errorf("the finding is advisory and carries its rule id and code (%q):\n%s",
						wantRow, diag.String())
				}
				if strings.Contains(diag.String(), "[") {
					t.Errorf("an advisory row must not wear the bracketed head a halting diagnostic owns:\n%s",
						diag.String())
				}
			})
		}
	}
}

// reportedProvidesKeys extracts, in report order, the published key each
// provides-declared-by-component finding names.
func reportedProvidesKeys(report string) []string {
	const opener = "publishes the provides key \""
	var out []string
	for _, line := range strings.Split(report, "\n") {
		if !strings.Contains(line, "provides-declared-by-component") {
			continue
		}
		at := strings.Index(line, opener)
		if at < 0 {
			continue
		}
		rest := line[at+len(opener):]
		if end := strings.IndexByte(rest, '"'); end >= 0 {
			out = append(out, rest[:end])
		}
	}
	return out
}

// TestProvidesDeclaredByComponentIsSilentWithoutTheComponent pins the case a
// user hits first: linting a Recipe outside the workspace that holds its
// Component. The payload field is absent, and the rule must report nothing
// rather than report the whole published bag as undeclared.
func TestProvidesDeclaredByComponentIsSilentWithoutTheComponent(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "publishing-linter.wasm")
	recipeRel := "recipe/helm/broker-gateway/1.0.0/recipe.yaml"
	rw := seedProvidesWorkspace(t, map[string]string{
		recipeRel: providesRecipe(map[string]string{
			"endpoint": "{{ .infra.endpoint }}",
			"provider": "{{ .values.provider_name }}",
		}),
	})
	var diag bytes.Buffer
	cfg := Config{
		EntityPath:     filepath.Join(rw.Root(), "catalog", filepath.FromSlash(recipeRel)),
		PluginPath:     pluginPath,
		PolicyResolver: func(_ string, ws bool) bool { return ws },
		Workspace:      rw,
		DiagOut:        &diag,
		Out:            &bytes.Buffer{},
	}
	if err := RunEntity(t.Context(), cfg); err != nil {
		t.Fatalf("lint must not halt: %v", err)
	}
	if keys := reportedProvidesKeys(diag.String()); len(keys) != 0 {
		t.Errorf("an unresolvable Component must produce no finding; got %v\n%s", keys, diag.String())
	}
}

// TestProvidesDeclaredByComponentRespectsTheSuppressionFlag drives the flag
// through the whole path — workspace config, policy assembly, the wire payload,
// the guest — beside the same workspace with the flag at its default, so the
// silence is attributable to the flag.
func TestProvidesDeclaredByComponentRespectsTheSuppressionFlag(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "publishing-linter.wasm")
	recipeRel := "recipe/helm/broker-gateway/1.0.0/recipe.yaml"
	docs := map[string]string{
		"component/core/ai-model/broker-base/1.0.0/component.yaml": providesBaseComponent,
		"component/core/ai-model/broker/1.0.0/component.yaml":      providesExtendingComponent,
		recipeRel: providesRecipe(map[string]string{
			"endpoint": "{{ .infra.endpoint }}",
			"model":    "{{ .values.model }}",
			"provider": "{{ .values.provider_name }}",
		}),
	}

	run := func(t *testing.T, suppress bool) []string {
		t.Helper()
		rw := seedProvidesWorkspace(t, docs)
		var diag bytes.Buffer
		cfg := Config{
			EntityPath: filepath.Join(rw.Root(), "catalog", filepath.FromSlash(recipeRel)),
			PluginPath: pluginPath,
			PolicyResolver: func(flagBase string, ws bool) bool {
				if flagBase == "allow-undeclared-recipe-provides" {
					return suppress
				}
				return ws
			},
			Workspace: rw,
			DiagOut:   &diag,
			Out:       &bytes.Buffer{},
		}
		if err := RunEntity(t.Context(), cfg); err != nil {
			t.Fatalf("lint must not halt: %v", err)
		}
		return reportedProvidesKeys(diag.String())
	}

	if got := run(t, false); strings.Join(got, ",") != "provider" {
		t.Fatalf("with the flag at its default the rule fires; got %v", got)
	}
	if got := run(t, true); len(got) != 0 {
		t.Errorf("with the flag set the rule is silent; got %v", got)
	}
}
