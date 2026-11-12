// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package compiler_test — end-to-end proof that the facts a governance plugin
// needs in order to judge an attachment for itself reach the IR the governance
// socket carries: each attachment's verbatim catalog labels, the declared slot
// it fills (or that it fills none), and its sensitivity marking.
//
// Every assertion here is paired against a sibling on the same surface that
// must NOT carry the fact, because a fact that is present everywhere is
// indistinguishable from a fact nothing computed: the slot-filled attachment's
// binding is checked beside the free attachment's, the credential Feature's
// family label beside a sibling Feature that declares none, and the marked
// delivery key beside the unmarked one.
package compiler_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

const (
	// governanceWireNode is the fixture's single node.
	governanceWireNode = "app"
	// governanceSlotAttachment fills the Component's one declared slot;
	// governanceFreeAttachment fills none.
	governanceSlotAttachment = "credentials"
	governanceFreeAttachment = "metrics"
	// governanceAuthorLabel is a label of the catalog author's own invention.
	// Nothing in the compiler knows the key, so its arrival is what
	// distinguishes a verbatim label carry from a filtered one.
	governanceAuthorLabel = "governance.example/rotation"
)

func governanceWireBuild(t *testing.T, variant string) *pipeline.Result {
	t.Helper()
	return pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  "testdata/governance-wire/release-" + variant + ".yaml",
		CatalogPaths: []string{"testdata/governance-wire/catalog/base"},
	})
}

// governanceAttachment reads one attachment off the built graph, failing when
// it is absent — an assertion against a missing attachment would be vacuous.
func governanceAttachment(t *testing.T, result *pipeline.Result, name string) types.IRFeatureEntry {
	t.Helper()
	if result.IRGraph == nil {
		t.Fatalf("pipeline produced no IR graph")
	}
	attachments, ok := result.IRGraph.Spec.FeatureValuesMapping.Get(governanceWireNode)
	if !ok {
		t.Fatalf("node %q carries no attachment map", governanceWireNode)
	}
	entry, ok := attachments.Get(name)
	if !ok {
		t.Fatalf("node %q carries no attachment %q (keys: %v)", governanceWireNode, name, attachments.Keys())
	}
	return entry
}

// TestGovernanceWire_AttachmentLabelsAreVerbatim proves the attached Feature's
// catalog labels reach the wire unfiltered, in the catalog entity's own
// declaration order, and that two attachments carry their own label sets rather
// than a merged or defaulted one.
func TestGovernanceWire_AttachmentLabelsAreVerbatim(t *testing.T) {
	t.Parallel()
	result := governanceWireBuild(t, "both")

	credentials := governanceAttachment(t, result, governanceSlotAttachment)
	// Author order, not alphabetical: the entity declares the reserved family
	// label first and its own label second, and "feature.aggrega.ai/kind" sorts
	// after "governance.example/rotation", so an alphabetising carry would
	// return the reversed sequence here.
	gotOrder := credentials.Labels.Keys()
	wantOrder := []string{"feature.aggrega.ai/kind", governanceAuthorLabel}
	if len(gotOrder) != len(wantOrder) {
		t.Fatalf("%s labels: got %v, want exactly %v", governanceSlotAttachment, gotOrder, wantOrder)
	}
	for i := range wantOrder {
		if gotOrder[i] != wantOrder[i] {
			t.Errorf("%s label order: got %v, want %v (author order, not sorted)", governanceSlotAttachment, gotOrder, wantOrder)
			break
		}
	}
	if v, _ := credentials.Labels.Get("feature.aggrega.ai/kind"); v != "secret" {
		t.Errorf("%s: feature.aggrega.ai/kind = %q, want %q", governanceSlotAttachment, v, "secret")
	}
	if v, _ := credentials.Labels.Get(governanceAuthorLabel); v != "quarterly" {
		t.Errorf("%s: %s = %q, want %q — a label the compiler has no reason to know must survive verbatim",
			governanceSlotAttachment, governanceAuthorLabel, v, "quarterly")
	}

	// The paired sibling: a different Feature carries ITS labels, not the
	// credential Feature's. A carry that stamped one label set onto every
	// attachment would pass the assertions above and fail here.
	metrics := governanceAttachment(t, result, governanceFreeAttachment)
	if v, ok := metrics.Labels.Get("feature.aggrega.ai/kind"); ok {
		t.Errorf("%s: feature.aggrega.ai/kind = %q, want it absent — its Feature declares no family label", governanceFreeAttachment, v)
	}
	if v, _ := metrics.Labels.Get(governanceAuthorLabel); v != "never" {
		t.Errorf("%s: %s = %q, want %q", governanceFreeAttachment, governanceAuthorLabel, v, "never")
	}
	if v, _ := metrics.Labels.Get("governance.example/tier"); v != "observability" {
		t.Errorf("%s: governance.example/tier = %q, want %q", governanceFreeAttachment, v, "observability")
	}
}

