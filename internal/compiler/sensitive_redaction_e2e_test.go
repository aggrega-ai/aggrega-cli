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
	"02-provides-evaluation.yaml",
	"03-inject-hydration.yaml",
	"04-inject-mapping.yaml",
	"06-source-locals.yaml",
	"08-workload-synthesis.yaml",
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

	// The pipe is populated from inject hydration onward; slots 01/02 predate it.
	for _, slot := range []string{
		"03-inject-hydration.yaml",
		"04-inject-mapping.yaml",
		"06-source-locals.yaml",
		"08-workload-synthesis.yaml",
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

// The derived-withholding fixture is a sibling Release over the same catalog. It
// exists separately so the fixture above stays byte-identical: a Recipe that
// computes values from a declared leaf needs its own locals block and its own
// rendered artifact, and adding either to the fixture above would have moved the
// artifact this file asserts on.
const derivedArtifactPath = "dist/derived/compose/docker-compose.yaml"

const (
	// The plaintext each declared-sensitive datum carries in the derived fixture.
	// Distinct from the fixture above so no assertion can pass on the wrong one.
	derivedSecret = "derived-secret-plaintext" // component values.dbPassword
	derivedAPIKey = "derived-apikey-plaintext" // component infra.apiKey
	derivedToken  = "derived-token-plaintext"  // component values.creds.adminToken
)

// derivedLocalsSlots are the trace slots whose projection carries the resolved
// locals block. The last two also carry the rendered .workload document, which is
// verbatim by design, so a whole-body plaintext check applies only to the first
// two — the per-entry assertions below run on all four.
var derivedLocalsSlots = []string{
	"06-source-locals.yaml",
	"08-workload-synthesis.yaml",
	"09b-feature-rendering-boundary.yaml",
	"10-engine-synthesis/app.compose-fragment.yaml",
}

var derivedPillarOnlySlots = []string{
	"06-source-locals.yaml",
	"08-workload-synthesis.yaml",
}

func derivedRedactionBuild(t *testing.T) *pipeline.Result {
	t.Helper()
	return pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  "testdata/sensitive-redaction/locals-release.yaml",
		CatalogPaths: []string{"testdata/sensitive-redaction/catalog/base"},
		IncludeTrace: true,
	})
}

