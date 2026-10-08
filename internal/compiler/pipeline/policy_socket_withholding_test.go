// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The workspace these proofs compile. Its gateway carries a value of its own its
// Component declares sensitive, an attachment of its own block and an attachment
// written on one of its connections — each authoring a declared-sensitive value
// beside a readable one — and its Release writes declared-sensitive infra on both
// attachments. Its Recipe renders the node's own value and the credential the
// own-block attachment delivers, so the graph the render reads carries them.
const (
	withholdingDir           = "../../../internal/compiler/testdata/governance-withholding/"
	withholdingAggregatePath = withholdingDir + "aggregate.yaml"
	withholdingReleasePath   = withholdingDir + "release.yaml"
	withholdingManifestPath  = withholdingDir + "manifest.yaml"
	withholdingCatalogPath   = withholdingDir + "catalog"

	// withholdingEngineURI is the Rego policy engine the compiler ships, attached
	// by its bare identity. The policy a test attaches is read by the real engine
	// through the real socket, so what a verdict quotes is what crossed it.
	withholdingEngineURI = "governance/rego"

	// withholdingMarkerKeyToken stands in for the withheld marker's key in the
	// policy text below and is replaced by the key the module exports, so this
	// file never spells the key itself.
	withholdingMarkerKeyToken = "WITHHELD_MARKER_KEY"
)

// withholdingProbePolicy reports every string each pillar the graph carries holds,
// verbatim, so a value that reaches the plugin unwithheld appears in a verdict
// whatever leaf it sits at; every leaf that arrived as the withheld marker, with
// the reason it carries; and the facts a policy reasons on — type, version,
// binding, origin, the markings, a label of the fixture author's own invention,
// the connection, and the node's authored uses copy.
//
// Every rule is a warn, so the build proceeds and each verdict joins the advisory
// stream the assertions read.
const withholdingProbePolicy = `package withholding

import rego.v1

warn contains msg if {
	some node_id, attachments in input.graph.spec.feature_values_mapping
	some name, a in attachments
	some pillar in ["values", "infra"]
	some key, v in object.get(a, pillar, {})
	is_string(v)
	msg := sprintf("attachment %v/%v %v.%v = %v", [node_id, name, pillar, key, v])
}

warn contains msg if {
	some node_id, attachments in input.graph.spec.feature_values_mapping
	some name, a in attachments
	some pillar in ["values", "infra"]
	some key, v in object.get(a, pillar, {})
	is_object(v)
	reason := v["WITHHELD_MARKER_KEY"]
	msg := sprintf("attachment %v/%v %v.%v withheld: %v", [node_id, name, pillar, key, reason])
}

warn contains msg if {
	some pillar, mapping in {"values": "values_mapping", "infra": "infra_mapping"}
	some node_id, payload in object.get(input.graph.spec, mapping, {})
	some key, v in payload
	is_string(v)
	msg := sprintf("node %v %v.%v = %v", [node_id, pillar, key, v])
}

warn contains msg if {
	some pillar, mapping in {"values": "values_mapping", "infra": "infra_mapping"}
	some node_id, payload in object.get(input.graph.spec, mapping, {})
	some key, v in payload
	is_object(v)
	reason := v["WITHHELD_MARKER_KEY"]
	msg := sprintf("node %v %v.%v withheld: %v", [node_id, pillar, key, reason])
}

warn contains msg if {
	some path, attachments in object.get(input.graph.spec, "boundary_features", {})
	some name, a in attachments
	some key, v in object.get(a, "infra", {})
	is_string(v)
	msg := sprintf("boundary attachment %v/%v infra.%v = %v", [path, name, key, v])
}

warn contains msg if {
	some path, attachments in object.get(input.graph.spec, "boundary_features", {})
	some name, a in attachments
	some key, v in object.get(a, "infra", {})
	is_object(v)
	reason := v["WITHHELD_MARKER_KEY"]
	msg := sprintf("boundary attachment %v/%v infra.%v withheld: %v", [path, name, key, reason])
}

warn contains msg if {
	some node_id, attachments in input.graph.spec.feature_values_mapping
	some name, a in attachments
	labels := object.get(a, "labels", {})
	msg := sprintf("attachment %v/%v reads type=%v version=%v binding=%v origin=%v sensitive=%v leaves=%v owner=%v", [node_id, name, a.type, a.resolved_version, object.get(a, "binding", "-"), object.get(a, "origin", "-"), object.get(a, "sensitive", false), concat(",", object.get(a, "sensitive_payload_leaves", [])), object.get(labels, "withholding.example/owner", "-")])
}

warn contains msg if {
	some node_id, attachments in input.graph.spec.feature_values_mapping
	some name, a in attachments
	c := a.connection
	msg := sprintf("attachment %v/%v connection socket=%v alias=%v", [node_id, name, c.socket, c.alias])
}

warn contains msg if {
	some node_id, n in input.graph.spec.nodes
	leaves := object.get(n, "sensitive_payload_leaves", [])
	count(leaves) > 0
	msg := sprintf("node %v marks %v", [node_id, concat(",", leaves)])
}

warn contains msg if {
	some node_id, n in input.graph.spec.nodes
	some socket, entries in object.get(n, "uses", {})
	is_object(entries)
	some alias, entry in entries
	is_object(entry)
	some slot, att in object.get(entry, "features", {})
	msg := sprintf("node %v uses %v/%v carries %v as %v", [node_id, socket, alias, slot, att])
}
`