// TestGovernanceWire_BindingNamesTheDeclaredSlot proves the declaration-origin
// marker distinguishes an attachment that fills a declared slot from one that
// fills none, and that it carries the slot's name rather than a bare flag.
func TestGovernanceWire_BindingNamesTheDeclaredSlot(t *testing.T) {
	t.Parallel()
	result := governanceWireBuild(t, "both")

	if got := governanceAttachment(t, result, governanceSlotAttachment).Binding; got != governanceSlotAttachment {
		t.Errorf("%s binding = %q, want the declared slot name %q", governanceSlotAttachment, got, governanceSlotAttachment)
	}
	if got := governanceAttachment(t, result, governanceFreeAttachment).Binding; got != types.AttachmentBindingFree {
		t.Errorf("%s binding = %q, want %q — it fills no declared slot", governanceFreeAttachment, got, types.AttachmentBindingFree)
	}
}

// TestGovernanceWire_SensitivityAccompaniesTheFacts proves the derived
// sensitivity axis is on the same wire as the labels and the binding, so a
// policy reads all three from one payload. The derivation itself is not
// re-tested here; what is proven is that it arrives, and that it discriminates
// between the marked and unmarked delivery key.
func TestGovernanceWire_SensitivityAccompaniesTheFacts(t *testing.T) {
	t.Parallel()
	result := governanceWireBuild(t, "both")

	credentials := governanceAttachment(t, result, governanceSlotAttachment)
	if !credentials.Sensitive {
		t.Errorf("%s: sensitive = false, want true (its Feature declares sensitive leaves)", governanceSlotAttachment)
	}
	env, ok := credentials.SensitiveInject.Get("env")
	if !ok {
		t.Fatalf("%s: no marked env delivery keys; sensitive_inject = %v", governanceSlotAttachment, credentials.SensitiveInject.Keys())
	}
	if from, ok := env.Get("DATABASE_PASSWORD"); !ok || from != "password" {
		t.Errorf("%s: marked env key DATABASE_PASSWORD -> %q (present %v), want %q", governanceSlotAttachment, from, ok, "password")
	}

	// The third unit: the authored leaf. The attachment authors a value at
	// literalPassword, which its Feature declares sensitive, and authors one at
	// secretName, which it does not. Both are asserted, because a derivation
	// that marked every authored leaf would satisfy the first assertion alone.
	if got := credentials.SensitivePayloadLeaves; len(got) != 1 || got[0] != "values.literalPassword" {
		t.Errorf("%s: sensitive_payload_leaves = %v, want exactly [values.literalPassword]", governanceSlotAttachment, got)
	}

	// The paired sibling: the free attachment's Feature declares nothing
	// sensitive, so it carries none of the three facts.
	metrics := governanceAttachment(t, result, governanceFreeAttachment)
	if metrics.Sensitive {
		t.Errorf("%s: sensitive = true, want false (its Feature declares no sensitive leaf)", governanceFreeAttachment)
	}
	if metrics.SensitiveInject.Len() != 0 {
		t.Errorf("%s: sensitive_inject = %v, want empty", governanceFreeAttachment, metrics.SensitiveInject.Keys())
	}
	if len(metrics.SensitivePayloadLeaves) != 0 {
		t.Errorf("%s: sensitive_payload_leaves = %v, want empty (its Feature declares no sensitive leaf)",
			governanceFreeAttachment, metrics.SensitivePayloadLeaves)
	}
}

