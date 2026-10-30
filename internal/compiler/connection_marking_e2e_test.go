// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package compiler_test — end-to-end proof that a connection's own configuration
// is marked against the contract the CONSUMING component declares on the socket,
// and withheld wherever a display shows it: the graph handed to a reader, and the
// per-pass trace.
//
// The fixture's consumer draws two connections to one target. The first is
// written against a socket whose contract declares a values leaf, a defaulted
// values leaf and an infra leaf sensitive, each beside a readable sibling. The
// second is written against a socket whose contract declares a leaf of the SAME
// name readable, and the target's own schema declares a leaf of that name
// sensitive on its own values. Every withholding asserted here is therefore
// attributable to the consumer's socket and to nothing else: a rule reading the
// target's declarations, or reading a leaf's name, withholds the control too.
package compiler_test

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

const (
	connectionMarkingArtifact = "dist/cluster/compose/docker-compose.yaml"

	// The literals the marked connection carries at its declared leaves. None may
	// reach a display; every one must reach the rendered artifact.
	connTokenPlaintext   = "conn-token-plaintext"
	connDefaultPlaintext = "conn-default-plaintext"
	connInfraPlaintext   = "conn-infra-plaintext"

	// The literal the control connection carries at a leaf of the same name its
	// socket declares readable. It must survive on every surface.
	controlTokenPlaintext = "control-token-plaintext"
)

// connectionMarkedLiterals are the values that must not reach a display.
var connectionMarkedLiterals = []string{connTokenPlaintext, connDefaultPlaintext, connInfraPlaintext}

// markedConnectionUsesSlots are the consumer's trace slots that project .context.uses,
// enumerated rather than sampled so a slot withholding at one pass and not another
// cannot pass.
var markedConnectionUsesSlots = []string{
	"04-inject-mapping.yaml",
	"06-source-locals.yaml",
	"08-workload-synthesis.yaml",
	"09b-feature-rendering-boundary.yaml",
	"10-engine-synthesis/app.compose-fragment.yaml",
}

func connectionMarkingBuild(t *testing.T, unredacted bool) *pipeline.Result {
	t.Helper()
	return pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:     "testdata/connection-marking/release.yaml",
		CatalogPaths:    []string{"testdata/connection-marking/catalog/base"},
		IncludeTrace:    true,
		TraceUnredacted: unredacted,
	})
}

// connectionEdge returns one of the consumer's edges by alias.
func connectionEdge(t *testing.T, g *types.IRGraph, alias string) types.IREdge {
	t.Helper()
	for _, e := range g.Spec.Edges {
		if e.Source == "app" && e.Alias == alias {
			return e
		}
	}
	t.Fatalf("the graph carries no edge from app with alias %q", alias)
	return types.IREdge{}
}

// TestConnectionMarking_EdgeCarriesTheConsumerSocketsMarking is the marking
// itself. The marked connection names exactly its three declared leaves — the
// written one, the one standing at a declared default the author never wrote, and
// the infra one the Release wrote — values before infra; the control connection
// names nothing, although its target declares a leaf of the same name sensitive.
func TestConnectionMarking_EdgeCarriesTheConsumerSocketsMarking(t *testing.T) {
	t.Parallel()
	result := connectionMarkingBuild(t, false)

	marked := connectionEdge(t, result.IRGraph, "main")
	assertMarkedExactly(t, "edge app/main", marked.SensitivePayloadLeaves, []string{
		"values.token", "values.seed", "infra.api_key",
	})
	firstInfra, lastValues := -1, -1
	for i, c := range marked.SensitivePayloadLeaves {
		if strings.HasPrefix(c, "infra.") && firstInfra < 0 {
			firstInfra = i
		}
		if strings.HasPrefix(c, "values.") {
			lastValues = i
		}
	}
	if firstInfra >= 0 && lastValues > firstInfra {
		t.Errorf("edge app/main marking %v lists an infra leaf before a values leaf; the payloads' order puts values first",
			marked.SensitivePayloadLeaves)
	}

	control := connectionEdge(t, result.IRGraph, "control")
	if control.SensitivePayloadLeaves != nil {
		t.Errorf("edge app/control marking = %#v, want nil: its socket declares token readable, and the target's own "+
			"declaration governs the target's data, never a consumer's configuration", control.SensitivePayloadLeaves)
	}

	// The graph the build renders from still carries every value: the marking is a
	// statement beside the data, never a substitution in it.
	raw, err := json.Marshal(result.IRGraph)
	if err != nil {
		t.Fatalf("marshal the graph: %v", err)
	}
	for _, literal := range append(append([]string{}, connectionMarkedLiterals...), controlTokenPlaintext) {
		if !strings.Contains(string(raw), literal) {
			t.Errorf("the compiled graph no longer carries %q; the withholding reached the graph the render reads", literal)
		}
	}
}

