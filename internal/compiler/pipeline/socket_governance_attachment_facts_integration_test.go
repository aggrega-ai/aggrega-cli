// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package pipeline_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// These tests prove the governance socket carries the facts a policy needs to
// judge an attachment ITSELF: the attachment's verbatim catalog labels, its
// verbatim catalog annotations, whether it fills a declared slot, and its
// sensitivity marking. The proof is a real guest run through the real socket,
// because a payload dump shows only that fields exist — it cannot show that a
// policy can be written against them.
//
// Each halt is paired with a pass over a control fixture that moves exactly one
// variable, so a halt is attributable. A policy that rejected everything would
// satisfy every halt assertion here and fail every paired pass.

const (
	governanceWireDir       = "../../../internal/compiler/testdata/governance-wire/"
	governanceWireCatalog   = governanceWireDir + "catalog/base"
	governanceWireManifest  = governanceWireDir + "manifest.yaml"
	governanceFactsURI      = "proof://governance-attachment-facts"
	governanceFactsArtifact = "governance-attachment-facts-go.wasm"

	// governanceFactsAuthorLabel is a label of the fixture catalog author's own
	// invention. The compiler has no knowledge of the key, so a verdict that
	// quotes its value could only have read it off the wire.
	governanceFactsAuthorLabel = "governance.example/rotation"

	// governanceFactsAuthorAnnotation is the annotations-side counterpart, and
	// is an annotation of the fixture catalog author's own invention for the
	// same reason: a rule keyed on a reserved-namespace key would keep firing
	// under a namespace filter, so only a key the host has no reason to know
	// can show the annotations arrived verbatim.
	governanceFactsAuthorAnnotation = "governance.example/review-ticket"

	// The two authored leaves the credential fixture writes a value at, named
	// here because the assertions below need BOTH: one is the coordinate a
	// verdict must report, the other the coordinate a verdict must never
	// report, and a pair stated as two bare literals reads as one fact repeated
	// rather than as the discrimination it is.
	//
	// The Feature declares literalPassword sensitive and deliberately leaves
	// secretName undeclared — a locator is the public half of a referenced
	// credential — so which of the two a marking names is the whole test of
	// whether the marking follows the declarations or reaches past them.
	governanceFactsDeclaredLeaf   = "values.literalPassword"
	governanceFactsUndeclaredLeaf = "values.secretName"
)

// loadGovernanceWire loads one variant of the governance-wire fixture —
// "both", "no-free", "only-free", "no-marked-delivery" or "no-sensitive-datum" —
// returning the inputs and lookups RunPipeline consumes. The variants share one
// catalog and one Manifest and differ only in their Aggregate, which is what
// makes a halt/pass pair attributable.
//
// The three that carry the sensitivity axis form a chain in which each step moves
// exactly ONE variable, so a route can be attributed rather than inferred:
// "both" marks a delivery key and authors a declared-sensitive leaf;
// "no-marked-delivery" fills that delivery key from a public locator instead and
// keeps the authored leaf; "no-sensitive-datum" then drops the value at that
// leaf. Every one of the three keeps the per-attachment roll-up true, because
// the roll-up reads the Feature's declarations and no variant touches those.
func loadGovernanceWire(t *testing.T, variant string) (pipeline.Inputs, resolver.CatalogLookup, renderer.RecipeLookup, topology.SubAggregateLoader) {
	t.Helper()
	rel, err := resolver.LoadRelease(governanceWireDir + "release-" + variant + ".yaml")
	if err != nil {
		t.Fatalf("load release (%s): %v", variant, err)
	}
	manifest, err := resolver.LoadManifest(governanceWireManifest)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	aggPath := governanceWireDir + "aggregate-" + variant + ".yaml"
	aggBytes, err := os.ReadFile(aggPath)
	if err != nil {
		t.Fatalf("read aggregate (%s): %v", variant, err)
	}
	cat, err := catalog.LoadAll("", governanceWireCatalog)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	inputs := pipeline.Inputs{
		Release:           rel,
		Manifest:          manifest,
		AggregateRawBytes: aggBytes,
		AggregatePath:     aggPath,
		WorkspaceConfig:   workspaceconfig.Default(),
		CompilerVersion:   fixtureCompilerVersion,
	}
	return inputs, cat.CatalogLookup(), cat.RecipeLookupWithManifest(manifest), cat.SubAggregateLoader()
}

