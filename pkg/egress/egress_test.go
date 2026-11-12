// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package egress_test

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

func TestRoundTripperOnline(t *testing.T) {
	rt, err := egress.RoundTripper(workspaceconfig.Config{Offline: false})
	if err != nil {
		t.Fatalf("online RoundTripper: unexpected error %v", err)
	}
	if rt == nil {
		t.Fatal("online RoundTripper: nil transport")
	}
}

func TestRoundTripperOfflineRefuses(t *testing.T) {
	rt, err := egress.RoundTripper(workspaceconfig.Config{Offline: true})
	if !errors.Is(err, egress.ErrOfflineEgress()) {
		t.Fatalf("offline RoundTripper: err = %v; want ErrOfflineEgress", err)
	}
	if rt != nil {
		t.Fatalf("offline RoundTripper: transport = %v; want nil (refuse before dial)", rt)
	}
}

func TestHTTPClientOnline(t *testing.T) {
	c, err := egress.HTTPClient(workspaceconfig.Config{Offline: false})
	if err != nil {
		t.Fatalf("online HTTPClient: unexpected error %v", err)
	}
	if c == nil || c.Transport == nil {
		t.Fatal("online HTTPClient: nil client or transport")
	}
}

func TestHTTPClientOfflineRefuses(t *testing.T) {
	c, err := egress.HTTPClient(workspaceconfig.Config{Offline: true})
	if !errors.Is(err, egress.ErrOfflineEgress()) {
		t.Fatalf("offline HTTPClient: err = %v; want ErrOfflineEgress", err)
	}
	if c != nil {
		t.Fatalf("offline HTTPClient: client = %v; want nil", c)
	}
}

// The offline refusal carries its own remediation so callers that surface it
// need append none, and the source-resolution classifier will not double-append.
func TestErrOfflineEgressCarriesRemediation(t *testing.T) {
	if got := egress.ErrOfflineEgress().Error(); !contains(got, "remediation:") {
		t.Errorf("ErrOfflineEgress message %q lacks a remediation clause", got)
	}
}

// TestErrOfflineEgressNamesTheKeyTheLoaderReads holds the remediation's spelling
// of the configuration key against the constant that names it.
//
// The two are a deliberate duplicate: the generator that publishes this sentence
// reads it from the declaration's own source text and refuses a value naming a
// constant, so the key has to be written out. This is the comparison that
// duplication owes — and it is owed rather than optional, because the shipped
// remediation named a key the loader does not read, and nothing failed. A reader
// following it was sent to write a key the schema refuses, and refuses for the
// wrong reason.
func TestErrOfflineEgressNamesTheKeyTheLoaderReads(t *testing.T) {
	got := egress.ErrOfflineEgress().Error()
	if !contains(got, workspaceconfig.OfflineSettingKey) {
		t.Errorf("the remediation does not name the offline key at all: %q", got)
	}
	// The wrong spelling, which is the one that shipped. The posture is a
	// top-level setting; the `workspace:` block is the root marker and admits
	// only the catalog directories, so a key written inside it is refused.
	if contains(got, "workspace."+workspaceconfig.OfflineSettingKey) {
		t.Errorf("the remediation names the key beneath the root-marker block, where the schema does not admit it: %q", got)
	}
	// What a reader does for one run. The flag exists so a committed or
	// exported posture can be got past without editing anything, so a
	// remediation that does not name it withholds the only answer available to
	// somebody who did not write the file.
	if !contains(got, "--"+workspaceconfig.OfflineSettingKey+"=false") {
		t.Errorf("the remediation does not say how to override the posture for one run: %q", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