// withholdingAcceptingPolicy decides nothing that halts. Its one warn verdict is
// there so a run can tell that the policy was dispatched and evaluated at all,
// rather than inferring it from a silence an unattached socket also produces.
const withholdingAcceptingPolicy = `package accepting

import rego.v1

warn contains "the accepting policy evaluated the graph" if {
	count(input.graph.spec.nodes) > 0
}
`

// withholdingPlantedValues are the declared-sensitive values the compiled
// workspace authors. Each one must reach the pipeline's own graph and the
// rendered workload, and none may reach the plugin.
var withholdingPlantedValues = []string{
	"planted-node-own-value",
	"planted-component-credential",
	"planted-connection-credential",
	"planted-release-infra",
	"planted-connection-infra",
}

// withholdingRun is what one dispatch over a graph left behind: the verdicts the
// attached policy returned, with the plugin attribution the socket prefixes them
// with removed, and the graph the run holds after the dispatch, in its two homes
// — the graph the assertions and the render read, and the copy the analysis
// carries — each as the bytes it marshals to.
type withholdingRun struct {
	verdicts    []string
	graph       []byte
	policyGraph []byte
}

// withholdingVerdicts reads the attached policy's warn verdicts off the advisory
// stream, keeping only the ones the policy plugin raised. The prefix is the one
// the socket composes for a verdict carrying no detail, so the attribution is
// read from the formatter rather than spelled a second time here.
func withholdingVerdicts(warnings []abi.Diagnostic) []string {
	prefix := policyWarnMessage(withholdingEngineURI, abi.Diagnostic{})
	var out []string
	for _, w := range warnings {
		if rest, ok := strings.CutPrefix(w.Message, prefix); ok {
			out = append(out, rest)
		}
	}
	slices.Sort(out)
	return out
}

// withholdingAttachment configures the shipped Rego engine at the ir-verdict
// role with policy as its one source, and returns the configuration together
// with a plugin lookup that serves the engine's embedded bytes for its identity
// and hands every other reference to fallback. An empty policy attaches nothing.
func withholdingAttachment(t *testing.T, policy string, fallback pluginpath.Lookup) (*workspaceconfig.Config, pluginpath.Lookup) {
	t.Helper()
	cfg := defaultWorkspaceConfigForTest()
	if policy == "" {
		return cfg, nil
	}
	engine, ok := embedded.Lookup(withholdingEngineURI)
	if !ok {
		t.Fatalf("the Rego policy engine is not embedded in this build (%s); the proof needs the engine the compiler ships", withholdingEngineURI)
	}
	src := filepath.Join(t.TempDir(), "policy.rego")
	body := strings.ReplaceAll(policy, withholdingMarkerKeyToken, redact.WithheldMarkerKey())
	if err := os.WriteFile(src, []byte(body), 0o600); err != nil {
		t.Fatalf("write policy source: %v", err)
	}
	cfg.Plugins.Governance = []workspaceconfig.PluginRef{{
		URI:  withholdingEngineURI,
		Role: plugintype.RoleIRVerdict,
		With: &workspaceconfig.PluginRefWith{Sources: []string{src}},
	}}
	lookup := func(uri string) (pluginpath.ResolvedPlugin, error) {
		if uri == withholdingEngineURI {
			return pluginpath.ResolvedPlugin{Bytes: engine, URI: uri}, nil
		}
		if fallback == nil {
			return pluginpath.ResolvedPlugin{}, fmt.Errorf("no plugin other than the policy engine is expected here, got %q", uri)
		}
		return fallback(uri)
	}
	return cfg, lookup
}

