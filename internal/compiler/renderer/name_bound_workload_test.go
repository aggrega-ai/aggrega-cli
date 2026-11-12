// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/labels"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// questionOracle is a stub provider engine that records every question it is
// asked, whole, so a test can read what the question carried and not only the
// proposal.
type questionOracle struct {
	answer func(NameBoundInput) DerivedNameVerdict
	seen   []NameBoundInput
}

func (o *questionOracle) NameBound(_ context.Context, in NameBoundInput) (NameBoundOutput, error) {
	o.seen = append(o.seen, in)
	return NameBoundOutput{Names: []DerivedNameVerdict{o.answer(in)}}, nil
}

// questionWorkload is a resolved workload stating form and declaring one Service
// per key, in the order given.
func questionWorkload(form string, serviceKeys ...string) *types.Workload {
	w := &types.Workload{Form: form}
	for _, key := range serviceKeys {
		w.Services.Set(key, types.ServiceSpec{})
	}
	return w
}

// TestNameBoundQuestion_CarriesTheResolvedWorkloadFacts asserts the question both
// settle paths put to a guest carries the node's workload form and its Service
// keys as the workload states them — the keys in the order it declares them — and
// that a question about a node with no workload is byte-for-byte the question a
// guest has always been asked.
//
// The composed path is driven through more than one pass, because a shortened
// proposal is judged against the same names as the first and a fact dropped after
// the first pass would let a later proposal be accepted for names it was never
// judged against.
func TestNameBoundQuestion_CarriesTheResolvedWorkloadFacts(t *testing.T) {
	t.Parallel()

	w := questionWorkload("scheduled", "web", "metrics", "admin")
	wantKeys := []string{"web", "metrics", "admin"}

	authored := &questionOracle{answer: func(NameBoundInput) DerivedNameVerdict {
		return DerivedNameVerdict{Kind: "release name", Usable: true}
	}}
	if err := SettleAuthoredComponentIdentity(context.Background(), authored, "api", "spec.components.api", w); err != nil {
		t.Fatalf("a usable identity must settle: %v", err)
	}
	if len(authored.seen) != 1 {
		t.Fatalf("the authored path asked %d times; it asks once", len(authored.seen))
	}
	if q := authored.seen[0]; q.Proposed != "api" || !q.Authored || q.WorkloadForm != "scheduled" || !slices.Equal(q.ServiceKeys, wantKeys) {
		t.Errorf("the authored question is %+v; want proposed api, authored, form scheduled, keys %v", q, wantKeys)
	}

	const bound = 40
	composed := &questionOracle{answer: func(in NameBoundInput) DerivedNameVerdict {
		if len(in.Proposed) > bound {
			return DerivedNameVerdict{Kind: "Service name", Usable: false, Bound: boundTo(bound), Reason: "too long"}
		}
		return DerivedNameVerdict{Kind: "Service name", Usable: true}
	}}
	if _, err := SettleComposedComponentIdentity(context.Background(), composed, labels.AttachmentScope, "stub", w,
		strings.Repeat("host", 10), strings.Repeat("instance", 10)); err != nil {
		t.Fatalf("the exchange must converge: %v", err)
	}
	if len(composed.seen) < 2 {
		t.Fatalf("the composed path asked %d times; the fixture is built to need a second pass", len(composed.seen))
	}
	for i, q := range composed.seen {
		if q.Authored || q.WorkloadForm != "scheduled" || !slices.Equal(q.ServiceKeys, wantKeys) {
			t.Errorf("pass %d asked %+v; every pass carries form scheduled and keys %v, and is not authored", i+1, q, wantKeys)
		}
	}

	// No workload, and a workload stating neither fact, carry neither field, so the
	// encoded question is the one a guest reading neither has always received.
	legacy, err := NameBoundInput{Proposed: "api", Authored: true}.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode the question without workload facts: %v", err)
	}
	for _, tc := range []struct {
		scenario string
		workload *types.Workload
	}{
		{"no workload", nil},
		{"a workload stating no form and no Service", questionWorkload("")},
	} {
		got, err := nameBoundQuestion("api", true, tc.workload).EncodeCBOR()
		if err != nil {
			t.Fatalf("%s: encode: %v", tc.scenario, err)
		}
		if !bytes.Equal(got, legacy) {
			t.Errorf("%s: the encoded question is %x; a question carrying no workload facts is %x", tc.scenario, got, legacy)
		}
	}
}

// recordedQuestion is one name-bound question a provider was asked during a real
// build, with the capability of the provider that was asked.
type recordedQuestion struct {
	capability string
	in         NameBoundInput
}

// questionRecordingProvider is the four-site fixture's provider, answering the
// name-bound question by accepting every proposal and recording what it was
// asked. Accepting everything leaves the build exactly as it renders unobserved.
type questionRecordingProvider struct {
	*envOwnerProvider
	record func(recordedQuestion)
}

