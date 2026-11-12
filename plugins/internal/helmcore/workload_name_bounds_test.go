// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package helmcore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/labels"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The two names this target derives from a component identity only for the
// workload the node renders — the CronJob a scheduled workload emits, and each
// Service named after the identity and a Service key — are judged here through
// the shipped provider's own answer, and every refusal through the compiler's real
// settle path, so what is asserted is what an author meets.
//
// Every length a row sits at is derived through the name the synthesizer emits
// (synthesizedNameFor, serviceNameFor) rather than added up here, so a change to
// how a name is joined moves the fixture with it instead of leaving an arithmetic
// copy agreeing with the old join.

// workloadNameOracle answers the name-bound question with this provider's own
// answer, so the settle path is driven against what ships.
type workloadNameOracle struct{}

func (workloadNameOracle) NameBound(_ context.Context, in renderer.NameBoundInput) (renderer.NameBoundOutput, error) {
	return NameBoundOutput(in), nil
}

// workloadFormAndKeys is a resolved workload stating form and declaring one
// Service per key, in the order given.
func workloadFormAndKeys(form string, serviceKeys ...string) *types.Workload {
	w := &types.Workload{Form: form}
	for _, key := range serviceKeys {
		w.Services.Set(key, types.ServiceSpec{})
	}
	return w
}

// emittedServiceName is the name the synthesizer gives the Service a key names,
// for an identity in the given state.
func emittedServiceName(identity string, composed bool, key string) string {
	return serviceNameFor(synthesizedNameFor(identity, composed), key)
}

// keyOfLength is a key of n letters.
func keyOfLength(n int) string { return strings.Repeat("k", n) }

// longestFittingKey is the length of the longest key whose Service, named after
// identity, the cluster still accepts — found by measuring the emitted name.
func longestFittingKey(t *testing.T, identity string, composed bool) int {
	t.Helper()
	best := 0
	for n := 1; n <= 2*serviceNameMaxLength; n++ {
		if len(emittedServiceName(identity, composed, keyOfLength(n))) <= serviceNameMaxLength {
			best = n
		}
	}
	if best == 0 {
		t.Fatalf("fixture: no key fits beside %q", identity)
	}
	return best
}

// TestCronJobNameAuthored_BuildsAt52AndHaltsAt53NamingTheField is both sides of
// the wall for a name a person wrote, plus the controls that keep the wall where
// the workload puts it.
//
// The CronJob is named after the component's release name; an all-lowercase
// identity is spelled as it stands there, which the fixture checks rather than
// assumes. Fifty-two builds; fifty-three halts — and fifty-three is still a release
// name Helm accepts, so only the CronJob bound can be what halts it. The same
// identity on any other form, or on a node whose question carries no workload,
// builds exactly as it did before the bound existed, because no CronJob is
// emitted for it.
func TestCronJobNameAuthored_BuildsAt52AndHaltsAt53NamingTheField(t *testing.T) {
	t.Parallel()

	at52 := strings.Repeat("r", 52)
	at53 := strings.Repeat("r", 53)
	if len(synthesizedNameFor(at52, false)) != 52 || len(synthesizedNameFor(at53, false)) != 53 {
		t.Fatalf("fixture: an all-lowercase identity must name its CronJob at its own length")
	}

	cases := []struct {
		scenario string
		identity string
		workload *types.Workload
		halts    bool
	}{
		{scenario: "a scheduled workload's CronJob of 52 builds", identity: at52, workload: workloadFormAndKeys(rendercore.WorkloadFormScheduled)},
		{scenario: "a scheduled workload's CronJob of 53 halts", identity: at53, workload: workloadFormAndKeys(rendercore.WorkloadFormScheduled), halts: true},
		{scenario: "control: a stateless workload emits no CronJob", identity: at53, workload: workloadFormAndKeys(rendercore.WorkloadFormStateless)},
		{scenario: "control: a workload stating no form emits no CronJob", identity: at53, workload: workloadFormAndKeys("")},
		{scenario: "control: a question carrying no workload", identity: at53},
	}
	for _, tc := range cases {
		t.Run(tc.scenario, func(t *testing.T) {
			t.Parallel()

			field := "spec.components." + tc.identity
			err := renderer.SettleAuthoredComponentIdentity(context.Background(), workloadNameOracle{}, tc.identity, field, tc.workload)
			if !tc.halts {
				if err != nil {
					t.Fatalf("an identity of %d must build here: %v", len(tc.identity), err)
				}
				return
			}
			if err == nil {
				t.Fatalf("a CronJob named after an identity of %d must halt the build", len(tc.identity))
			}
			if !errors.Is(err, renderer.ErrComponentIdentityTargetRefuses()) {
				t.Fatalf("the halt must carry the target-refuses condition; got %v", err)
			}
			for _, want := range []string{
				"written at " + field, // the field the author wrote
				"which the target cannot use as a " + CronJobNameKind,                   // the kind that refused
				"a CronJob name is at most 52 characters",                               // the target's own reason
				fmt.Sprintf("would be %d", len(synthesizedNameFor(tc.identity, false))), // the length the author cannot otherwise see
				"choose a name of at most 52 characters",                                // the measured budget
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the halt does not carry %q; got %v", want, err)
				}
			}
		})
	}
}