func derivedTraceSlot(t *testing.T, result *pipeline.Result, slot string) string {
	t.Helper()
	want := "boundaries/derived/app/" + slot
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

// TestSensitiveRedaction_TraceWithholdsDerivedLocals is the proof that
// withholding follows the DERIVATION and not just the declared key. Every entry
// asserted here is a local whose own key nobody declared; what identifies it is
// the reference its template made.
//
// Each withheld entry is paired with a structurally identical sibling that reads
// nothing declared. Without those pairs a rule that blanked the whole block would
// satisfy every positive assertion.
func TestSensitiveRedaction_TraceWithholdsDerivedLocals(t *testing.T) {
	t.Parallel()
	result := derivedRedactionBuild(t)

	withheld := []struct {
		key    string
		reason string
	}{
		{"zzDsn", "interpolates a declared values leaf"},
		{"infraKeyed", "interpolates a declared infra leaf"},
		{"credsCensus", "iterates an object holding a declared leaf"},
		{"dollarAnchored", "reads the root through $ inside a rebound body"},
		{"wrapsDsn", "reads a local already withheld above it"},
	}
	// Each control is the same template shape over a path nothing declares.
	controls := map[string]string{
		"appLabel":    "demoapp-local",
		"infraPlain":  "region=eu-west-1",
		"portsCensus": "admin=9090;http=8080;",
		"wrapsLabel":  "wrapped[demoapp-local]",
	}

	for _, slot := range derivedLocalsSlots {
		body := derivedTraceSlot(t, result, slot)

		for _, w := range withheld {
			if !strings.Contains(body, w.key+": "+redact.Placeholder()) {
				t.Errorf("%s must withhold local %q (%s), keeping its key\n--- slot ---\n%s", slot, w.key, w.reason, body)
			}
		}
		for key, want := range controls {
			if !strings.Contains(body, key+": "+want) {
				t.Errorf("%s must keep local %q verbatim as %q\n--- slot ---\n%s", slot, key, want, body)
			}
		}
		// Author order survives the substitution. zzDsn is authored first and
		// sorts last, so a sort creeping in would move it below appLabel.
		if first, second := strings.Index(body, "zzDsn:"), strings.Index(body, "appLabel:"); first < 0 || second < 0 || first > second {
			t.Errorf("%s must show the locals block in author order (zzDsn before appLabel); got indexes %d and %d\n--- slot ---\n%s", slot, first, second, body)
		}
	}

	for _, slot := range derivedPillarOnlySlots {
		body := derivedTraceSlot(t, result, slot)
		for _, plaintext := range []string{derivedSecret, derivedAPIKey, derivedToken} {
			if strings.Contains(body, plaintext) {
				t.Errorf("%s leaked %q through a computed value\n--- slot ---\n%s", slot, plaintext, body)
			}
		}
	}
}

// TestSensitiveRedaction_DerivedArtifactCarriesPlaintext is the boundary's other
// half for the derived axis: every computed value reaches the deployable in the
// clear, including the ones the trace withholds.
func TestSensitiveRedaction_DerivedArtifactCarriesPlaintext(t *testing.T) {
	t.Parallel()
	result := derivedRedactionBuild(t)

	body, ok := result.Artifacts[derivedArtifactPath]
	if !ok {
		paths := make([]string, 0, len(result.Artifacts))
		for p := range result.Artifacts {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		t.Fatalf("expected the merged compose artifact at %q; got %v", derivedArtifactPath, paths)
	}
	artifact := string(body)

	for _, want := range []string{derivedSecret, derivedAPIKey, derivedToken, "demoapp-local"} {
		if !strings.Contains(artifact, want) {
			t.Errorf("the rendered artifact must carry %q verbatim; a placeholder there would be an undeployable manifest\n--- artifact ---\n%s", want, artifact)
		}
	}
	if strings.Contains(artifact, redact.Placeholder()) {
		t.Errorf("the rendered artifact must never contain the redaction placeholder\n--- artifact ---\n%s", artifact)
	}
}

// workloadProjectionSlots are this fixture's trace slots that project the rendered
// .workload document. Enumerated rather than sampled: a clause that withheld the
// projection at one pass and not another would leave every other slot passing.
var workloadProjectionSlots = []string{
	"09b-feature-rendering-boundary.yaml",
	"10-engine-synthesis/app.compose-fragment.yaml",
}

// TestSensitiveRedaction_TraceShowsTheWorkloadAsASubstitutedReRender covers the
// projection that is not addressable by key. .workload is a parsed document: a
// datum a template interpolated into it lands in a field no author declared, and
// nothing links that field back to the declaration it came from, so there is no key
// to substitute AT — but the context the template read is fully key-addressed, so
// the display shows the same template evaluated again against a substituted context.
//
// The reader therefore keeps the whole document with the replacement text in the
// secret positions, instead of losing the view exactly where debugging is hardest.
// What that costs is honesty work: the display must SAY the document is a re-render,
// because a reader told nothing would take it for what the build emitted.
//
// Every assertion below reads the DECODED document rather than the slot's text, so
// each one names the field it is about — the same values also reach the pillars in
// the same slot, and a whole-body search could not tell the two apart.
func TestSensitiveRedaction_TraceShowsTheWorkloadAsASubstitutedReRender(t *testing.T) {
	t.Parallel()
	result := sensitiveRedactionBuild(t)

	for _, slot := range workloadProjectionSlots {
		body := traceSlot(t, result, slot)

		if !strings.Contains(body, redact.SubstitutedProjection()) {
			t.Errorf("%s must announce that its document is a re-render and name the flag that shows the emitted one\n--- slot ---\n%s", slot, body)
		}
		if strings.Contains(body, redact.WithheldProjection()) {
			t.Errorf("%s withheld a projection it could have re-rendered\n--- slot ---\n%s", slot, body)
		}

		leaves := workloadLeaves(t, body)
		// Each position reading a declared-sensitive datum, substituted. The three
		// cover the two withholding mechanisms the document can carry: two declared
		// leaves and one delivery key marked by an attachment's inject mapping.
		for _, path := range []string{
			"containers.main.variables.APP_DB_PASSWORD",
			"containers.main.variables.APP_ADMIN_TOKEN",
			"containers.main.variables.DATABASE_PASSWORD",
		} {
			if got := leaves[path]; got != redact.Placeholder() {
				t.Errorf("%s: %s must show the replacement text; got %q", slot, path, got)
			}
		}
		// The controls: undeclared siblings in the SAME document survive with their
		// own values. Without these, a display that blanked every variable would
		// satisfy every assertion above.
		for path, want := range map[string]string{
			"containers.main.variables.APP_NAME":      plainValuesLeaf,
			"containers.main.variables.APP_POOL_SIZE": plainNestedLeaf,
			"containers.main.variables.DATABASE_NAME": plainDelivered,
		} {
			if got := leaves[path]; got != want {
				t.Errorf("%s: %s reads nothing declared and must survive as %q; got %q", slot, path, want, got)
			}
		}
		// No declared-sensitive plaintext anywhere in the decoded document.
		for path, text := range leaves {
			for _, plaintext := range []string{sensitiveValuesLeaf, sensitiveNestedLeaf, sensitiveDelivered} {
				if strings.Contains(text, plaintext) {
					t.Errorf("%s: %s leaked %q", slot, path, plaintext)
				}
			}
		}
		// The pillar projection in the SAME slot is still withheld per key, so the
		// document-scale mechanism did not replace the key-addressed one.
		if !strings.Contains(body, "dbPassword: "+redact.Placeholder()) {
			t.Errorf("%s must still withhold the declared leaf per key\n--- slot ---\n%s", slot, body)
		}
		if !strings.Contains(body, "appName: "+plainValuesLeaf) {
			t.Errorf("%s must keep an undeclared sibling leaf verbatim\n--- slot ---\n%s", slot, body)
		}
	}
}

// TestSensitiveRedaction_AttachmentOwnLeafIsAKnownGap records, as executable fact,
// the one thing the withholding does not reach on this fixture.
//
// An attachment's OWN declared-sensitive values leaf: the per-node pillar
// projection carries the host component's pillars, not the per-attachment view, so
// the datum is absent from the display rather than withheld in it. Absent and
// withheld are different states, and the difference matters — if the projection
// ever gains that surface, the datum starts printing with nothing covering it. The
// pin turns that into a visible failure instead of a silent leak.
func TestSensitiveRedaction_AttachmentOwnLeafIsAKnownGap(t *testing.T) {
	t.Parallel()
	result := sensitiveRedactionBuild(t)

	if len(result.Trace) == 0 {
		t.Fatal("the build produced no trace; this pin would be vacuous")
	}
	for slot := range result.Trace {
		if strings.Contains(string(result.Trace[slot]), sensitiveByValueLeaf) {
			t.Errorf("%s carries the attachment's declared-sensitive values leaf %q; the projection gained a surface that needs its own withholding set", slot, sensitiveByValueLeaf)
		}
	}
}