// governanceFactsInputs attaches the attachment-reasoning verdict guest at the
// ir-verdict role over one fixture variant, with the rule and severity the
// scenario exercises. echo_label is always set, so every verdict message this
// suite asserts on also reports whether an author-invented label crossed the
// socket boundary.
func governanceFactsInputs(t *testing.T, variant, rule, severity string) (pipeline.Inputs, resolver.CatalogLookup, renderer.RecipeLookup, topology.SubAggregateLoader) {
	t.Helper()
	inputs, buildLookup, recipeLookup, subAgg := loadGovernanceWire(t, variant)
	inputs.WorkspaceConfig.Plugins.Governance = []workspaceconfig.PluginRef{{
		URI:  governanceFactsURI,
		Role: plugintype.RoleIRVerdict,
		With: &workspaceconfig.PluginRefWith{Settings: map[string]any{
			"rule":       rule,
			"severity":   severity,
			"echo_label": governanceFactsAuthorLabel,
		}},
	}}
	inputs.PluginPathLookup = fixtureSocketLookup(t, buildLookup, map[string]string{
		governanceFactsURI: governanceFactsArtifact,
	})
	return inputs, buildLookup, recipeLookup, subAgg
}

// TestGovernanceFreeAttachmentDenyE2E proves an operator can forbid free-form
// attachment: the guest reads the declaration-origin marker off the wire, finds
// the attachment that fills no declared slot, and halts. The halt names the
// attachment, its type and the marker value, so the verdict could not have been
// emitted without reading them.
func TestGovernanceFreeAttachmentDenyE2E(t *testing.T) {
	ctx := context.Background()
	inputs, buildLookup, recipeLookup, subAgg := governanceFactsInputs(t, "both", "no-free-attachments", analyzer.SeverityHalt)

	_, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
	if err == nil {
		t.Fatalf("free-attachment halt: expected a halt, got nil")
	}
	if !errors.Is(err, analyzer.ErrPolicyViolation()) {
		t.Fatalf("free-attachment halt not errors.Is ErrPolicyViolation: %v", err)
	}
	msg := err.Error()
	t.Logf("free-attachment verdict halt:\n%s", msg)
	for _, want := range []string{`attachment "metrics"`, `feature.gov/telemetry`, `binding "free"`} {
		if !strings.Contains(msg, want) {
			t.Errorf("free-attachment halt does not report %q:\n%v", want, msg)
		}
	}
	// The label the compiler has no reason to know, quoted back by the guest.
	if !strings.Contains(msg, governanceFactsAuthorLabel+"=never") {
		t.Errorf("free-attachment halt did not echo the author-declared label %q, so the labels did not reach the guest verbatim:\n%v",
			governanceFactsAuthorLabel, msg)
	}
}

// TestGovernanceFreeAttachmentPassE2E is the paired control: the same rule over
// the fixture whose ONLY difference is the absence of the free attachment. The
// build completes and renders, which is what distinguishes a working rule from
// one that rejects everything.
func TestGovernanceFreeAttachmentPassE2E(t *testing.T) {
	ctx := context.Background()
	inputs, buildLookup, recipeLookup, subAgg := governanceFactsInputs(t, "no-free", "no-free-attachments", analyzer.SeverityHalt)

	res, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
	if err != nil {
		t.Fatalf("free-attachment control: unexpected halt: %v", err)
	}
	if len(res.Artifacts) == 0 {
		t.Fatalf("free-attachment control produced no artifacts")
	}
}

// TestGovernanceSensitiveByValueDenyE2E proves an operator can refuse what the
// compiler only warns about: the guest reads the per-delivery-key sensitivity
// marking and halts the build that delivers declared-sensitive material to a
// by-value delivery key. The halt names the mechanism, the delivery key and the
// provides key the marking came from.
func TestGovernanceSensitiveByValueDenyE2E(t *testing.T) {
	ctx := context.Background()
	inputs, buildLookup, recipeLookup, subAgg := governanceFactsInputs(t, "both", "no-sensitive-by-value", analyzer.SeverityHalt)

	_, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
	if err == nil {
		t.Fatalf("sensitive-by-value halt: expected a halt, got nil")
	}
	if !errors.Is(err, analyzer.ErrPolicyViolation()) {
		t.Fatalf("sensitive-by-value halt not errors.Is ErrPolicyViolation: %v", err)
	}
	msg := err.Error()
	t.Logf("sensitive-by-value verdict halt:\n%s", msg)
	for _, want := range []string{`attachment "credentials"`, `env key "DATABASE_PASSWORD"`, `provides key "password"`} {
		if !strings.Contains(msg, want) {
			t.Errorf("sensitive-by-value halt does not report %q:\n%v", want, msg)
		}
	}
	if !strings.Contains(msg, governanceFactsAuthorLabel+"=quarterly") {
		t.Errorf("sensitive-by-value halt did not echo the author-declared label %q:\n%v", governanceFactsAuthorLabel, msg)
	}
}