// TestCronJobNameComposed_BoundIsTheShorteningInstruction pins the composed half:
// a composed identity is never refused for its length, it comes back with a bound
// the compiler shortens to.
//
// A composed identity names an attachment's resources as it stands, so the CronJob
// is the identity's own length: fifty-two is usable and fifty-three comes back
// bounded to fifty-two. The verdict is asserted directly because on this target
// the release-name bound, which a composed identity's joins are escaped for, binds
// a composed identity more tightly first, so the settle loop's answer would be the
// release name's rather than the CronJob's.
func TestCronJobNameComposed_BoundIsTheShorteningInstruction(t *testing.T) {
	t.Parallel()

	scheduled := rendercore.NameBoundInput{WorkloadForm: rendercore.WorkloadFormScheduled}

	scheduled.Proposed = strings.Repeat("a", 52)
	v, derived := CronJobNameVerdict(scheduled)
	if !derived || !v.Usable {
		t.Fatalf("a composed identity of 52 names a CronJob the cluster accepts; derived=%v usable=%v reason=%q",
			derived, v.Usable, v.Reason)
	}

	scheduled.Proposed = strings.Repeat("a", 53)
	v, derived = CronJobNameVerdict(scheduled)
	if !derived || v.Usable {
		t.Fatalf("a composed identity of 53 names a CronJob the cluster refuses; derived=%v usable=%v", derived, v.Usable)
	}
	if v.Bound == nil || *v.Bound != 52 {
		t.Fatalf("the refusal must carry the bound the compiler shortens to, 52; got %v", v.Bound)
	}
	if v.Kind != CronJobNameKind {
		t.Errorf("kind = %q, want %q", v.Kind, CronJobNameKind)
	}

	for _, form := range []string{"", rendercore.WorkloadFormStateless, rendercore.WorkloadFormTask, rendercore.WorkloadFormReference} {
		if _, derived := CronJobNameVerdict(rendercore.NameBoundInput{
			Proposed: strings.Repeat("a", 60), WorkloadForm: form,
		}); derived {
			t.Errorf("form %q emits no CronJob, so no CronJob verdict may be reported", form)
		}
	}
}

