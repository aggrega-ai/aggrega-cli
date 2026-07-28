// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package compiler_test — end-to-end proof of the host-redaction boundary: a
// declared-sensitive datum is withheld from the per-pass trace a human reads,
// and reaches the rendered artifact in the clear because the artifact is the
// deployable.
//
// Every assertion here is paired. A redaction that never fires is
// indistinguishable from correct behaviour in a test that only asserts the
// placeholder is present somewhere, so each positive assertion sits beside a
// non-sensitive sibling on the SAME surface whose value must survive verbatim.

package compiler_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
)

const (
	sensitiveArtifactPath = "dist/cluster/compose/docker-compose.yaml"

	// The plaintext each declared-sensitive datum carries in the fixture.
	sensitiveValuesLeaf  = "hunter2-plaintext"   // component values.dbPassword
	sensitiveNestedLeaf  = "adm1n-plaintext"     // component values.tuning.adminToken
	sensitiveDelivered   = "s3cr3t-from-feature" // delivered through the tainted DATABASE_PASSWORD key
	sensitiveByValueLeaf = "by-value-plaintext"  // the attachment's own values.literalPassword

	// The plaintext each NON-sensitive sibling carries. These prove the
	// redaction discriminates instead of blanking whatever it can reach.
	plainValuesLeaf = "demo"     // component values.appName
	plainNestedLeaf = "25"       // component values.tuning.poolSize
	plainDelivered  = "db-creds" // delivered through the untainted DATABASE_NAME key
)

// pillarProjectionSlots are the trace slots whose whole body is the per-pass
// pillar projection — values, infra and the delivery pipe — with no derived
// document alongside. Every declared-sensitive datum in these slots is
// identifiable by key, so these are the slots the redaction fully covers.
var pillarProjectionSlots = []string{
	"01-pillars-initial.yaml",
	"02-pass1-provides.yaml",
	"03-pass2-inject.yaml",
	"04-phaseA-inject.yaml",
	"06-phaseB-locals.yaml",
	"08-phaseB-workload.yaml",
}

func sensitiveRedactionBuild(t *testing.T) *pipeline.Result {
	t.Helper()
	return pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  "testdata/sensitive-redaction/release.yaml",
		CatalogPaths: []string{"testdata/sensitive-redaction/catalog/base"},
		IncludeTrace: true,
	})
}