// compileWithholdingWorkspace runs the real pre-render pipeline over the
// workspace above with policy attached at the ir-verdict role (none when policy
// is empty), so the dispatch under proof is the one the pipeline itself makes.
// Everything is read inside the continuation, where the run's products are valid.
func compileWithholdingWorkspace(t *testing.T, policy string) withholdingRun {
	t.Helper()

	rel, err := resolver.LoadRelease(withholdingReleasePath)
	if err != nil {
		t.Fatalf("load release: %v", err)
	}
	manifest, err := resolver.LoadManifest(withholdingManifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	aggregate, err := os.ReadFile(withholdingAggregatePath)
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	cat, err := catalog.LoadAll("", withholdingCatalogPath)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		t.Fatalf("catalog extends: %d error(s); first: %v", len(extErrs), extErrs[0])
	}

	inputs := Inputs{
		Release:           rel,
		Manifest:          manifest,
		AggregateRawBytes: aggregate,
		AggregatePath:     withholdingAggregatePath,
		ReleasePath:       withholdingReleasePath,
		WorkspaceConfig:   defaultWorkspaceConfigForTest(),
		PluginPins:        pluginpath.NoPins(),
		CompilerVersion:   "0.1.0",
	}
	// The production lookup is built before the override is installed, so every
	// reference other than the policy engine resolves exactly as a build's does.
	inputs.WorkspaceConfig, inputs.PluginPathLookup = withholdingAttachment(t, policy, buildEntityPluginLookup(inputs, cat.CatalogLookup()))

	var run withholdingRun
	runErr := runStepsOneToFour(
		context.Background(),
		inputs,
		cat.CatalogLookup(),
		cat.RecipeLookupWithManifest(manifest),
		cat.SubAggregateLoader(),
		func(out *stepsOneToFourOutput) error {
			if len(out.Analysis.Errors) > 0 {
				return fmt.Errorf("the workspace raised %d violation(s); first: %s", len(out.Analysis.Errors), out.Analysis.Errors[0].Message)
			}
			graph, err := json.Marshal(out.IRGraph)
			if err != nil {
				return fmt.Errorf("marshal the pipeline's graph: %w", err)
			}
			policyGraph, err := json.Marshal(&out.Analysis.PolicyInput.Graph)
			if err != nil {
				return fmt.Errorf("marshal the analysis's graph: %w", err)
			}
			run = withholdingRun{verdicts: withholdingVerdicts(out.Analysis.Warnings), graph: graph, policyGraph: policyGraph}
			return nil
		},
	)
	if runErr != nil {
		t.Fatalf("pre-render pipeline: %v", runErr)
	}
	if run.graph == nil {
		t.Fatal("the pipeline completed without invoking the continuation")
	}
	return run
}