// TestServiceNameLengthAuthored_BuildsAt63AndHaltsAt64NamingTheKey is the wall
// for a Service named after an identity a person wrote and a key the Recipe's
// author wrote.
//
// The identity is three letters and the key makes up the rest; the key on the
// wall is the longest whose emitted Service name the cluster accepts, and the key
// past it one letter longer. The halt is one the developer can see only through the
// reason: it names the key and where the workload declares it, beside the
// developer's own field. A key long enough to leave no room at all offers no
// budget, since no identity of any length fits beside it, and a reference workload
// names its one Service without a key, so a long key bounds nothing there.
func TestServiceNameLengthAuthored_BuildsAt63AndHaltsAt64NamingTheKey(t *testing.T) {
	t.Parallel()

	const identity = "web"
	fits := longestFittingKey(t, identity, false)
	onTheWall, pastTheWall := keyOfLength(fits), keyOfLength(fits+1)
	if n := len(emittedServiceName(identity, false, onTheWall)); n != serviceNameMaxLength {
		t.Fatalf("fixture: the key on the wall names a Service of %d, want %d", n, serviceNameMaxLength)
	}
	// The budget is the longest prefix of the identity whose Service still fits
	// beside the key past the wall, measured through the emitted name.
	budget := 0
	for i := 1; i <= len(identity); i++ {
		if len(emittedServiceName(identity[:i], false, pastTheWall)) <= serviceNameMaxLength {
			budget = i
		}
	}
	if budget == 0 {
		t.Fatal("fixture: the key past the wall must still leave room for part of the identity")
	}
	// The smallest key beside which not even the identity's first character fits.
	noRoom := keyOfLength(longestFittingKey(t, identity[:1], false) + 1)

	cases := []struct {
		scenario string
		workload *types.Workload
		halts    bool
		want     []string
		notWant  string
	}{
		{scenario: "a Service name on the wall builds", workload: workloadFormAndKeys("", "default", onTheWall)},
		{
			scenario: "a Service name past the wall halts naming the key",
			workload: workloadFormAndKeys("", "default", pastTheWall),
			halts:    true,
			want: []string{
				"which the target cannot use as a " + ServiceNameKind,
				"a Kubernetes Service name is at most 63 characters",
				"which the workload declares at spec.workload.services." + pastTheWall,
				fmt.Sprintf("would be %d", len(emittedServiceName(identity, false, pastTheWall))),
				fmt.Sprintf("choose a name of at most %d characters", budget),
			},
		},
		{
			scenario: "a key leaving no room offers no budget",
			workload: workloadFormAndKeys("", noRoom),
			halts:    true,
			want: []string{
				"spec.workload.services." + noRoom,
				"it is the key that has to be shorter",
				"choose a name the target accepts",
			},
			notWant: "choose a name of at most 0 characters",
		},
		{scenario: "control: a reference workload's Service carries no key", workload: workloadFormAndKeys(rendercore.WorkloadFormReference, pastTheWall)},
		{scenario: "control: a question carrying no workload", workload: nil},
	}
	for _, tc := range cases {
		t.Run(tc.scenario, func(t *testing.T) {
			t.Parallel()

			field := "spec.components." + identity
			err := renderer.SettleAuthoredComponentIdentity(context.Background(), workloadNameOracle{}, identity, field, tc.workload)
			if !tc.halts {
				if err != nil {
					t.Fatalf("this must build: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("a Service name past the cluster's limit must halt the build")
			}
			if !errors.Is(err, renderer.ErrComponentIdentityTargetRefuses()) {
				t.Fatalf("the halt must carry the target-refuses condition; got %v", err)
			}
			for _, want := range append([]string{"written at " + field}, tc.want...) {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the halt does not carry %q; got %v", want, err)
				}
			}
			if tc.notWant != "" && strings.Contains(err.Error(), tc.notWant) {
				t.Errorf("the halt carries %q; got %v", tc.notWant, err)
			}
		})
	}
}

// TestServiceNameLengthComposed_ShortensTheIdentityToFit is the composed half: an
// attachment's identity is shortened until every Service it names fits, and never
// refused for a length a shorter identity reaches.
//
// The keys are found by measuring the emitted name against the composition itself,
// so the rows sit on the wall whatever the composition's spelling: the key on the
// wall must leave the identity as composed, and a key leaving room for thirty
// characters of identity must shorten it. A shortened composition keeps the last
// part whole up to a fixed run and appends a digest, so the shortest identity it
// can produce depends on the parts; the parts here are a long host and a short
// instance, and the fixture MEASURES that a composition bounded to thirty is no
// longer than thirty, so the row exercises shortening rather than that floor. The
// control asks the same parts with no workload and must get the composition back
// unchanged, which is what makes the shortening attributable to the key rather
// than to another kind.
func TestServiceNameLengthComposed_ShortensTheIdentityToFit(t *testing.T) {
	t.Parallel()

	scope := labels.AttachmentScope
	parts := []string{strings.Repeat("h", 20), "inst"}
	composed := labels.ComposeComponentIdentity(scope, labels.Unbounded, parts...)
	settle := func(w *types.Workload) (string, error) {
		return renderer.SettleComposedComponentIdentity(context.Background(), workloadNameOracle{}, scope, "helm", w, parts...)
	}
	const room = 30
	if len(composed) <= room {
		t.Fatalf("fixture: the composition %q is %d characters; the row past the wall needs more than %d", composed, len(composed), room)
	}
	if shortest := labels.ComposeComponentIdentity(scope, room, parts...); len(shortest) > room {
		t.Fatalf("fixture: composed against a bound of %d these parts give %q (%d characters), so the bound is below what "+
			"the composition can reach and the row would exercise that floor instead of shortening", room, shortest, len(shortest))
	}

	if got, err := settle(nil); err != nil || got != composed {
		t.Fatalf("control: with no workload the composition %q must settle unchanged; got %q, %v", composed, got, err)
	}

	onTheWall := keyOfLength(longestFittingKey(t, composed, true))
	if got, err := settle(workloadFormAndKeys("", onTheWall)); err != nil || got != composed {
		t.Errorf("a Service name on the wall must leave the identity as composed; got %q, %v", got, err)
	}

	// The key that leaves exactly room characters for an identity.
	pastTheWall := keyOfLength(longestFittingKey(t, strings.Repeat("a", room), true))
	got, err := settle(workloadFormAndKeys("", pastTheWall))
	if err != nil {
		t.Fatalf("a composed identity is shortened, never refused, where a shorter one fits: %v", err)
	}
	if got == composed {
		t.Fatalf("the identity %q was not shortened, so its Service name is %d characters",
			got, len(emittedServiceName(got, true, pastTheWall)))
	}
	if n := len(emittedServiceName(got, true, pastTheWall)); n > serviceNameMaxLength {
		t.Errorf("settled on %q, whose Service name is %d characters", got, n)
	}
	for _, v := range NameBoundOutput(rendercore.NameBoundInput{Proposed: got, ServiceKeys: []string{pastTheWall}}).Names {
		if !v.Usable {
			t.Errorf("the settled identity %q is still refused as a %s: %s", got, v.Kind, v.Reason)
		}
	}

	// A key that leaves no room is the target's answer rather than a guest defect,
	// and the halt names the key the Recipe's author has to shorten.
	noRoom := keyOfLength(longestFittingKey(t, composed[:1], true) + 1)
	_, err = settle(workloadFormAndKeys("", noRoom))
	if !errors.Is(err, renderer.ErrComponentIdentityTargetRefuses()) {
		t.Fatalf("a key leaving no room must halt as the target refusing; got %v", err)
	}
	if errors.Is(err, renderer.ErrNameBoundNotConverging()) {
		t.Errorf("a key leaving no room is a plain answer, not a guest defect: %v", err)
	}
	if !strings.Contains(err.Error(), "spec.workload.services."+noRoom) {
		t.Errorf("the halt must name the key and where it is declared; got %v", err)
	}
}

// TestNameBoundOutput_WithoutWorkloadFactsIsTheAnswerItWas pins the byte-identical
// half: a question carrying neither workload fact gets the same three verdicts, in
// the same order, that the provider gave before either fact existed, so a node
// whose names are all within their bounds renders as it did.
func TestNameBoundOutput_WithoutWorkloadFactsIsTheAnswerItWas(t *testing.T) {
	t.Parallel()

	for _, in := range []rendercore.NameBoundInput{
		{Proposed: "web", Authored: true},
		{Proposed: strings.Repeat("r", 53), Authored: true},
		{Proposed: "cfr--host--instance"},
	} {
		var kinds []string
		for _, v := range NameBoundOutput(in).Names {
			kinds = append(kinds, v.Kind)
		}
		want := []string{ReleaseNameKind, ServiceNameKind, rendercore.ComponentIDLabelKind}
		if strings.Join(kinds, "|") != strings.Join(want, "|") {
			t.Errorf("proposed %q with no workload facts: kinds %v, want %v", in.Proposed, kinds, want)
		}
	}
}
