// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lintops

// The input a rule reading a whole Release is handed, observed where it leaves
// for a guest. These tests drive the real release-scoped pass — the shared closure
// resolution over a real workspace, an extending Release folded onto its base —
// with a rule of their own and a dispatch that records each input exactly as the
// linter dispatcher encodes it (json.Marshal of the input, which is the byte
// payload the dispatcher hands the guest), so what they assert is the payload
// rather than an intermediate structure.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// probeRule is the rule these tests dispatch. It is no rule the guest
// implements, which is the point: the input is observed before any guest reads
// it, so the tests do not depend on what a rule makes of it.
const probeRule = "release-rule-input-probe"

// The planted parameter values. One is written in the extending Release and one
// in the base it extends, so the fold carries both into the Release the pass
// resolves, and an input that kept the parameters block would carry both.
const (
	childParameterMarker = "CHILD-PARAMETER-MARKER-5c1e"
	baseParameterMarker  = "BASE-PARAMETER-MARKER-9d2b"
)

// probeAggregate declares its two instances in an order that is not sorted
// order, so an order-preserving components map is distinguishable from a sorted
// one. The instance description carries a parameter reference as template text:
// the Aggregate is handed over as its author wrote it, so the reference arrives
// unresolved rather than as the value a parameter fulfils it with.
const probeAggregate = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: probe-app
  version: "1.0.0"
spec:
  parameters:
    api_key:
      type: string
      required: true
    region:
      type: string
  components:
    web:
      type: core/compute/web-ui
    backend:
      type: core/compute/api-server
      description: "serves callers holding {{ .parameters.api_key }}"
`

const probeManifest = `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: probe-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/compute/api-server: helm-api-server-synthesized-with-artifacts:1.0.0
        core/compute/web-ui: helm-web-ui-synthesized-with-artifacts:1.0.0
`

const probeBaseRelease = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: base-release
  environment: test
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  parameters:
    region: ` + baseParameterMarker + `
  boundaries:
    app:
      directory: ./dist/app
      components:
        web: {}
        backend:
          infra:
            port: 8080
`

const probeChildRelease = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: child-release
  environment: test
  version: "1.0.0"
spec:
  extends: ./base-release.yaml
  parameters:
    api_key: ` + childParameterMarker + `
`

// The planted values the Aggregate itself carries: the default its own parameter
// declaration gives, and the value it passes to the module it imports. Both are
// parameter values, and neither may reach a plugin.
const (
	aggregateDefaultMarker = "AGGREGATE-DEFAULT-MARKER-3f8a"
	moduleParameterMarker  = "MODULE-PARAMETER-MARKER-7b40"
)

// probeAggregateWithModule is probeAggregate with a default on one of its
// parameter declarations and a module it imports, passing that module a
// parameter value. The module's components are not among the Aggregate's own,
// so the Component map is the one probeAggregate yields.
const probeAggregateWithModule = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: probe-app
  version: "1.0.0"
spec:
  parameters:
    api_key:
      type: string
      required: true
    region:
      type: string
      default: ` + aggregateDefaultMarker + `
  aggregates:
    edge:
      type: probe-module
      parameters:
        zone: ` + moduleParameterMarker + `
  components:
    web:
      type: core/compute/web-ui
    backend:
      type: core/compute/api-server
      description: "serves callers holding {{ .parameters.api_key }}"
`

// probeModulePath is where the workspace keeps the module probeAggregateWithModule
// imports. A module is imported by its catalog name, so the directory holding it
// is added to the workspace's catalog directories whenever it is seeded.
const probeModulePath = "modules/probe-module.yaml"

// probeModule is the module probeAggregateWithModule imports: one parameter it
// declares and one component.
const probeModule = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: probe-module
  version: "1.0.0"
spec:
  parameters:
    zone:
      type: string
  components:
    gateway:
      type: core/compute/web-ui
`

// probeBaseReleaseWithModule is probeBaseRelease routing the module's component
// into the same boundary.
const probeBaseReleaseWithModule = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: base-release
  environment: test
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  parameters:
    region: ` + baseParameterMarker + `
  boundaries:
    app:
      directory: ./dist/app
      components:
        web: {}
        backend:
          infra:
            port: 8080
      aggregates:
        edge:
          components:
            gateway: {}
`