// TestGovernanceSensitiveByValueAuthoredLeafDenyE2E is the per-route probe for
// the OTHER route the same rule covers: a declared-sensitive datum written into
// the attachment's own authored payload rather than delivered through an inject
// key. The fixture differs from the halt above in exactly one variable — its
// delivery key fills from a public locator instead of the declared-sensitive
// provides key — so no delivery key is marked and the only remaining offence is
// the authored leaf.
//
// Three assertions carry the attribution. The verdict must NAME the authored
// leaf, which it can only do by reading the per-leaf marking off the wire. It
// must NOT name a delivery key, which is what shows the two routes are reported
// separately rather than folded into one message: a rule that fell back to the
// delivery wording would satisfy a bare "did it halt" check and fail this.
//
// And it must NOT name the OTHER leaf this same attachment authors a value at.
// That third assertion is the one a presence check cannot stand in for, and it
// is why the pair of leaves is the fixture rather than a single one. The
// attachment writes a value at secretName as well, which the Feature declares
// nothing about; a marking that reached past the declarations would name it, the
// build would be halted over material its author left readable, and every
// presence assertion above would still be satisfied. Marking too widely is the
// failure with no cheap undo — it declares public data secret and then refuses
// builds that should pass — so the verdict has to show the unmarked leaf staying
// unmarked in the same breath as it names the marked one.
func TestGovernanceSensitiveByValueAuthoredLeafDenyE2E(t *testing.T) {
	ctx := context.Background()
	inputs, buildLookup, recipeLookup, subAgg := governanceFactsInputs(t, "no-marked-delivery", "no-sensitive-by-value", analyzer.SeverityHalt)

	_, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
	if err == nil {
		t.Fatalf("authored-leaf halt: expected a halt, got nil")
	}
	if !errors.Is(err, analyzer.ErrPolicyViolation()) {
		t.Fatalf("authored-leaf halt not errors.Is ErrPolicyViolation: %v", err)
	}
	msg := err.Error()
	t.Logf("authored-leaf verdict halt:\n%s", msg)
	for _, want := range []string{`attachment "credentials"`, `authored leaf "` + governanceFactsDeclaredLeaf + `"`} {
		if !strings.Contains(msg, want) {
			t.Errorf("authored-leaf halt does not report %q, so the per-leaf marking did not reach the guest:\n%v", want, msg)
		}
	}
	if strings.Contains(msg, governanceFactsUndeclaredLeaf) {
		t.Errorf("authored-leaf halt names %q, a leaf this attachment authors a value at but the Feature declares "+
			"nothing about; the marking joins declaration to declaration, so a verdict reaching this leaf means the "+
			"marking was applied more widely than any author asked for:\n%v", governanceFactsUndeclaredLeaf, msg)
	}
	if strings.Contains(msg, "DATABASE_PASSWORD") {
		t.Errorf("authored-leaf halt names a delivery key, but this fixture marks none; the two routes must be "+
			"reported as the separate findings they are:\n%v", msg)
	}
	if !strings.Contains(msg, governanceFactsAuthorLabel+"=quarterly") {
		t.Errorf("authored-leaf halt did not echo the author-declared label %q:\n%v", governanceFactsAuthorLabel, msg)
	}
}

// TestGovernanceSensitiveByValuePassE2E is the paired control for both routes:
// the same rule over the fixture whose ONLY difference from the authored-leaf
// halt above is that no value is authored at the declared-sensitive leaf. Neither
// route carries a declared-sensitive datum, so the build completes.
//
// The attachment is still sensitive at the per-attachment roll-up here — that
// answer reads the Feature's declarations, which this fixture does not touch — so
// a rule keyed on the roll-up rather than on the two per-datum markings would
// halt here and this control would fail. That is the discrimination the control
// exists for, and it is why the control is not simply an attachment with nothing
// sensitive about it.
func TestGovernanceSensitiveByValuePassE2E(t *testing.T) {
	ctx := context.Background()
	inputs, buildLookup, recipeLookup, subAgg := governanceFactsInputs(t, "no-sensitive-datum", "no-sensitive-by-value", analyzer.SeverityHalt)

	res, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
	if err != nil {
		t.Fatalf("sensitive-by-value control: unexpected halt: %v", err)
	}
	if len(res.Artifacts) == 0 {
		t.Fatalf("sensitive-by-value control produced no artifacts")
	}
}

