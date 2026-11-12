// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package compiler_test — end-to-end proof of the two withholdings that do not fit
// the key-addressed shape: a value governed by ANOTHER node's declarations, and a
// rendered document with no key to substitute at.
//
// The fixture's consuming node declares NOTHING sensitive. Every withholding
// asserted here is therefore attributable to a declaration that travelled across
// an edge — which is the property that makes the assertions mean something: a
// withholding driven by the displayed node's own set could not produce them.
//
// Both targets publish the SAME two provides key names and only one declares one of
// them sensitive. A rule that fired on a key's spelling would withhold both and
// fail every control below.

package compiler_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
)

const crossNodeArtifactPath = "dist/crossnode/compose/docker-compose.yaml"

const (
	// The literal the DECLARING target publishes under zzTargetSecret. It must not
	// reach the consumer's display.
	markedTargetSecret = "wobble-store-plaintext"
	// The literal the control target publishes under the SAME key name. Nothing
	// declares it, so it must survive verbatim on the same surface.
	plainTargetSecret = "visible-plain-plaintext"
)

// crossNodeUsesSlots are the consumer's trace slots that project .context.uses.
// The uses map is visible from the first pass that carries the topology onward, so
// each of these slots is a place the cross-node values could have printed.
var crossNodeUsesSlots = []string{
	"04-inject-mapping.yaml",
	"06-source-locals.yaml",
	"08-workload-synthesis.yaml",
	"09b-feature-rendering-boundary.yaml",
	"10-engine-synthesis/app.compose-fragment.yaml",
}

// crossNodeWorkloadSlots are the consumer's trace slots that project the rendered
// .workload document. Enumerated rather than sampled: a clause that withheld the
// projection at one pass and not another would leave every other slot passing.
var crossNodeWorkloadSlots = []string{
	"09b-feature-rendering-boundary.yaml",
	"10-engine-synthesis/app.compose-fragment.yaml",
}

func crossNodeBuild(t *testing.T) *pipeline.Result {
	t.Helper()
	return pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  "testdata/sensitive-redaction/uses-release.yaml",
		CatalogPaths: []string{"testdata/sensitive-redaction/catalog/base"},
		IncludeTrace: true,
	})
}

// crossNodeSlot returns one trace entry for the named node of the cross-node
// fixture, failing when it is absent — an assertion against a missing slot would be
// vacuous.
func crossNodeSlot(t *testing.T, result *pipeline.Result, nodeID, slot string) string {
	t.Helper()
	want := "boundaries/crossnode/" + nodeID + "/" + slot
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

// TestSensitiveRedaction_TraceWithholdsConsumedTargetProvides is the cross-node
// assertion. The consuming node declares nothing; what withholds the value is the
// TARGET Component's declaration, reached across the edge.
//
// Four controls sit beside the one positive assertion, and each rules out a
// different way the redaction could be right by accident: the undeclared sibling
// key on the same edge, the identically-named key on the other edge, the edge's own
// fields, and the author order of the provides bag.
func TestSensitiveRedaction_TraceWithholdsConsumedTargetProvides(t *testing.T) {
	t.Parallel()
	result := crossNodeBuild(t)

	for _, slot := range crossNodeUsesSlots {
		body := crossNodeSlot(t, result, "app", slot)

		// Keyed rather than bare: the same value reaches three surfaces in this slot
		// — published under the edge, delivered onto a delivery key, and interpolated
		// into the rendered document — each with its own assertion. Keying this one
		// to the published projection keeps a failure attributable to the surface it
		// belongs to instead of to whichever surface leaked first.
		if strings.Contains(body, "zzTargetSecret: "+markedTargetSecret) {
			t.Errorf("%s leaked the target's declared-sensitive provides value %q\n--- slot ---\n%s", slot, markedTargetSecret, body)
		}
		// Shape survives: the key is still printed under the marked edge.
		if !strings.Contains(body, "zzTargetSecret: "+redact.Placeholder()) {
			t.Errorf("%s must keep the withheld provides key\n--- slot ---\n%s", slot, body)
		}
		// Control 1 — the undeclared sibling on the SAME edge.
		if !strings.Contains(body, "aaTargetHost: store") {
			t.Errorf("%s must keep the marked edge's undeclared sibling verbatim\n--- slot ---\n%s", slot, body)
		}
		// Control 2 — the SAME key name on the edge whose target declares nothing.
		// This is the assertion a name-shaped rule cannot pass.
		if !strings.Contains(body, "zzTargetSecret: "+plainTargetSecret) {
			t.Errorf("%s must keep the identically-named key on the undeclared target's edge\n--- slot ---\n%s", slot, body)
		}
		// Control 3 — the edge's own fields are untouched.
		for _, want := range []string{"component: core/uses-target", "socket: marked", "target: store"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s must keep the edge's own field %q\n--- slot ---\n%s", slot, want, body)
			}
		}
		// Control 4 — author order survives. zzTargetSecret is published first and
		// sorts last, so a sort creeping into the substitution would move it.
		first, second := strings.Index(body, "zzTargetSecret:"), strings.Index(body, "aaTargetHost:")
		if first < 0 || second < 0 || first > second {
			t.Errorf("%s must show the provides bag in author order (zzTargetSecret before aaTargetHost); got indexes %d and %d\n--- slot ---\n%s", slot, first, second, body)
		}
	}
}

