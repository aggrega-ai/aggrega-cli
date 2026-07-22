// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

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
	if !errors.Is(err, egress.ErrOfflineEgress) {
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
	if !errors.Is(err, egress.ErrOfflineEgress) {
		t.Fatalf("offline HTTPClient: err = %v; want ErrOfflineEgress", err)
	}
	if c != nil {
		t.Fatalf("offline HTTPClient: client = %v; want nil", c)
	}
}

// The offline refusal carries its own remediation so callers that surface it
// need append none, and the source-resolution classifier will not double-append.
func TestErrOfflineEgressCarriesRemediation(t *testing.T) {
	if got := egress.ErrOfflineEgress.Error(); !contains(got, "remediation:") {
		t.Errorf("ErrOfflineEgress message %q lacks a remediation clause", got)
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