// TestGovernanceSecretKindFromLabelsDenyE2E proves the labels-carrying wire is a
// superset of a host-computed credential verdict: the guest reproduces the
// "is this attachment a credential" judgement from the catalog family label
// alone, with no host-supplied bucket to read. The verdict names the attachment
// it judged and the label it judged it by; its paired control below proves the
// judgement is the label's and not a reflex that fires on any attachment.
func TestGovernanceSecretKindFromLabelsDenyE2E(t *testing.T) {
	ctx := context.Background()
	inputs, buildLookup, recipeLookup, subAgg := governanceFactsInputs(t, "both", "deny-secret-kind", analyzer.SeverityHalt)

	_, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
	if err == nil {
		t.Fatalf("secret-kind halt: expected a halt, got nil")
	}
	if !errors.Is(err, analyzer.ErrPolicyViolation()) {
		t.Fatalf("secret-kind halt not errors.Is ErrPolicyViolation: %v", err)
	}
	msg := err.Error()
	t.Logf("secret-kind verdict halt:\n%s", msg)
	if !strings.Contains(msg, `attachment "credentials"`) {
		t.Errorf("secret-kind halt does not name the credential attachment:\n%v", msg)
	}
	if strings.Contains(msg, `attachment "metrics"`) {
		t.Errorf("secret-kind halt fired on the attachment that declares no family label:\n%v", msg)
	}
	if !strings.Contains(msg, "feature.aggrega.ai/kind=secret") {
		t.Errorf("secret-kind halt does not report the label it judged:\n%v", msg)
	}
}

// TestGovernanceSecretKindPassesWithoutTheFamilyLabelE2E is the paired control
// for the superset proof: the same labels-only rule over a fixture whose single
// attachment declares no credential family label. The build completes, so the
// rule is reading the label rather than objecting to attachment as such.
func TestGovernanceSecretKindPassesWithoutTheFamilyLabelE2E(t *testing.T) {
	ctx := context.Background()
	inputs, buildLookup, recipeLookup, subAgg := governanceFactsInputs(t, "only-free", "deny-secret-kind", analyzer.SeverityHalt)

	res, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
	if err != nil {
		t.Fatalf("secret-kind control: unexpected halt: %v", err)
	}
	if len(res.Artifacts) == 0 {
		t.Fatalf("secret-kind control produced no artifacts")
	}
}

// TestGovernanceCatalogAnnotationDenyE2E proves a policy can judge an attached
// FEATURE by its catalog annotations, which is the half of the catalog metadata
// the wire previously carried for a Component and not for a Feature. The guest
// reads an annotation in a namespace the compiler has no knowledge of, finds the
// attachment whose review is outstanding, and halts. The halt names the
// attachment and the annotation key and value it judged by, so the verdict could
// not have been emitted without reading them.
//
// The other attachment in the same build declares the SAME annotation key with a
// completed review, so a rule firing on the key's presence rather than on its
// value would name it too. Its absence from the message is asserted.
func TestGovernanceCatalogAnnotationDenyE2E(t *testing.T) {
	ctx := context.Background()
	inputs, buildLookup, recipeLookup, subAgg := governanceFactsInputs(t, "both", "deny-unreviewed-attachment", analyzer.SeverityHalt)

	_, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
	if err == nil {
		t.Fatalf("catalog-annotation halt: expected a halt, got nil")
	}
	if !errors.Is(err, analyzer.ErrPolicyViolation()) {
		t.Fatalf("catalog-annotation halt not errors.Is ErrPolicyViolation: %v", err)
	}
	msg := err.Error()
	t.Logf("catalog-annotation verdict halt:\n%s", msg)
	if !strings.Contains(msg, `attachment "credentials"`) {
		t.Errorf("catalog-annotation halt does not name the attachment whose review is outstanding:\n%v", msg)
	}
	if strings.Contains(msg, `attachment "metrics"`) {
		t.Errorf("catalog-annotation halt fired on the attachment whose review is complete, so it is keyed on the annotation's presence rather than its value:\n%v", msg)
	}
	if !strings.Contains(msg, governanceFactsAuthorAnnotation+"=none") {
		t.Errorf("catalog-annotation halt did not report the author-declared annotation %q it judged, so the annotations did not reach the guest verbatim:\n%v",
			governanceFactsAuthorAnnotation, msg)
	}
	// The labels-driven half of the same wire is unaffected: every verdict this
	// suite asserts on also echoes an author-declared LABEL, so a change that
	// broke the labels carry would show here as well as in its own tests.
	if !strings.Contains(msg, governanceFactsAuthorLabel+"=quarterly") {
		t.Errorf("catalog-annotation halt did not echo the author-declared label %q:\n%v", governanceFactsAuthorLabel, msg)
	}
}