// traceSlot returns the body of one trace entry for the fixture's single node,
// failing when the slot is absent — an assertion against a missing slot would
// be vacuous, which is the one failure mode this whole file exists to avoid.
func traceSlot(t *testing.T, result *pipeline.Result, slot string) string {
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

// TestSensitiveRedaction_ArtifactCarriesPlaintext is the boundary's other half:
// the rendered artifact is the deployable and is never redacted. It fails the
// moment a redaction call is introduced anywhere on an artifact path — which is
// exactly the regression the boundary must make impossible.
func TestSensitiveRedaction_ArtifactCarriesPlaintext(t *testing.T) {
	t.Parallel()
	result := sensitiveRedactionBuild(t)

	body, ok := result.Artifacts[sensitiveArtifactPath]
	if !ok {
		paths := make([]string, 0, len(result.Artifacts))
		for p := range result.Artifacts {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		t.Fatalf("expected the merged compose artifact at %q; got %v", sensitiveArtifactPath, paths)
	}
	artifact := string(body)

	for _, want := range []string{
		sensitiveValuesLeaf,
		sensitiveNestedLeaf,
		sensitiveDelivered,
		plainValuesLeaf,
		plainDelivered,
	} {
		if !strings.Contains(artifact, want) {
			t.Errorf("the rendered artifact must carry %q verbatim; a placeholder there would be an undeployable manifest\n--- artifact ---\n%s", want, artifact)
		}
	}
	if strings.Contains(artifact, redact.Placeholder()) {
		t.Errorf("the rendered artifact must never contain the redaction placeholder\n--- artifact ---\n%s", artifact)
	}
}

// TestSensitiveRedaction_TraceWithholdsDeclaredLeaves covers the two authored
// leaf shapes on the component's own pillar — a top-level leaf and a nested one
// — each beside an undeclared sibling in the same object, at every pass whose
// projection is pillars-only.
func TestSensitiveRedaction_TraceWithholdsDeclaredLeaves(t *testing.T) {
	t.Parallel()
	result := sensitiveRedactionBuild(t)

	for _, slot := range pillarProjectionSlots {
		body := traceSlot(t, result, slot)

		if strings.Contains(body, sensitiveValuesLeaf) {
			t.Errorf("%s leaked the declared-sensitive values leaf %q\n--- slot ---\n%s", slot, sensitiveValuesLeaf, body)
		}
		if strings.Contains(body, sensitiveNestedLeaf) {
			t.Errorf("%s leaked the nested declared-sensitive leaf %q\n--- slot ---\n%s", slot, sensitiveNestedLeaf, body)
		}
		// Shape survives: the key is still printed, only its value is withheld.
		if !strings.Contains(body, "dbPassword: "+redact.Placeholder()) {
			t.Errorf("%s must keep the withheld leaf's key\n--- slot ---\n%s", slot, body)
		}
		if !strings.Contains(body, "adminToken: "+redact.Placeholder()) {
			t.Errorf("%s must keep the withheld nested leaf's key\n--- slot ---\n%s", slot, body)
		}
		// Discrimination: the undeclared siblings survive verbatim.
		if !strings.Contains(body, "appName: "+plainValuesLeaf) {
			t.Errorf("%s must keep an undeclared sibling leaf verbatim\n--- slot ---\n%s", slot, body)
		}
		if !strings.Contains(body, `poolSize: "`+plainNestedLeaf+`"`) {
			t.Errorf("%s must keep an undeclared nested sibling verbatim\n--- slot ---\n%s", slot, body)
		}
	}
}

// TestSensitiveRedaction_TraceWithholdsTaintedDeliveryKey is the assertion that
// proves the per-delivery-key taint is CONSUMED rather than merely computed.
// The datum here is never declared on the component: it enters through an
// attachment's inject mapping whose `from:` names a provides key the Feature
// declares sensitive, and the only thing that can identify it at the pipe is
// the marking the graph carries.
//
// The `all` pillar is asserted alongside `env` because it is the host-built
// union of the typed mechanisms — the same datum reappears there, and a
// redaction covering only the typed pillar would re-print it two lines below.
func TestSensitiveRedaction_TraceWithholdsTaintedDeliveryKey(t *testing.T) {
	t.Parallel()
	result := sensitiveRedactionBuild(t)

	// The pipe is populated from Pass 2 onward; slots 01/02 predate it.
	for _, slot := range []string{
		"03-pass2-inject.yaml",
		"04-phaseA-inject.yaml",
		"06-phaseB-locals.yaml",
		"08-phaseB-workload.yaml",
	} {
		body := traceSlot(t, result, slot)

		if strings.Contains(body, sensitiveDelivered) {
			t.Errorf("%s leaked the value delivered through the tainted delivery key %q\n--- slot ---\n%s", slot, sensitiveDelivered, body)
		}
		if n := strings.Count(body, "DATABASE_PASSWORD: "+redact.Placeholder()); n != 2 {
			t.Errorf("%s must show the tainted delivery key withheld in both the env pillar and the union pillar; got %d occurrence(s)\n--- slot ---\n%s", slot, n, body)
		}
		if n := strings.Count(body, "DATABASE_NAME: "+plainDelivered); n != 2 {
			t.Errorf("%s must keep the untainted sibling delivery key verbatim in both pillars; got %d occurrence(s)\n--- slot ---\n%s", slot, n, body)
		}
	}
}

// TestSensitiveRedaction_KnownCoverageGaps records, as executable fact, the two
// things the redaction does NOT reach today. Both are pinned rather than left
// implicit so the limit is discoverable from the test suite instead of from a
// leaked trace, and so the test fails — visibly, as a signal to delete it —
// when either gap is closed.
//
// Gap 1: a DERIVED projection. .workload is a template-rendered document. A
// sensitive leaf that a template interpolated into it is no longer identifiable
// by key, and nothing upstream carries a provenance fact linking a rendered
// field back to the declaration it came from. Recovering it would take either
// template-level taint propagation or a search of the document's content for
// the value, and content inspection is precisely what the compiler must not do.
//
// Gap 2: an attachment's OWN declared-sensitive values leaf. The per-node
// pillar projection carries the host component's pillars, not .features.<name>,
// so the datum is absent from the trace rather than redacted in it. If the
// projection ever gains that surface, this assertion turns it into a visible
// failure instead of a silent leak.
func TestSensitiveRedaction_KnownCoverageGaps(t *testing.T) {
	t.Parallel()
	result := sensitiveRedactionBuild(t)

	// Gap 1 — the derived .workload projection at passes B.3 and later.
	derived := traceSlot(t, result, "09b-phaseB6-boundary-fr.yaml")
	if !strings.Contains(derived, "workload:") {
		t.Fatalf("the fixture's B.6 slot no longer carries a .workload projection; this pin is now vacuous\n--- slot ---\n%s", derived)
	}
	if !strings.Contains(derived, sensitiveValuesLeaf) || !strings.Contains(derived, sensitiveDelivered) {
		t.Errorf("the .workload projection no longer carries the plaintext: the derived-projection gap has been closed and this pin must be deleted\n--- slot ---\n%s", derived)
	}
	// The pillar projection in the SAME slot is still redacted, so the gap is
	// scoped to the derived document rather than to the late passes.
	if strings.Contains(derived, "dbPassword: "+sensitiveValuesLeaf) {
		t.Errorf("the pillar projection at B.6 must still be redacted\n--- slot ---\n%s", derived)
	}

	// Gap 2 — the attachment's own by-value declared-sensitive leaf.
	for slot := range result.Trace {
		if strings.Contains(string(result.Trace[slot]), sensitiveByValueLeaf) {
			t.Errorf("%s carries the attachment's declared-sensitive values leaf %q; the projection gained a surface that needs its own redaction set", slot, sensitiveByValueLeaf)
		}
	}
}