// TestGovernanceWire_AuthoredLeafMarkingIsOmittedWhenNothingIsMarked proves the
// per-leaf marking is absent from the WIRE BYTES, not merely empty in the Go
// value, for an attachment that authors no declared-sensitive leaf.
//
// The assertion is on the marshalled JSON rather than on the field, because the
// two can disagree: a field that is empty in Go still moves every committed
// cross-language vector if the encoder emits it unconditionally. The key's
// absence is what keeps an attachment whose Feature and slot declare nothing
// carrying the bytes it carried before this axis existed.
//
// The positive half is asserted on the same bytes in the same run, so the check
// cannot pass by the key being unreachable in both directions.
func TestGovernanceWire_AuthoredLeafMarkingIsOmittedWhenNothingIsMarked(t *testing.T) {
	t.Parallel()
	const wireKey = `"sensitive_payload_leaves"`

	marked := governanceAttachment(t, governanceWireBuild(t, "both"), governanceSlotAttachment)
	markedBytes, err := json.Marshal(marked)
	if err != nil {
		t.Fatalf("marshal the marked attachment: %v", err)
	}
	if !strings.Contains(string(markedBytes), wireKey) {
		t.Fatalf("the attachment that authors a declared-sensitive leaf does not carry %s on the wire, so the "+
			"absence asserted below would prove nothing:\n%s", wireKey, markedBytes)
	}

	unmarked := governanceAttachment(t, governanceWireBuild(t, "no-sensitive-datum"), governanceSlotAttachment)
	unmarkedBytes, err := json.Marshal(unmarked)
	if err != nil {
		t.Fatalf("marshal the unmarked attachment: %v", err)
	}
	if strings.Contains(string(unmarkedBytes), wireKey) {
		t.Errorf("an attachment that authors no declared-sensitive leaf still carries %s on the wire; the key "+
			"must be omitted entirely so an attachment declaring nothing carries unchanged bytes:\n%s",
			wireKey, unmarkedBytes)
	}
	// The attachment is still sensitive at the roll-up here, so the omission
	// above is the per-leaf answer being empty and not the whole axis dropping
	// out of the payload.
	if !unmarked.Sensitive {
		t.Errorf("the no-sensitive-datum variant also cleared the per-attachment roll-up, so the omission above "+
			"is not attributable to the per-leaf join: sensitive = %v, want true", unmarked.Sensitive)
	}
}