// handBuiltWithholdingGraph places a declared-sensitive value at every position
// the graph carries authored pillar data the compiled workspace does not reach:
// an annotation-expanded attachment, a Release-tier attachment carrying infra
// alone, a boundary-level attachment, and the node's own infra. The markings are
// set as the graph build sets them — a pillar-prefixed path per marked leaf, on
// the entry that carries the leaf — so the only thing this construction stands in
// for is which scope wrote each attachment, which the withholding never reads.
func handBuiltWithholdingGraph() *types.IRGraph {
	pillar := func(kv ...string) types.OrderedMap[any] {
		m := types.OrderedMap[any]{}
		for i := 0; i+1 < len(kv); i += 2 {
			m.Set(kv[i], kv[i+1])
		}
		return m
	}
	owner := func() types.OrderedMap[string] {
		m := types.OrderedMap[string]{}
		m.Set("withholding.example/owner", "platform")
		return m
	}

	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("app", types.IRNode{
		Component:              "hand/app",
		ResolvedVersion:        "1.0.0",
		SensitivePayloadLeaves: []string{"values.password", "infra.passphrase"},
	})
	values := types.OrderedMap[json.RawMessage]{}
	values.Set("app", json.RawMessage(`{"name":"demo","password":"planted-node-value"}`))
	infra := types.OrderedMap[json.RawMessage]{}
	infra.Set("app", json.RawMessage(`{"size":"small","passphrase":"planted-node-infra"}`))

	attachments := types.OrderedMap[types.IRFeatureEntry]{}
	attachments.Set("own", types.IRFeatureEntry{
		Type: "feature.hand/token", ResolvedVersion: "1.0.0", Labels: owner(), Binding: "own",
		Origin: types.OriginExplicit, Sensitive: true,
		Values:                 pillar("key", "own-key", "secret", "planted-own-credential"),
		SensitivePayloadLeaves: []string{"values.secret"},
	})
	attachments.Set("audit", types.IRFeatureEntry{
		Type: "feature.hand/audit", ResolvedVersion: "1.0.0", Labels: owner(), Binding: types.AttachmentBindingFree,
		Origin: types.OriginExplicit, Sensitive: true,
		Infra:                  pillar("sink", "siem", "token", "planted-release-infra"),
		SensitivePayloadLeaves: []string{"infra.token"},
	})
	attachments.Set("masking", types.IRFeatureEntry{
		Type: "feature.hand/masking", ResolvedVersion: "1.0.0", Labels: owner(), Binding: types.AttachmentBindingFree,
		Origin: types.OriginAnnotation, AnnotationKey: "masking.example/enabled", Sensitive: true,
		Values:                 pillar("mode", "hash", "salt", "planted-annotation-salt"),
		SensitivePayloadLeaves: []string{"values.salt"},
	})
	attachments.Set("gpt.credential", types.IRFeatureEntry{
		Type: "feature.hand/token", ResolvedVersion: "1.0.0", Labels: owner(), Binding: "credential",
		Origin: types.OriginExplicit, Sensitive: true,
		Connection:             &types.AttachmentConnection{Socket: "models", Alias: "gpt", Attachment: "credential"},
		Values:                 pillar("key", "gpt-key", "secret", "planted-connection-credential"),
		SensitivePayloadLeaves: []string{"values.secret"},
	})
	byNode := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	byNode.Set("app", attachments)

	boundary := types.OrderedMap[types.IRBoundaryFeature]{}
	boundary.Set("guard", types.IRBoundaryFeature{
		Type:                   "feature.hand/guard",
		Infra:                  pillar("note", "boundary-wide", "apiKey", "planted-boundary-key"),
		SensitivePayloadLeaves: []string{"infra.apiKey"},
	})
	byBoundary := types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]]{}
	byBoundary.Set("cluster", boundary)

	// The two lists the graph always carries are present and empty, as the graph
	// build writes them when there is nothing to list. A nil slice would marshal
	// as null, which no build emits and which a plugin decoding the payload's
	// declared shape refuses before any policy runs — so the row would fail on a
	// payload that cannot occur rather than on anything the withholding did.
	g := &types.IRGraph{APIVersion: types.IRAPIVersion, Kind: types.IRKind}
	g.Spec.Edges = []types.IREdge{}
	g.Spec.Provenance = types.IRProvenance{SchemaVersion: types.IRAPIVersion, SourceIdentifiers: []string{}}
	g.Spec.Nodes = nodes
	g.Spec.ValuesMapping = values
	g.Spec.InfraMapping = infra
	g.Spec.FeatureValuesMapping = byNode
	g.Spec.BoundaryFeatures = byBoundary
	return g
}