// TestGovernanceCatalogAnnotationPassesWhenReviewedE2E is the paired control: the
// same rule over the fixture whose single attachment declares the same annotation
// key with a completed review. The build completes and renders, so the rule is
// reading the annotation's value rather than objecting to attachment as such.
func TestGovernanceCatalogAnnotationPassesWhenReviewedE2E(t *testing.T) {
	ctx := context.Background()
	inputs, buildLookup, recipeLookup, subAgg := governanceFactsInputs(t, "only-free", "deny-unreviewed-attachment", analyzer.SeverityHalt)

	res, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
	if err != nil {
		t.Fatalf("catalog-annotation control: unexpected halt: %v", err)
	}
	if len(res.Artifacts) == 0 {
		t.Fatalf("catalog-annotation control produced no artifacts")
	}
}

// TestGovernanceAttachmentFactsObservedAtTheGuest dumps, from inside the guest,
// every fact each attachment carried on the wire. It is the payload evidence
// read at the point of arrival rather than at the point of assembly: an info
// verdict cannot report a field the guest did not receive.
func TestGovernanceAttachmentFactsObservedAtTheGuest(t *testing.T) {
	ctx := context.Background()

	var res *pipeline.Result
	stderr := captureStderr(t, func() {
		inputs, buildLookup, recipeLookup, subAgg := governanceFactsInputs(t, "both", "observe", analyzer.SeverityInfo)
		r, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
		if err != nil {
			t.Fatalf("observe run: unexpected halt: %v", err)
		}
		res = r
	})
	if res == nil || len(res.Artifacts) == 0 {
		t.Fatalf("observe run produced no artifacts")
	}
	t.Logf("attachment facts as decoded inside the guest:\n%s", stderr)
	for _, want := range []string{
		`"binding":"credentials"`,
		`"binding":"free"`,
		`"feature.aggrega.ai/kind":"secret"`,
		`"governance.example/rotation":"quarterly"`,
		`"governance.example/rotation":"never"`,
		// The guest reports the annotations as the raw wire bytes it received, so
		// these two assertions pin AUTHOR order end to end rather than merely
		// arrival: the credential Feature declares review-ticket first and
		// data-residency second, which is the reverse of sorted order, and the
		// assertion is on the whole object rather than on each key separately.
		// A guest that decoded the set into a Go map could not report an order
		// at all, and a host that sorted the set would produce the other one.
		`"catalog_annotations":{"governance.example/review-ticket":"none","governance.example/data-residency":"eu-west"}`,
		`"catalog_annotations":{"governance.example/review-ticket":"OPS-4417"}`,
		// All three sensitivity units, observed at the point of arrival. The
		// third is asserted with its pillar prefix, which is what distinguishes
		// a coordinate a consumer can act on from a bare leaf name that two
		// pillars could both claim.
		`"sensitive":true`,
		`"sensitive_inject":{"env":{"DATABASE_PASSWORD":"password"}}`,
		`"sensitive_authored_leaves":["values.literalPassword"]`,
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the guest did not observe %s in its payload:\n%s", want, stderr)
		}
	}
}

// TestGovernanceVerdictMovesNoRenderedByte proves a verdict role shapes no
// output: the artifacts of a build with the guest attached are byte-identical to
// the artifacts of the same build with no governance attachment at all.
func TestGovernanceVerdictMovesNoRenderedByte(t *testing.T) {
	ctx := context.Background()

	inputs, buildLookup, recipeLookup, subAgg := loadGovernanceWire(t, "both")
	bare, err := pipeline.RunPipeline(ctx, inputs, buildLookup, recipeLookup, subAgg, pipeline.Options{})
	if err != nil {
		t.Fatalf("build without a governance attachment: %v", err)
	}

	// A rule that finds nothing to object to, so the build completes with the
	// guest in the pipeline.
	withGuest, buildLookup2, recipeLookup2, subAgg2 := governanceFactsInputs(t, "both", "observe", analyzer.SeverityInfo)
	guested, err := pipeline.RunPipeline(ctx, withGuest, buildLookup2, recipeLookup2, subAgg2, pipeline.Options{})
	if err != nil {
		t.Fatalf("build with the governance attachment: %v", err)
	}
	if len(bare.Artifacts) == 0 {
		t.Fatalf("baseline build produced no artifacts")
	}
	assertArtifactsByteIdentical(t, bare.Artifacts, guested.Artifacts)
}
