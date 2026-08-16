// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// slotCatalog returns a lookup whose one Component declares two attachment
// slots, so a near miss has something to be near.
func slotCatalog() CatalogLookup {
	return CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			if typeURI != "core/datastore/db" {
				return types.Component{}, false
			}
			slots := types.OrderedMap[types.AttachmentSlot]{}
			slots.Set("db_password", types.AttachmentSlot{Inject: []string{"env"}})
			slots.Set("app_user_password", types.AttachmentSlot{Required: true})
			return types.Component{
				Metadata: types.ComponentMetadata{Name: "db", Version: "1.0.0"},
				Spec:     types.ComponentSpec{FeaturesSchema: slots},
			}, true
		},
	}
}

func aggWithAttachments(t *testing.T, featureNames, credentialNames []string) types.NormalizedAggregate {
	t.Helper()
	features := types.OrderedMap[types.FeatureInstance]{}
	for _, n := range featureNames {
		features.Set(n, types.FeatureInstance{Type: "core/some-feature"})
	}
	// A credential is declared on the same surface; the lint reads one block.
	for _, n := range credentialNames {
		features.Set(n, types.FeatureInstance{Type: "core/some-credential"})
	}
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("db", types.ComponentInstance{
		Type:     "core/datastore/db",
		Features: features,
	})
	return normalized(t, &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec:     types.AggregateSpec{Components: components},
	})
}

func TestLintAttachmentSlotNearMiss_TypoReported(t *testing.T) {
	// "db_passwrod" is one transposition from the declared "db_password".
	got := LintAttachmentSlotNearMiss(aggWithAttachments(t, nil, []string{"db_passwrod"}), slotCatalog())
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 advisory; got %d: %v", len(got), got)
	}
	if !errors.Is(got[0], ErrAttachmentSlotNearMiss()) {
		t.Errorf("expected ErrAttachmentSlotNearMiss; got %v", got[0].Err)
	}
	// Both spellings must appear, or the author cannot see the pair.
	for _, want := range []string{"db_passwrod", "db_password"} {
		if !strings.Contains(got[0].Message, want) {
			t.Errorf("expected the advisory to name %q; got %q", want, got[0].Message)
		}
	}
	// The Field locator is what places the advisory on the authoring key.
	wantField := `.spec.components["db"].features["db_passwrod"]`
	if got[0].Field != wantField {
		t.Errorf("expected Field %q; got %q", wantField, got[0].Field)
	}
}

func TestLintAttachmentSlotNearMiss_DeclaredSlotSilent(t *testing.T) {
	// An attachment that FILLS a declared slot is the intended case.
	if got := LintAttachmentSlotNearMiss(aggWithAttachments(t, nil, []string{"db_password"}), slotCatalog()); len(got) != 0 {
		t.Fatalf("expected no advisory for an exact slot fill; got %v", got)
	}
}

func TestLintAttachmentSlotNearMiss_DistinctFreeAttachmentSilent(t *testing.T) {
	// A free attachment whose name resembles no slot is ordinary authoring:
	// the surface is open, so this must stay quiet or the lint becomes noise.
	if got := LintAttachmentSlotNearMiss(aggWithAttachments(t, []string{"monitoring"}, nil), slotCatalog()); len(got) != 0 {
		t.Fatalf("expected no advisory for an unrelated free attachment; got %v", got)
	}
}

func TestLintAttachmentSlotNearMiss_BothBlocksWalked(t *testing.T) {
	// A typo is the same mistake whichever authoring block it lands in, so
	// both are walked against the same slot declarations.
	got := LintAttachmentSlotNearMiss(aggWithAttachments(t, []string{"db_passwor"}, []string{"app_user_passwrod"}), slotCatalog())
	if len(got) != 2 {
		t.Fatalf("expected 2 advisories (one per block); got %d: %v", len(got), got)
	}
	var sawFeatures, sawSecrets bool
	for _, a := range got {
		if strings.Contains(a.Field, ".features[") {
			sawFeatures = true
		}
		if strings.Contains(a.Field, ".features[") {
			sawSecrets = true
		}
	}
	if !sawFeatures || !sawSecrets {
		t.Errorf("expected an advisory from each block; got %v", got)
	}
}

