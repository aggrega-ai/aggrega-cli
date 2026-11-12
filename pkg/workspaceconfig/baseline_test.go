// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package workspaceconfig

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// TestDefaultBaselineMatchesCanonicalJSON proves DefaultBaseline is derived from
// the canonical baseline.json rather than a hand-maintained Go list: it reads the
// canonical document, decodes it independently of the package's own parse, and
// asserts set-equality against DefaultBaseline's identities, deprecation
// identities, and signature requirement. A drift between the served baseline and
// the offline baseline (the bug this guards) fails here.
func TestDefaultBaselineMatchesCanonicalJSON(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("baseline.json")
	if err != nil {
		t.Fatalf("read canonical baseline.json: %v", err)
	}
	var doc struct {
		RequiresSignatures bool `json:"requires_signatures"`
		TrustedIdentities  []struct {
			Identity       string `json:"identity"`
			IdentityRegexp string `json:"identity_regexp"`
			Issuer         string `json:"issuer"`
		} `json:"trusted_identities"`
		TrustedDeprecationIdentities []struct {
			Identity       string `json:"identity"`
			IdentityRegexp string `json:"identity_regexp"`
			Issuer         string `json:"issuer"`
		} `json:"trusted_deprecation_identities"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decode canonical baseline.json: %v", err)
	}

	want := make([]TrustedIdentity, 0, len(doc.TrustedIdentities))
	for _, id := range doc.TrustedIdentities {
		want = append(want, TrustedIdentity{Identity: id.Identity, IdentityRegexp: id.IdentityRegexp, Issuer: id.Issuer})
	}
	wantDep := make([]TrustedIdentity, 0, len(doc.TrustedDeprecationIdentities))
	for _, id := range doc.TrustedDeprecationIdentities {
		wantDep = append(wantDep, TrustedIdentity{Identity: id.Identity, IdentityRegexp: id.IdentityRegexp, Issuer: id.Issuer})
	}

	got := DefaultBaseline()

	if got.Host != HubBaselineHost {
		t.Errorf("baseline host = %q; want %q", got.Host, HubBaselineHost)
	}
	if got.Signing.Required != doc.RequiresSignatures {
		t.Errorf("signing.required = %v; want %v (from requires_signatures)", got.Signing.Required, doc.RequiresSignatures)
	}
	if !identitySetEqual(got.TrustedIdentities, want) {
		t.Errorf("trusted identities = %+v; want the canonical set %+v", got.TrustedIdentities, want)
	}
	if !identitySetEqual(got.TrustedDeprecationIdentities, wantDep) {
		t.Errorf("trusted deprecation identities = %+v; want the canonical set %+v", got.TrustedDeprecationIdentities, wantDep)
	}
}

// TestDefaultBaselineReturnsFreshCopy proves DefaultBaseline hands each caller an
// independently mutable copy: mutating one result never disturbs a second.
func TestDefaultBaselineReturnsFreshCopy(t *testing.T) {
	t.Parallel()

	a := DefaultBaseline()
	if len(a.TrustedIdentities) == 0 {
		t.Fatal("baseline carries no trusted identities")
	}
	a.TrustedIdentities[0].Identity = "mutated@example.com"
	a.TrustedIdentities = append(a.TrustedIdentities, TrustedIdentity{Identity: "extra@example.com"})

	b := DefaultBaseline()
	if b.TrustedIdentities[0].Identity == "mutated@example.com" {
		t.Error("mutating one DefaultBaseline result leaked into a later call; the copy is not fresh")
	}
	if len(b.TrustedIdentities) == len(a.TrustedIdentities) {
		t.Error("appending to one result changed a later call's length; the slice is shared")
	}
}

// identitySetEqual reports whether two identity slices carry the same entries
// regardless of order — a set comparison, so the assertion does not pin a
// particular list order.
func identitySetEqual(a, b []TrustedIdentity) bool {
	if len(a) != len(b) {
		return false
	}
	used := make([]bool, len(b))
	for _, x := range a {
		matched := false
		for i, y := range b {
			if !used[i] && reflect.DeepEqual(x, y) {
				used[i] = true
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}