// dispatchOverHandBuiltGraph runs the policy socket directly over the graph
// above, through the same analysis a build assembles from a graph.
func dispatchOverHandBuiltGraph(t *testing.T, policy string) (withholdingRun, []string) {
	t.Helper()
	g := handBuiltWithholdingGraph()
	analysis := analyzer.Analyze(g)
	cfg, lookup := withholdingAttachment(t, policy, nil)
	inputs := Inputs{WorkspaceConfig: cfg, PluginPathLookup: lookup, PluginPins: pluginpath.NoPins()}
	if err := runPolicySocket(context.Background(), inputs, resolver.CatalogLookup{}, analysis); err != nil {
		t.Fatalf("policy socket: %v", err)
	}
	graph, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("marshal the graph: %v", err)
	}
	policyGraph, err := json.Marshal(&analysis.PolicyInput.Graph)
	if err != nil {
		t.Fatalf("marshal the analysis's graph: %v", err)
	}
	planted := []string{
		"planted-node-value", "planted-node-infra", "planted-own-credential", "planted-release-infra",
		"planted-annotation-salt", "planted-connection-credential", "planted-boundary-key",
	}
	return withholdingRun{verdicts: withholdingVerdicts(analysis.Warnings), graph: graph, policyGraph: policyGraph}, planted
}

// TestPolicySocketWithholdsDeclaredSensitiveLeaves proves a governance plugin
// receives the graph with every declared-sensitive leaf withheld, while every
// fact a policy reasons on still reaches it. The policy is read by the engine the
// compiler ships, and each verdict quotes what the payload carried at a position,
// so the assertions are about what crossed the socket rather than about what was
// assembled before it.
//
// Each row asserts the exact verdict set, so a leaf arriving in the clear shows as
// a line naming its value, a withheld leaf that stopped arriving shows as a
// missing line, and a fact that stopped arriving shows as a changed one. Each also
// asserts that the graph the run holds after the dispatch still carries every
// planted value: the withholding is a copy taken for the plugin, and the graph the
// assertions and the render read is not the plugin's.
func TestPolicySocketWithholdsDeclaredSensitiveLeaves(t *testing.T) {
	t.Parallel()

	for _, row := range []struct {
		name     string
		dispatch func(t *testing.T) (withholdingRun, []string)
		want     []string
	}{
		{
			name: "a compiled build: an own-block and a connection credential, infra the Release writes on both, and the node's own value",
			dispatch: func(t *testing.T) (withholdingRun, []string) {
				return compileWithholdingWorkspace(t, withholdingProbePolicy), withholdingPlantedValues
			},
			want: []string{
				"attachment gateway/db_password infra.rotation_token withheld: declared",
				"attachment gateway/db_password infra.rotation_window = weekly",
				"attachment gateway/db_password reads type=feature.withhold/token version=1.0.0 binding=db_password origin=explicit sensitive=true leaves=values.secret,infra.rotation_token owner=platform",
				"attachment gateway/db_password values.key = db-key",
				"attachment gateway/db_password values.secret withheld: declared",
				"attachment gateway/gpt.credential connection socket=models alias=gpt",
				"attachment gateway/gpt.credential infra.rotation_token withheld: declared",
				"attachment gateway/gpt.credential infra.rotation_window = daily",
				"attachment gateway/gpt.credential reads type=feature.withhold/token version=1.0.0 binding=credential origin=explicit sensitive=true leaves=values.secret,infra.rotation_token owner=platform",
				"attachment gateway/gpt.credential values.key = gpt-key",
				"attachment gateway/gpt.credential values.secret withheld: declared",
				"node gateway marks values.admin_token",
				`node gateway uses models/gpt carries credential as {"type": "feature.withhold/token"}`,
				"node gateway values.admin_token withheld: declared",
				"node gateway values.region = eu",
			},
		},
		{
			name: "every other position the graph carries authored pillar data, built by hand",
			dispatch: func(t *testing.T) (withholdingRun, []string) {
				return dispatchOverHandBuiltGraph(t, withholdingProbePolicy)
			},
			want: []string{
				"attachment app/audit infra.sink = siem",
				"attachment app/audit infra.token withheld: declared",
				"attachment app/audit reads type=feature.hand/audit version=1.0.0 binding=free origin=explicit sensitive=true leaves=infra.token owner=platform",
				"attachment app/gpt.credential connection socket=models alias=gpt",
				"attachment app/gpt.credential reads type=feature.hand/token version=1.0.0 binding=credential origin=explicit sensitive=true leaves=values.secret owner=platform",
				"attachment app/gpt.credential values.key = gpt-key",
				"attachment app/gpt.credential values.secret withheld: declared",
				"attachment app/masking reads type=feature.hand/masking version=1.0.0 binding=free origin=annotation sensitive=true leaves=values.salt owner=platform",
				"attachment app/masking values.mode = hash",
				"attachment app/masking values.salt withheld: declared",
				"attachment app/own reads type=feature.hand/token version=1.0.0 binding=own origin=explicit sensitive=true leaves=values.secret owner=platform",
				"attachment app/own values.key = own-key",
				"attachment app/own values.secret withheld: declared",
				"boundary attachment cluster/guard infra.apiKey withheld: declared",
				"boundary attachment cluster/guard infra.note = boundary-wide",
				"node app infra.passphrase withheld: declared",
				"node app infra.size = small",
				"node app marks values.password,infra.passphrase",
				"node app values.name = demo",
				"node app values.password withheld: declared",
			},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			run, planted := row.dispatch(t)

			for _, value := range planted {
				for _, v := range run.verdicts {
					if strings.Contains(v, value) {
						t.Errorf("the plugin received the declared-sensitive value %q unwithheld; its verdict quoted it:\n  %s", value, v)
					}
				}
				if !strings.Contains(string(run.graph), value) {
					t.Errorf("the graph the run holds after the dispatch no longer carries %q; the withholding reached the graph the assertions and the render read", value)
				}
			}

			want := slices.Clone(row.want)
			slices.Sort(want)
			if !slices.Equal(run.verdicts, want) {
				t.Errorf("the plugin's verdicts differ from what the withheld payload carries.\n  missing: %v\n  unexpected: %v",
					setDifference(want, run.verdicts), setDifference(run.verdicts, want))
			}
		})
	}
}