func (p questionRecordingProvider) NameBound(_ context.Context, in NameBoundInput) (NameBoundOutput, error) {
	p.record(recordedQuestion{capability: p.capability, in: in})
	return NameBoundOutput{}, nil
}

// questionSiteWorkload is a workload body stating form and declaring one Service
// per key, each Service exposing the one container port.
func questionSiteWorkload(form string, keys ...string) string {
	var b strings.Builder
	b.WriteString("apiVersion: aggrega.ai/v1beta1\nkind: Workload\n")
	b.WriteString("form: " + form + "\n")
	b.WriteString("containers:\n  main:\n    image: owner:latest\n    ports:\n      p: 8080\n")
	b.WriteString("services:\n")
	for _, key := range keys {
		b.WriteString("  " + key + ":\n    type: ClusterIP\n    ports:\n      p: 8080\n")
	}
	return b.String()
}

// TestNameBoundQuestion_IsAskedWithEachOwnersResolvedWorkload drives a real build
// over the four-site fixture and reads what each site's identity was asked with.
//
// Each workload-rendering site declares a form and Service keys of its own, so a
// fact read from the wrong owner — or from a workload not yet resolved — is a
// different answer rather than the same one by luck. The host component's
// identity, which its author wrote, is asked once, with its own workload's facts;
// the attachment rendering a workload is asked with that attachment's; the
// attachment rendering none is asked with neither. The boundary-scope attachment's
// identity is settled before any template is evaluated, because where its output
// is assembled may read it, so it is asked with neither too.
func TestNameBoundQuestion_IsAskedWithEachOwnersResolvedWorkload(t *testing.T) {
	t.Parallel()

	var (
		mu   sync.Mutex
		seen []recordedQuestion
	)
	factory := func(capability string) (Provider, error) {
		base, err := envOwnerFactory()(capability)
		if err != nil {
			return nil, err
		}
		return questionRecordingProvider{
			envOwnerProvider: base.(*envOwnerProvider),
			record: func(q recordedQuestion) {
				mu.Lock()
				seen = append(seen, q)
				mu.Unlock()
			},
		}, nil
	}

	base := envOwnerLookup()
	lookup := RecipeLookup{
		Recipe: func(instanceName, componentType string) (*types.Recipe, bool) {
			r, ok := base.Recipe(instanceName, componentType)
			if ok {
				r.Spec.Workload = types.WorkloadTemplate{Inline: questionSiteWorkload("stateful", "web", "metrics")}
			}
			return r, ok
		},
		FeatureRecipe: func(instanceName, featureType string) (*types.FeatureRecipe, bool) {
			fr, ok := base.FeatureRecipe(instanceName, featureType)
			if !ok {
				return nil, false
			}
			switch featureType {
			case ownerMonFeatureType:
				fr.Spec.Workload = types.WorkloadTemplate{Inline: questionSiteWorkload("daemon", "scrape")}
			case ownerBoundaryFeatureType:
				fr.Spec.Workload = types.WorkloadTemplate{Inline: questionSiteWorkload("stateful", "relay")}
			}
			return fr, true
		},
	}

	if _, err := RenderAll(context.Background(), ownerInput(), lookup, factory); err != nil {
		t.Fatalf("the fixture build did not complete, so nothing below is attributable to the question: %+v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	bySite := map[string][]NameBoundInput{}
	for _, q := range seen {
		bySite[q.capability] = append(bySite[q.capability], q.in)
	}

	for _, tc := range []struct {
		site     string
		authored bool
		form     string
		keys     []string
	}{
		{site: ownerHostCapability, authored: true, form: "stateful", keys: []string{"web", "metrics"}},
		{site: ownerMonCapability, form: "daemon", keys: []string{"scrape"}},
		{site: ownerLogsCapability},
		{site: ownerBoundaryCapability},
	} {
		asked := bySite[tc.site]
		if len(asked) == 0 {
			t.Errorf("%s: the identity was never asked about; a site never asked is indistinguishable "+
				"from one asked and accepted", tc.site)
			continue
		}
		if tc.authored && len(asked) != 1 {
			t.Errorf("%s: an authored identity is asked once; it was asked %d times", tc.site, len(asked))
		}
		for i, q := range asked {
			if q.Authored != tc.authored || q.WorkloadForm != tc.form || !slices.Equal(q.ServiceKeys, tc.keys) {
				t.Errorf("%s: question %d is %+v; want authored=%v form %q keys %v",
					tc.site, i+1, q, tc.authored, tc.form, tc.keys)
			}
		}
	}
	if q := bySite[ownerHostCapability]; len(q) > 0 && q[0].Proposed != ownerNode {
		t.Errorf("the host's question proposed %q; the authored identity is %q", q[0].Proposed, ownerNode)
	}
}