// TestConnectionMarking_GraphSurfaceWithholds is the graph a reader is handed —
// the command line's graph output, the daemon's answer and the governance
// socket's payload all take it from the one withholding asserted here. Every
// marked literal is gone from both positions the configuration sits at, the edge
// and the node's authored entry; every readable leaf survives at both.
func TestConnectionMarking_GraphSurfaceWithholds(t *testing.T) {
	t.Parallel()
	result := connectionMarkingBuild(t, false)

	out, err := json.Marshal(redact.Graph(result.IRGraph))
	if err != nil {
		t.Fatalf("marshal the withheld graph: %v", err)
	}
	body := string(out)
	for _, literal := range connectionMarkedLiterals {
		if strings.Contains(body, literal) {
			t.Errorf("the withheld graph still carries the declared-sensitive connection value %q", literal)
		}
	}
	for _, want := range []string{controlTokenPlaintext, "store.internal"} {
		if !strings.Contains(body, want) {
			t.Errorf("the withheld graph lost the readable connection value %q", want)
		}
	}

	var decoded struct {
		Spec struct {
			Nodes map[string]struct {
				Uses map[string]map[string]struct {
					Values map[string]any `json:"values"`
				} `json:"uses"`
			} `json:"nodes"`
			Edges []struct {
				Alias  string         `json:"alias"`
				Values map[string]any `json:"values"`
				Infra  map[string]any `json:"infra"`
			} `json:"edges"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("decode the withheld graph: %v", err)
	}
	marker := redact.WithheldMarkerKey()
	isMarker := func(v any) bool {
		m, ok := v.(map[string]any)
		return ok && len(m) == 1 && m[marker] == string(redact.WithheldDeclared)
	}
	for _, e := range decoded.Spec.Edges {
		if e.Alias != "main" {
			continue
		}
		for _, row := range []struct {
			what string
			v    any
		}{
			{"edge values.token", e.Values["token"]},
			{"edge values.seed", e.Values["seed"]},
			{"edge infra.api_key", e.Infra["api_key"]},
		} {
			if !isMarker(row.v) {
				t.Errorf("%s = %#v, want the withheld marker", row.what, row.v)
			}
		}
	}
	entry := decoded.Spec.Nodes["app"].Uses["databases"]["main"]
	if !isMarker(entry.Values["token"]) {
		t.Errorf("node app uses databases/main values.token = %#v, want the withheld marker", entry.Values["token"])
	}
	if entry.Values["endpoint"] != "store.internal" {
		t.Errorf("node app uses databases/main values.endpoint = %#v, want the authored value", entry.Values["endpoint"])
	}
}

// markedConnectionSlot returns one trace entry of the consumer, failing when absent.
func markedConnectionSlot(t *testing.T, result *pipeline.Result, slot string) string {
	t.Helper()
	want := "boundaries/cluster/app/" + slot
	body, ok := result.Trace[want]
	if !ok {
		paths := make([]string, 0, len(result.Trace))
		for p := range result.Trace {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		t.Fatalf("trace slot %q absent; the fixture's trace layout drifted. present: %v", want, paths)
	}
	return string(body)
}

// TestConnectionMarking_TraceWithholds is the per-pass trace. No trace file the
// build writes carries a marked literal — the uses map each slot projects, and the
// rendered workload the later slots show as a re-render — while the control
// connection's same-named leaf survives verbatim.
func TestConnectionMarking_TraceWithholds(t *testing.T) {
	t.Parallel()
	result := connectionMarkingBuild(t, false)

	if len(result.Trace) == 0 {
		t.Fatal("the build collected no trace; every absence below would be vacuous")
	}
	for path, body := range result.Trace {
		for _, literal := range connectionMarkedLiterals {
			if strings.Contains(string(body), literal) {
				t.Errorf("trace %s carries the declared-sensitive connection value %q", path, literal)
			}
		}
	}
	for _, slot := range markedConnectionUsesSlots {
		body := markedConnectionSlot(t, result, slot)
		if !strings.Contains(body, "token: "+redact.Placeholder()) {
			t.Errorf("%s must keep the withheld connection leaf's key\n--- slot ---\n%s", slot, body)
		}
		if !strings.Contains(body, "api_key: "+redact.Placeholder()) {
			t.Errorf("%s must keep the withheld connection infra leaf's key\n--- slot ---\n%s", slot, body)
		}
		for _, want := range []string{"endpoint: store.internal", "token: " + controlTokenPlaintext} {
			if !strings.Contains(body, want) {
				t.Errorf("%s must keep %q verbatim\n--- slot ---\n%s", slot, want, body)
			}
		}
	}
}

// TestConnectionMarking_TraceUnredactedRevealsAndArtifactCarriesPlaintext is the
// boundary's other half. The capture mode that turns the trace's withholding off
// shows every value as compiled, and the rendered artifact — the deployable —
// carries every value in the clear and no replacement text, whichever mode ran.
func TestConnectionMarking_TraceUnredactedRevealsAndArtifactCarriesPlaintext(t *testing.T) {
	t.Parallel()
	result := connectionMarkingBuild(t, true)

	body := markedConnectionSlot(t, result, "04-inject-mapping.yaml")
	for _, literal := range []string{connTokenPlaintext, connInfraPlaintext} {
		if !strings.Contains(body, literal) {
			t.Errorf("an unredacted capture must show %q as compiled\n--- slot ---\n%s", literal, body)
		}
	}

	for _, mode := range []bool{false, true} {
		r := result
		if !mode {
			r = connectionMarkingBuild(t, false)
		}
		artifact, ok := r.Artifacts[connectionMarkingArtifact]
		if !ok {
			paths := make([]string, 0, len(r.Artifacts))
			for p := range r.Artifacts {
				paths = append(paths, p)
			}
			sort.Strings(paths)
			t.Fatalf("expected the merged compose artifact at %q; got %v", connectionMarkingArtifact, paths)
		}
		for _, literal := range append(append([]string{}, connectionMarkedLiterals...), controlTokenPlaintext) {
			if !strings.Contains(string(artifact), literal) {
				t.Errorf("the rendered artifact must carry %q verbatim\n--- artifact ---\n%s", literal, artifact)
			}
		}
		if strings.Contains(string(artifact), redact.Placeholder()) {
			t.Errorf("the rendered artifact must never contain the replacement text\n--- artifact ---\n%s", artifact)
		}
	}
}