// TestPolicySocketLeavesTheGraphTheBuildReads proves the dispatch leaves the
// pipeline's graph exactly as the build produced it, whatever the attached policy
// reads: after the dispatch, the graph the assertions and the render read, and the
// copy the analysis carries, marshal to the same bytes as the graph a run with no
// governance attachment holds.
//
// The baseline is checked to carry the planted values first, because a comparison
// over a graph carrying none of them would pass for a withholding that rewrote
// the graph in place.
func TestPolicySocketLeavesTheGraphTheBuildReads(t *testing.T) {
	t.Parallel()

	baseline := compileWithholdingWorkspace(t, "")
	for _, value := range withholdingPlantedValues {
		if !strings.Contains(string(baseline.graph), value) {
			t.Fatalf("the baseline graph does not carry %q, so a comparison against it could not see a withholding that reached the graph", value)
		}
	}
	if len(baseline.verdicts) != 0 {
		t.Fatalf("a run with no governance attachment returned policy verdicts: %v", baseline.verdicts)
	}

	for _, row := range []struct {
		name         string
		policy       string
		wantVerdicts int
	}{
		{name: "an accepting policy", policy: withholdingAcceptingPolicy, wantVerdicts: 1},
		{name: "a policy reading every value the payload carries", policy: withholdingProbePolicy, wantVerdicts: 15},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			run := compileWithholdingWorkspace(t, row.policy)
			if len(run.verdicts) != row.wantVerdicts {
				t.Fatalf("the policy returned %d verdict(s), want %d; the dispatch this row is about did not happen as expected: %v",
					len(run.verdicts), row.wantVerdicts, run.verdicts)
			}
			if string(run.graph) != string(baseline.graph) {
				t.Errorf("the graph the assertions and the render read differs from a run with no governance attachment; the dispatch changed it")
			}
			if string(run.policyGraph) != string(baseline.graph) {
				t.Errorf("the graph the analysis carries differs from a run with no governance attachment; the dispatch changed it")
			}
		})
	}
}

// setDifference returns the members of a absent from b, in a's order.
func setDifference(a, b []string) []string {
	var out []string
	for _, s := range a {
		if !slices.Contains(b, s) {
			out = append(out, s)
		}
	}
	return out
}