// seedProbeWorkspace writes the documents above into a workspace reading the
// full-stack fixture catalog, with lintBlock appended to its configuration, and
// returns the resolved workspace and the extending Release's path.
func seedProbeWorkspace(t *testing.T, lintBlock string) (*workspaceconfig.ResolvedWorkspace, string) {
	t.Helper()
	return seedProbeWorkspaceWith(t, lintBlock, nil)
}

// seedProbeWorkspaceWith is seedProbeWorkspace with some documents replaced or
// added: each entry of replaced is written at its path in place of, or beside,
// the documents above, and the module directory joins the catalog directories
// when the module is among them.
func seedProbeWorkspaceWith(t *testing.T, lintBlock string, replaced map[string]string) (*workspaceconfig.ResolvedWorkspace, string) {
	t.Helper()
	catalogDir, err := filepath.Abs(filepath.Join("..", "compiler", "testdata", "fullstack-app", "catalog"))
	if err != nil {
		t.Fatalf("abs catalog: %v", err)
	}
	root := t.TempDir()
	catalogDirs := "workspace:\n  catalog_dirs:\n    - " + catalogDir + "\n"
	if _, ok := replaced[probeModulePath]; ok {
		catalogDirs += "    - ./" + filepath.ToSlash(filepath.Dir(probeModulePath)) + "\n"
	}
	files := map[string]string{
		workspaceconfig.ConfigFilename: catalogDirs + lintBlock,
		"aggregate.yaml":               probeAggregate,
		"manifest.yaml":                probeManifest,
		"base-release.yaml":            probeBaseRelease,
		"release.yaml":                 probeChildRelease,
	}
	for name, body := range replaced {
		files[name] = body
	}
	for name, body := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, filepath.FromSlash(name))), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	return rw, filepath.Join(root, "release.yaml")
}

// recordingDispatch stands in for the linter dispatch. It records every input it
// is handed together with the bytes the dispatcher would hand a guest for it, and
// answers each input with the findings answer returns for it (none, when answer
// is nil).
type recordingDispatch struct {
	inputs   []validator.LinterInput
	payloads [][]byte
	answer   func(validator.LinterInput) []abi.Diagnostic
}

func (r *recordingDispatch) dispatch(_ context.Context, _ []LinterAttachment, inputs []validator.LinterInput) ([][]abi.Diagnostic, error) {
	out := make([][]abi.Diagnostic, 0, len(inputs))
	for _, in := range inputs {
		payload, err := json.Marshal(in)
		if err != nil {
			return nil, err
		}
		r.inputs = append(r.inputs, in)
		r.payloads = append(r.payloads, payload)
		var findings []abi.Diagnostic
		if r.answer != nil {
			findings = r.answer(in)
		}
		out = append(out, findings)
	}
	return out, nil
}

// probeInputs returns the recorded inputs addressed to the probe rule.
func (r *recordingDispatch) probeInputs() []validator.LinterInput {
	var out []validator.LinterInput
	for _, in := range r.inputs {
		if in.RuleID == probeRule {
			out = append(out, in)
		}
	}
	return out
}

// runReleaseProbe runs the release-scoped pass over the seeded workspace with
// the probe rule and a recording dispatch.
func runReleaseProbe(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, releasePath string, resolve func(string, bool) bool, rec *recordingDispatch) (string, string) {
	t.Helper()
	var out, diag bytes.Buffer
	err := runRelease(t.Context(), rw, releasePath, nil, &out, &diag, nil, releaseRun{
		policyResolve: resolve,
		rules:         []string{probeRule},
		dispatch:      rec.dispatch,
	})
	if err != nil {
		t.Fatalf("the release-scoped pass must run over the seeded Release: %v\n%s", err, diag.String())
	}
	return out.String(), diag.String()
}

// orderedKeys reads an object's top-level keys in the order the bytes carry them.
// It reads the token stream, because decoding into a map would discard exactly the
// order under test.
func orderedKeys(t *testing.T, raw []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("expected an object; got %v (%v): %s", tok, err, raw)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("read key: %v", err)
		}
		keys = append(keys, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatalf("skip value: %v", err)
		}
	}
	return keys
}