// TestSensitiveRedaction_TraceShowsTheWorkloadWithCrossNodeDataSubstituted is the
// document-scale assertion for the cross-node axis. The consuming node declares
// nothing, so every substituted position in its rendered workload is attributable
// to a declaration that travelled across an edge.
//
// It is the sharpest form of the cross-node claim available, because the document
// carries the marked value, its undeclared sibling, and the identically-named key
// from the OTHER target side by side. A withholding driven by the key's spelling
// would blank two of the three.
func TestSensitiveRedaction_TraceShowsTheWorkloadWithCrossNodeDataSubstituted(t *testing.T) {
	t.Parallel()
	result := crossNodeBuild(t)

	for _, slot := range crossNodeWorkloadSlots {
		body := crossNodeSlot(t, result, "app", slot)

		if !strings.Contains(body, redact.SubstitutedProjection()) {
			t.Errorf("%s must announce that its document is a re-render\n--- slot ---\n%s", slot, body)
		}
		if strings.Contains(body, redact.WithheldProjection()) {
			t.Errorf("%s withheld a projection it could have re-rendered\n--- slot ---\n%s", slot, body)
		}

		leaves := workloadLeaves(t, body)
		// The two positions reading the declaring target's marked provides key: once
		// as the published value, once as the value delivered onto this node's own
		// delivery key. Both are governed by the TARGET's declaration.
		for _, path := range []string{
			"containers.main.variables.MARKED_SECRET",
			"containers.main.variables.STORE_SECRET_ENV",
		} {
			if got := leaves[path]; got != redact.Placeholder() {
				t.Errorf("%s: %s must show the replacement text; got %q", slot, path, got)
			}
		}
		// Control 1 — the marked target's undeclared sibling key.
		if got := leaves["containers.main.variables.MARKED_HOST"]; got != "store" {
			t.Errorf("%s: the marked target's undeclared sibling must survive; got %q", slot, got)
		}
		// Control 2 — the SAME provides key name on the target that declares nothing.
		// This is the assertion a name-shaped rule cannot pass.
		if got := leaves["containers.main.variables.PLAIN_SECRET"]; got != plainTargetSecret {
			t.Errorf("%s: the identically-named key on the undeclared target must survive as %q; got %q", slot, plainTargetSecret, got)
		}
		// Control 3 — the consumer's own undeclared values leaf.
		if got := leaves["containers.main.variables.APP_NAME"]; got != "usesdemo" {
			t.Errorf("%s: the consumer's own undeclared leaf must survive; got %q", slot, got)
		}
		for path, text := range leaves {
			if strings.Contains(text, markedTargetSecret) {
				t.Errorf("%s: %s leaked %q", slot, path, markedTargetSecret)
			}
		}
	}
}

// TestSensitiveRedaction_UndeclaredNodesDisplayNothingWithheld is the invisibility
// control, and it is the assertion that keeps the axis from becoming a tax on
// builds that never use it. The fixture's two target nodes consume nothing and, for
// the control target, declare nothing: their displays must carry neither the
// placeholder nor the withholding statement anywhere.
//
// The declaring target is included deliberately. It declares a provides key, but a
// provides declaration governs what its CONSUMERS see, not its own pillars — so its
// own display is unchanged too.
func TestSensitiveRedaction_UndeclaredNodesDisplayNothingWithheld(t *testing.T) {
	t.Parallel()
	result := crossNodeBuild(t)

	checked := 0
	for path, body := range result.Trace {
		if !strings.Contains(path, "/store/") && !strings.Contains(path, "/plain/") {
			continue
		}
		checked++
		if strings.Contains(string(body), redact.Placeholder()) {
			t.Errorf("%s withholds a value on a node that consumes nothing\n--- slot ---\n%s", path, body)
		}
		if strings.Contains(string(body), redact.WithheldProjection()) {
			t.Errorf("%s withholds its workload projection on a node that consumes nothing\n--- slot ---\n%s", path, body)
		}
	}
	if checked == 0 {
		t.Fatal("no target-node slot was examined; this control is vacuous")
	}
	// Each target's own rendered workload is still shown in full, so the control is
	// asserting presence rather than merely absence.
	for _, nodeID := range []string{"store", "plain"} {
		body := crossNodeSlot(t, result, nodeID, "09b-feature-rendering-boundary.yaml")
		if !strings.Contains(body, "containers:") {
			t.Errorf("%s must still show its rendered workload document\n--- slot ---\n%s", nodeID, body)
		}
	}
}