// TestGovernanceWire_ControlVariantsMoveOneVariableEach proves each control
// fixture differs from the baseline in exactly the fact its paired policy
// judges, so a policy's halt-then-pass comparison isolates one cause. Without
// this, a control that changed two facts would let a passing build be
// attributed to the wrong one.
func TestGovernanceWire_ControlVariantsMoveOneVariableEach(t *testing.T) {
	t.Parallel()

	// The free-attachment control: no attachment is free, and the marked
	// delivery key the other policy judges is untouched.
	noFree := governanceWireBuild(t, "no-free")
	attachments, ok := noFree.IRGraph.Spec.FeatureValuesMapping.Get(governanceWireNode)
	if !ok {
		t.Fatalf("no-free: node %q carries no attachment map", governanceWireNode)
	}
	for name, entry := range attachments.All() {
		if entry.Binding == types.AttachmentBindingFree {
			t.Errorf("no-free control still carries a free attachment %q", name)
		}
	}
	credentials := governanceAttachment(t, noFree, governanceSlotAttachment)
	if credentials.SensitiveInject.Len() == 0 {
		t.Errorf("no-free control lost the marked delivery key; it must move only the free-attachment variable")
	}

	// The delivery-route variant: no delivery key is marked, the authored leaf
	// the other route reads is untouched, and the free attachment a third
	// policy judges is still present.
	noMarkedDelivery := governanceWireBuild(t, "no-marked-delivery")
	noMarkedDeliveryCredentials := governanceAttachment(t, noMarkedDelivery, governanceSlotAttachment)
	if got := noMarkedDeliveryCredentials.SensitiveInject.Len(); got != 0 {
		t.Errorf("no-marked-delivery variant still marks %d delivery mechanism(s); it must mark none", got)
	}
	if got := noMarkedDeliveryCredentials.SensitivePayloadLeaves; len(got) != 1 || got[0] != "values.literalPassword" {
		t.Errorf("no-marked-delivery variant's authored-leaf marking = %v, want exactly [values.literalPassword]; "+
			"it must move only the delivery variable, and the authored leaf is what makes the two routes separable", got)
	}
	if got := governanceAttachment(t, noMarkedDelivery, governanceFreeAttachment).Binding; got != types.AttachmentBindingFree {
		t.Errorf("no-marked-delivery variant lost the free attachment (binding %q); it must move only the delivery variable", got)
	}
	// The attachment stays sensitive: the Feature's declared leaves are
	// unchanged, and only the DELIVERY of sensitive material moved. A control
	// that also cleared the roll-up would be testing two facts at once.
	if !noMarkedDeliveryCredentials.Sensitive {
		t.Errorf("no-marked-delivery variant also cleared the per-attachment roll-up; it must move only the delivery variable")
	}

	// The per-datum control: neither route carries a declared-sensitive datum,
	// so a policy halting on either must pass. Exactly one variable separates it
	// from the variant above — the absence of a value at the declared-sensitive
	// leaf — and the roll-up stays true, which is what makes a policy keyed on
	// the roll-up instead of on the per-datum marking visible here.
	noSensitiveDatum := governanceWireBuild(t, "no-sensitive-datum")
	noSensitiveDatumCredentials := governanceAttachment(t, noSensitiveDatum, governanceSlotAttachment)
	if got := noSensitiveDatumCredentials.SensitiveInject.Len(); got != 0 {
		t.Errorf("no-sensitive-datum control marks %d delivery mechanism(s); it must mark none", got)
	}
	if got := noSensitiveDatumCredentials.SensitivePayloadLeaves; len(got) != 0 {
		t.Errorf("no-sensitive-datum control still marks authored leaves %v; it must mark none", got)
	}
	if !noSensitiveDatumCredentials.Sensitive {
		t.Errorf("no-sensitive-datum control cleared the per-attachment roll-up; the roll-up reads the Feature's " +
			"declarations, which this control does not touch, so clearing it would move a second variable")
	}
	if got := governanceAttachment(t, noSensitiveDatum, governanceFreeAttachment).Binding; got != types.AttachmentBindingFree {
		t.Errorf("no-sensitive-datum control lost the free attachment (binding %q); it must move only the authored-leaf variable", got)
	}
}

// TestGovernanceWire_PayloadDump records the real per-attachment payload the
// governance socket carries for this fixture. It asserts only that the dump is
// non-empty and names both attachments; its value is the logged bytes, which
// are the marshalled wire rather than a hand-written illustration of it.
func TestGovernanceWire_PayloadDump(t *testing.T) {
	t.Parallel()
	result := governanceWireBuild(t, "both")
	attachments, ok := result.IRGraph.Spec.FeatureValuesMapping.Get(governanceWireNode)
	if !ok {
		t.Fatalf("node %q carries no attachment map", governanceWireNode)
	}
	raw, err := json.MarshalIndent(attachments, "", "  ")
	if err != nil {
		t.Fatalf("marshal attachment map: %v", err)
	}
	t.Logf("governance wire, node %q attachments:\n%s", governanceWireNode, raw)
	if attachments.Len() != 2 {
		t.Errorf("expected both attachments on the wire, got %v", attachments.Keys())
	}
}