// TestReleaseRunHandsAReleaseScopedRuleTheWholeRelease pins the three documents
// a rule reading a whole Release receives, and that it receives them once.
func TestReleaseRunHandsAReleaseScopedRuleTheWholeRelease(t *testing.T) {
	rw, releasePath := seedProbeWorkspace(t, "")
	rec := &recordingDispatch{}
	runReleaseProbe(t, rw, releasePath, nil, rec)

	got := rec.probeInputs()
	if len(got) != 1 {
		t.Fatalf("a rule reading a whole Release is dispatched once per run; got %d inputs", len(got))
	}
	in := got[0]
	if in.EntityKind != "Aggregate" {
		t.Errorf("the input names the Aggregate as the document proposed carries; got entity_kind %q", in.EntityKind)
	}
	if len(in.Previous) != 0 || len(in.ImplementedComponent) != 0 || in.SourcePath != "" {
		t.Errorf("a release-scoped input carries none of the per-entity fields; got previous=%s implemented_component=%s source_path=%q",
			in.Previous, in.ImplementedComponent, in.SourcePath)
	}

	// proposed is the Aggregate as authored: the template text is intact.
	var proposed struct {
		Kind string `json:"kind"`
		Spec struct {
			Components map[string]struct {
				Description string `json:"description"`
			} `json:"components"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(in.Proposed, &proposed); err != nil {
		t.Fatalf("proposed must be JSON: %v", err)
	}
	if proposed.Kind != "Aggregate" {
		t.Errorf("proposed carries the Aggregate; got kind %q", proposed.Kind)
	}
	if desc := proposed.Spec.Components["backend"].Description; desc != "serves callers holding {{ .parameters.api_key }}" {
		t.Errorf("proposed carries the Aggregate as its author wrote it, template text intact; got %q", desc)
	}

	// release is the extending Release folded onto its base: its own metadata, and
	// the boundary tree only the base declares.
	var release struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Spec map[string]json.RawMessage `json:"spec"`
	}
	if err := json.Unmarshal(in.Release, &release); err != nil {
		t.Fatalf("release must be JSON: %v", err)
	}
	if release.Metadata.Name != "child-release" {
		t.Errorf("release is the Release the run names; got metadata.name %q", release.Metadata.Name)
	}
	if _, ok := release.Spec["boundaries"]; !ok {
		t.Errorf("release carries what the extended base declares, so the extends chain was folded; got spec keys %v", keysOf(release.Spec))
	} else if !strings.Contains(string(release.Spec["boundaries"]), `"port":8080`) {
		t.Errorf("the inherited boundary carries the base's per-component infra; got %s", release.Spec["boundaries"])
	}
	if _, ok := release.Spec["parameters"]; ok {
		t.Errorf("release carries no parameters block; got %s", release.Spec["parameters"])
	}

	// components is keyed by instance name, in the Aggregate's order, each value the
	// Component that instance's type resolves to.
	if in.Components.Len() == 0 {
		t.Fatal("the input carries the Components the Aggregate's instances use; got none")
	}
	payload, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	var wire struct {
		Components json.RawMessage `json:"components"`
	}
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if keys := orderedKeys(t, wire.Components); strings.Join(keys, ",") != "web,backend" {
		t.Errorf("components is keyed by instance name in the Aggregate's order; got %v", keys)
	}
	for instance, wantName := range map[string]string{
		"web":     "core/compute/web-ui",
		"backend": "core/compute/api-server",
	} {
		raw, ok := in.Components.Get(instance)
		if !ok {
			t.Errorf("components lacks instance %q", instance)
			continue
		}
		var comp struct {
			Kind     string `json:"kind"`
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal(raw, &comp); err != nil {
			t.Fatalf("components[%s] must be JSON: %v", instance, err)
		}
		if comp.Kind != "Component" || comp.Metadata.Name != wantName {
			t.Errorf("components[%s] is the Component its type resolves to (%s); got kind %q name %q",
				instance, wantName, comp.Kind, comp.Metadata.Name)
		}
	}

	// The per-entity inputs of the same run carry none of the Release documents.
	entityInputs := 0
	for _, other := range rec.inputs {
		if other.RuleID == probeRule {
			continue
		}
		entityInputs++
		if len(other.Release) != 0 || !other.Components.IsZero() {
			t.Errorf("per-entity rule %q received the release-scoped fields", other.RuleID)
		}
	}
	if entityInputs == 0 {
		t.Fatal("the run dispatched no per-entity input, so the partition above was never exercised")
	}
}

// TestReleaseRunWithholdsEveryReleaseParameterValue plants a parameter value in
// each of the four places a Release-scoped run reads one from — the parameters
// block of the extending Release and of the base it extends, the default the
// Aggregate's own parameter declaration gives, and the value the Aggregate passes
// to a module it imports — runs the pass, and asserts no value appears in any
// payload the run hands a guest.
//
// The control is the fixture itself: every value is on disk in a document the
// pass reads, and the documents a rule reading a whole Release is handed are those
// very documents, so their absence from the payloads is the removal's doing rather
// than the fixture's. The parameter reference in the Aggregate's template text and
// the Aggregate's parameter declarations, without their defaults, are asserted
// present beside them, so the removal is shown taking the values and leaving what
// names or declares a parameter. Each plant that removes one removal turns this
// test red.
func TestReleaseRunWithholdsEveryReleaseParameterValue(t *testing.T) {
	rw, releasePath := seedProbeWorkspaceWith(t, "", map[string]string{
		"aggregate.yaml":    probeAggregateWithModule,
		probeModulePath:     probeModule,
		"base-release.yaml": probeBaseReleaseWithModule,
	})

	planted := map[string]string{
		childParameterMarker:   "release.yaml",
		baseParameterMarker:    "base-release.yaml",
		aggregateDefaultMarker: "aggregate.yaml",
		moduleParameterMarker:  "aggregate.yaml",
	}
	for marker, file := range planted {
		body, err := os.ReadFile(filepath.Join(filepath.Dir(releasePath), file))
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if !bytes.Contains(body, []byte(marker)) {
			t.Fatalf("the planted value %q is not in %s, so its absence below would prove nothing", marker, file)
		}
	}

	rec := &recordingDispatch{}
	runReleaseProbe(t, rw, releasePath, nil, rec)
	probe := rec.probeInputs()
	if len(probe) != 1 {
		t.Fatalf("expected one release-scoped input; got %d", len(probe))
	}
	for i, payload := range rec.payloads {
		for marker := range planted {
			if bytes.Contains(payload, []byte(marker)) {
				t.Errorf("payload %d (rule %q) carries the parameter value %q; no parameter value may reach a linter plugin:\n%s",
					i, rec.inputs[i].RuleID, marker, payload)
			}
		}
	}

	var proposed struct {
		Spec struct {
			Parameters json.RawMessage                       `json:"parameters"`
			Aggregates map[string]map[string]json.RawMessage `json:"aggregates"`
			Components map[string]struct {
				Description string `json:"description"`
			} `json:"components"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(probe[0].Proposed, &proposed); err != nil {
		t.Fatalf("proposed must be JSON: %v", err)
	}
	if got, want := string(proposed.Spec.Parameters), `{"api_key":{"required":true,"type":"string"},"region":{"type":"string"}}`; got != want {
		t.Errorf("proposed keeps each parameter declaration and drops its default; got %s, want %s", got, want)
	}
	module, ok := proposed.Spec.Aggregates["edge"]
	if !ok {
		t.Fatalf("proposed lost the imported module entry itself; only its parameters block is removed: %s", probe[0].Proposed)
	}
	if _, ok := module["parameters"]; ok {
		t.Errorf("proposed carries the parameters passed to the imported module: %s", module["parameters"])
	}
	if _, ok := module["type"]; !ok {
		t.Errorf("the imported module entry lost its type; only its parameters block is removed: %v", module)
	}
	if desc := proposed.Spec.Components["backend"].Description; desc != "serves callers holding {{ .parameters.api_key }}" {
		t.Errorf("template text naming a parameter is kept as written; got %q", desc)
	}
}

// TestReleaseRunCarriesTheCredentialMismatchSuppression pins the suppression's
// route into the payload a release-scoped rule receives: from the workspace
// configuration, and from each of the two command-line overrides, which beat it
// in either direction.
func TestReleaseRunCarriesTheCredentialMismatchSuppression(t *testing.T) {
	const flagBase = "allow-connection-credential-mismatches"
	forcing := func(v bool) func(string, bool) bool {
		return func(flag string, ws bool) bool {
			if flag == flagBase {
				return v
			}
			return ws
		}
	}
	cases := []struct {
		name      string
		workspace bool
		resolve   func(string, bool) bool
		want      bool
	}{
		{"the default leaves the rule on", false, nil, false},
		{"the workspace configuration suppresses it", true, nil, true},
		{"the allow flag suppresses it over the workspace", false, forcing(true), true},
		{"the no-allow flag restores it over the workspace", true, forcing(false), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lintBlock := ""
			if tc.workspace {
				lintBlock = "lint:\n  publishing_discipline:\n    allow_connection_credential_mismatches: true\n"
			}
			rw, releasePath := seedProbeWorkspace(t, lintBlock)
			rec := &recordingDispatch{}
			runReleaseProbe(t, rw, releasePath, tc.resolve, rec)
			got := rec.probeInputs()
			if len(got) != 1 {
				t.Fatalf("expected one release-scoped input; got %d", len(got))
			}
			var policies map[string]bool
			if err := json.Unmarshal(got[0].Policies, &policies); err != nil {
				t.Fatalf("policies must be a JSON object of booleans: %v (%s)", err, got[0].Policies)
			}
			v, ok := policies["allow_connection_credential_mismatches"]
			if !ok {
				t.Fatalf("the payload's policies do not carry the suppression: %s", got[0].Policies)
			}
			if v != tc.want {
				t.Errorf("allow_connection_credential_mismatches = %v; want %v (policies %s)", v, tc.want, got[0].Policies)
			}
		})
	}
}

// TestReleaseRunWithNoReleaseScopedRuleHandsNoGuestTheRelease is the state the
// shipped rule set is in until a rule reading a whole Release is registered: the
// pass dispatches its per-entity rules alone, and no input carries a Release or a
// Component map.
func TestReleaseRunWithNoReleaseScopedRuleHandsNoGuestTheRelease(t *testing.T) {
	rw, releasePath := seedProbeWorkspace(t, "")
	rec := &recordingDispatch{}
	var diag bytes.Buffer
	if err := runRelease(t.Context(), rw, releasePath, nil, io.Discard, &diag, nil, releaseRun{
		dispatch: rec.dispatch,
	}); err != nil {
		t.Fatalf("the release-scoped pass must run: %v\n%s", err, diag.String())
	}
	if len(rec.inputs) == 0 {
		t.Fatal("the run dispatched nothing, so the absence below would prove nothing")
	}
	for _, in := range rec.inputs {
		if len(in.Release) != 0 || !in.Components.IsZero() {
			t.Errorf("rule %q received the release-scoped fields with no release-scoped rule registered", in.RuleID)
		}
	}
}

// TestReleaseRunReportsAReleaseScopedFindingAgainstTheRelease pins what happens
// to a finding a release-scoped rule returns: it is advisory whatever severity
// the guest gave it, it is reported under the Release the run names, and it is
// counted in the pass's summary.
func TestReleaseRunReportsAReleaseScopedFindingAgainstTheRelease(t *testing.T) {
	rw, releasePath := seedProbeWorkspace(t, "")
	rec := &recordingDispatch{answer: func(in validator.LinterInput) []abi.Diagnostic {
		if in.RuleID != probeRule {
			return nil
		}
		return []abi.Diagnostic{{RuleID: probeRule, Severity: abi.SeverityHalt, Message: "the two ends differ"}}
	}}
	out, diag := runReleaseProbe(t, rw, releasePath, nil, rec)
	if !strings.Contains(diag, releasePath+":\n") {
		t.Errorf("the finding is reported under the Release:\n%s", diag)
	}
	wantRow := abi.SeverityWarn + ": " + probeRule + ": the two ends differ"
	if !strings.Contains(diag, wantRow) {
		t.Errorf("the finding is advisory (%q):\n%s", wantRow, diag)
	}
	if !strings.Contains(out, "1 advisory finding(s)") {
		t.Errorf("the summary counts the release-scoped finding:\n%s", out)
	}
}

// TestReleaseRuleDocumentsLeaveOutWhatDoesNotResolve pins the Components map's
// edges over a hand-built resolution: an instance whose type does not resolve is
// absent rather than present and empty, a name two subsystems declare keeps its
// first declaration, and the order is the Aggregate's across subsystems.
func TestReleaseRuleDocumentsLeaveOutWhatDoesNotResolve(t *testing.T) {
	t.Parallel()
	const aggregateYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: split-app
  version: "1.0.0"
spec:
  subsystems:
    edge:
      components:
        router:
          type: core/gateway/router
        orphan:
          type: core/compute/nowhere
    data:
      components:
        store:
          type: core/datastore/store
        router:
          type: core/gateway/second-router
`
	var agg types.Aggregate
	if err := yaml.Unmarshal([]byte(aggregateYAML), &agg); err != nil {
		t.Fatalf("parse aggregate: %v", err)
	}
	rel := &types.Release{APIVersion: "aggrega.ai/v1alpha1", Kind: "Release"}
	rel.Metadata.Name = "split-release"
	rc := &compilerops.ResolvedClosure{Release: rel, Aggregate: &agg, AggBytes: []byte(aggregateYAML), AggregatePath: "aggregate.yaml"}

	known := map[string]string{
		"core/gateway/router":        "core/gateway/router",
		"core/gateway/second-router": "core/gateway/second-router",
		"core/datastore/store":       "core/datastore/store",
	}
	lookup := func(uri string) (types.Component, bool) {
		name, ok := known[uri]
		if !ok {
			return types.Component{}, false
		}
		var c types.Component
		c.APIVersion, c.Kind = "aggrega.ai/v1alpha1", "Component"
		c.Metadata.Name = name
		return c, true
	}

	docs, err := releaseRuleDocumentsFor(rc, lookup)
	if err != nil {
		t.Fatalf("releaseRuleDocumentsFor: %v", err)
	}
	if keys := strings.Join(docs.Components.Keys(), ","); keys != "router,store" {
		t.Errorf("components holds the resolved instances in the Aggregate's order, the unresolved one absent; got %s", keys)
	}
	raw, _ := docs.Components.Get("router")
	if !strings.Contains(string(raw), `"name":"core/gateway/router"`) {
		t.Errorf("a name declared twice keeps its first declaration; got %s", raw)
	}
	if _, ok := docs.Components.Get("orphan"); ok {
		t.Error("an instance whose type does not resolve must be absent")
	}

	if _, err := releaseRuleDocumentsFor(nil, lookup); err == nil {
		t.Error("no resolved Release must be refused rather than handed over empty")
	}
}

// TestReleaseScopedRulesAreDispatchedOnlyOverTheRelease pins the partition over
// a rule set holding both scopes, whatever the shipped set holds today: a rule
// marked release-scoped is in no entity list, in either mode, and is the whole of
// the release-scoped list.
func TestReleaseScopedRulesAreDispatchedOnlyOverTheRelease(t *testing.T) {
	t.Parallel()
	rules := []publishingRule{
		{condition: errSchemaEvolution, prePublish: true},
		{condition: errNameShape},
		{condition: errAnnotationPrefix, releaseScoped: true},
	}
	scoped := ruleID(errAnnotationPrefix)
	for _, prePublish := range []bool{false, true} {
		for _, id := range entityRuleIDsOf(rules, prePublish) {
			if id == scoped {
				t.Errorf("a release-scoped rule reached the entity list (prePublish=%v)", prePublish)
			}
		}
	}
	if got := strings.Join(entityRuleIDsOf(rules, true), ","); got != ruleID(errSchemaEvolution)+","+ruleID(errNameShape) {
		t.Errorf("the entity list keeps every entity rule in order; got %s", got)
	}
	if got := releaseRuleIDsOf(rules); len(got) != 1 || got[0] != scoped {
		t.Errorf("the release-scoped list is exactly the release-scoped rule; got %v", got)
	}
}

// keysOf lists a map's keys, for a failure message.
func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