func TestLintAttachmentSlotNearMiss_NeverHalts(t *testing.T) {
	// The advisory class must not reach the guard verdict: RunGuards is the
	// halting set, and the lint is deliberately not registered in it.
	na := aggWithAttachments(t, nil, []string{"db_passwrod"})
	if errs, err := RunGuards(nil, na, nil, slotCatalog(), nil, nil); err != nil || len(errs) != 0 {
		t.Fatalf("the near-miss lint must not halt through the guard suite; got %v (err %v)", errs, err)
	}
	if got := LintAttachmentSlotNearMiss(na, slotCatalog()); len(got) != 1 {
		t.Fatalf("expected the advisory from the lint producer; got %v", got)
	}
}

func TestLintAttachmentSlotNearMiss_ShortNamesNotPaired(t *testing.T) {
	// Below the minimum length nearly every name is within one edit of every
	// other, so pairing them would produce noise rather than signal.
	slots := types.OrderedMap[types.AttachmentSlot]{}
	slots.Set("key", types.AttachmentSlot{})
	cat := CatalogLookup{Component: func(u string) (types.Component, bool) {
		return types.Component{Spec: types.ComponentSpec{FeaturesSchema: slots}}, true
	}}
	if got := LintAttachmentSlotNearMiss(aggWithAttachments(t, []string{"kez"}, nil), cat); len(got) != 0 {
		t.Fatalf("expected no advisory for sub-threshold names; got %v", got)
	}
}

func TestLintAttachmentSlotNearMiss_NoSlotsNoAdvisory(t *testing.T) {
	// An entity that declares no slots has no near miss to report; every
	// attachment on it is a free attachment by construction.
	cat := CatalogLookup{Component: func(u string) (types.Component, bool) {
		return types.Component{Metadata: types.ComponentMetadata{Name: "bare"}}, true
	}}
	if got := LintAttachmentSlotNearMiss(aggWithAttachments(t, []string{"db_passwrod"}, nil), cat); len(got) != 0 {
		t.Fatalf("expected no advisory when the type declares no slots; got %v", got)
	}
}

func TestLintAttachmentSlotNearMiss_CatalogLessSilent(t *testing.T) {
	if got := LintAttachmentSlotNearMiss(aggWithAttachments(t, nil, []string{"db_passwrod"}), CatalogLookup{}); got != nil {
		t.Fatalf("expected nil on a catalog-less path; got %v", got)
	}
}

func TestLintAttachmentSlotNearMiss_Deterministic(t *testing.T) {
	// Byte-identical advisory sequence across runs of identical input.
	na := aggWithAttachments(t, []string{"db_passwor"}, []string{"app_user_passwrod"})
	first := LintAttachmentSlotNearMiss(na, slotCatalog())
	for i := 0; i < 8; i++ {
		again := LintAttachmentSlotNearMiss(na, slotCatalog())
		if len(again) != len(first) {
			t.Fatalf("run %d: length %d != %d", i, len(again), len(first))
		}
		for j := range first {
			if again[j].Error() != first[j].Error() {
				t.Fatalf("run %d, advisory %d: %q != %q", i, j, again[j].Error(), first[j].Error())
			}
		}
	}
}

func TestEditDistanceWithin(t *testing.T) {
	cases := []struct {
		a, b string
		max  int
		want bool
	}{
		{"db_password", "db_password", 1, true},
		// A transposition is the commonest typo and must cost ONE edit, or the
		// lint misses the case it primarily exists to catch.
		{"db_passwrod", "db_password", 1, true},
		{"db_passwor", "db_password", 1, true},   // one deletion
		{"db_passwordd", "db_password", 1, true}, // one insertion
		{"db_pussword", "db_password", 1, true},  // one substitution
		{"api_key", "api_url", 1, false},         // two substitutions
		{"short", "muchlongername", 1, false},    // length gap alone rejects
		{"db_pasword", "db_passwrod", 1, false},  // two edits apart
	}
	for _, c := range cases {
		if got := editDistanceWithin(c.a, c.b, c.max); got != c.want {
			t.Errorf("editDistanceWithin(%q, %q, %d) = %v; want %v", c.a, c.b, c.max, got, c.want)
		}
	}
}