// TestSensitiveRedaction_CrossNodeArtifactCarriesPlaintext is the boundary's other
// half for the cross-node axis: every value the consumer's display withholds
// reaches the deployable in the clear, because the artifact is what gets deployed.
func TestSensitiveRedaction_CrossNodeArtifactCarriesPlaintext(t *testing.T) {
	t.Parallel()
	result := crossNodeBuild(t)

	body, ok := result.Artifacts[crossNodeArtifactPath]
	if !ok {
		paths := make([]string, 0, len(result.Artifacts))
		for p := range result.Artifacts {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		t.Fatalf("expected the merged compose artifact at %q; got %v", crossNodeArtifactPath, paths)
	}
	artifact := string(body)

	for _, want := range []string{markedTargetSecret, plainTargetSecret, "usesdemo"} {
		if !strings.Contains(artifact, want) {
			t.Errorf("the rendered artifact must carry %q verbatim; a placeholder there would be an undeployable manifest\n--- artifact ---\n%s", want, artifact)
		}
	}
	if strings.Contains(artifact, redact.Placeholder()) {
		t.Errorf("the rendered artifact must never contain the redaction placeholder\n--- artifact ---\n%s", artifact)
	}
	if strings.Contains(artifact, redact.WithheldProjection()) {
		t.Errorf("the rendered artifact must never contain the withholding statement\n--- artifact ---\n%s", artifact)
	}
}

// crossNodeInjectSlots are the consumer's trace slots that project the delivery
// pipe. The pipe is populated from the delivery pass onward, and both of the passes
// that carry it before base rendering are enumerated: a marking applied at one and
// not the other would leave the other printing in the clear.
var crossNodeInjectSlots = []string{"03-inject-hydration.yaml", "04-inject-mapping.yaml"}

// TestSensitiveRedaction_TraceWithholdsUsesEdgeDeliveredValue is the delivery-pipe
// half of the cross-node withholding, and it covers the second of the two wirings
// that can route a target's declared-sensitive provides value onto a consumer's
// delivery key.
//
// The policy is identical to an attachment's — a delivery key whose referenced
// provides key is declared sensitive carries that declaration — and the fact source
// is identical too: the target's own declarations, read against the type the edge
// names. What differs is only which authored mapping did the routing.
//
// The `all` pillar is asserted alongside `env` because it is the host-built union of
// the typed mechanisms: the same datum reappears there, and a withholding covering
// only the typed pillar would re-print it a few lines below.
func TestSensitiveRedaction_TraceWithholdsUsesEdgeDeliveredValue(t *testing.T) {
	t.Parallel()
	result := crossNodeBuild(t)

	for _, slot := range crossNodeInjectSlots {
		body := crossNodeSlot(t, result, "app", slot)
		if !strings.Contains(body, "context.inject:") {
			t.Fatalf("%s no longer carries a delivery pipe; the assertions below would be vacuous\n--- slot ---\n%s", slot, body)
		}
		if strings.Contains(body, markedTargetSecret) {
			t.Errorf("%s leaked the value delivered across the marked edge %q\n--- slot ---\n%s", slot, markedTargetSecret, body)
		}
		if n := strings.Count(body, "STORE_SECRET: "+redact.Placeholder()); n != 2 {
			t.Errorf("%s must withhold the edge-delivered key in both the env pillar and the union pillar; got %d occurrence(s)\n--- slot ---\n%s", slot, n, body)
		}
		// The control on the same edge: its other delivery key is filled from an
		// UNDECLARED provides key of the same target, so the withholding has to
		// discriminate within one edge rather than blanking whatever it delivered.
		if n := strings.Count(body, "STORE_HOST: store"); n != 2 {
			t.Errorf("%s must keep the same edge's undeclared delivery key verbatim in both pillars; got %d occurrence(s)\n--- slot ---\n%s", slot, n, body)
		}
	}
	// The published projection in the same slot is still withheld, so closing the
	// delivery gap did not move the withholding from one surface to the other.
	usesBody := crossNodeSlot(t, result, "app", "04-inject-mapping.yaml")
	if !strings.Contains(usesBody, "zzTargetSecret: "+redact.Placeholder()) {
		t.Errorf("the published provides value must still be withheld\n--- slot ---\n%s", usesBody)
	}
	// And the edge's authored mapping itself is displayed verbatim: it names a
	// provides KEY, not a value, so nothing about it is a datum to withhold.
	if !strings.Contains(usesBody, "STORE_SECRET: zzTargetSecret") {
		t.Errorf("the edge's authored inject mapping names keys and must survive verbatim\n--- slot ---\n%s", usesBody)
	}
}
