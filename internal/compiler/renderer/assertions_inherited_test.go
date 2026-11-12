// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// inheritedAssertionCatalog builds the base / middle / leaf chain the tests
// below evaluate, resolves the leaf through the real extends merge, and returns
// a lookup serving the resolved leaf — the same shape a catalog hands the
// evaluator after inheritance resolution.
func inheritedAssertionCatalog(t *testing.T) resolver.CatalogLookup {
	t.Helper()
	base := types.Component{
		Metadata: types.ComponentMetadata{Name: "example/base", Version: "1.0.0"},
		Spec: types.ComponentSpec{
			Assertions: omString(t,
				"tls-required", `{{ if not .values.tls_enabled }}{{ fail "the baseline requires tls_enabled: true" }}{{ end }}`,
			),
		},
	}
	mid := types.Component{
		Metadata: types.ComponentMetadata{Name: "example/mid", Version: "1.0.0"},
		Spec: types.ComponentSpec{
			Extends: "example/base",
			Assertions: omString(t,
				"audit-window", `{{ if lt (int .values.timeout_ms) 100 }}{{ fail "the audit tier needs timeout_ms of at least 100" }}{{ end }}`,
			),
		},
	}
	leaf := types.Component{
		Metadata: types.ComponentMetadata{Name: "example/leaf", Version: "1.0.0"},
		Spec: types.ComponentSpec{
			Extends: "example/mid",
			Assertions: omString(t,
				"timeout-budget", `{{ if gt (int .values.timeout_ms) 2000 }}{{ fail "this service must answer within 2000ms" }}{{ end }}`,
			),
		},
	}
	lookup := func(uri string) (types.Component, bool) {
		switch uri {
		case "example/base":
			return base, true
		case "example/mid":
			return mid, true
		}
		return types.Component{}, false
	}
	resolved, errs := validator.ResolveComponentExtends(leaf, lookup)
	if len(errs) > 0 {
		t.Fatalf("resolve extends: %v", errs)
	}
	return resolver.CatalogLookup{
		Component: func(string) (types.Component, bool) { return resolved, true },
	}
}

// TestStep4b_InheritedAssertionsEachReportTheirOwnName pins the observable half
// of the additive merge: every level's rule is evaluated, and the halt names the
// assertion that actually failed rather than the entity's first rule.
//
// It also pins the failure ORDER: an instance violating more than one rule
// reports the one declared furthest up the chain, because evaluation walks the
// merged set in order and a base's invariant is the more fundamental contract.
func TestStep4b_InheritedAssertionsEachReportTheirOwnName(t *testing.T) {
	t.Parallel()
	cl := inheritedAssertionCatalog(t)

	cases := []struct {
		name       string
		tls        bool
		timeoutMs  int
		wantHalt   bool
		wantInMsg  string
		wantAbsent string
	}{
		{name: "conforms to every level", tls: true, timeoutMs: 1500, wantHalt: false},
		{
			name: "violates only the chain root's rule", tls: false, timeoutMs: 1500,
			wantHalt: true, wantInMsg: `assertion "tls-required"`, wantAbsent: "timeout-budget",
		},
		{
			name: "violates only the middle tier's rule", tls: true, timeoutMs: 50,
			wantHalt: true, wantInMsg: `assertion "audit-window"`, wantAbsent: "tls-required",
		},
		{
			name: "violates only the leaf's own rule", tls: true, timeoutMs: 9000,
			wantHalt: true, wantInMsg: `assertion "timeout-budget"`, wantAbsent: "tls-required",
		},
		{
			// Two rules fail; the chain root's is the one reported.
			name: "violates the root's and the leaf's", tls: false, timeoutMs: 9000,
			wantHalt: true, wantInMsg: `assertion "tls-required"`, wantAbsent: "timeout-budget",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			graph := graphWithNode(t, "svc", "example/leaf",
				omAny(t, "timeout_ms", tc.timeoutMs, "tls_enabled", tc.tls))
			err := EvaluateComponentAndFeatureAssertions(graph, cl)
			if !tc.wantHalt {
				if err != nil {
					t.Fatalf("expected no halt; got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected a halt; got nil")
			}
			if !strings.Contains(err.Error(), tc.wantInMsg) {
				t.Errorf("message should carry %s; got %v", tc.wantInMsg, err)
			}
			if tc.wantAbsent != "" && strings.Contains(err.Error(), tc.wantAbsent) {
				t.Errorf("message should not name %q; got %v", tc.wantAbsent, err)
			}
			t.Logf("halt message: %s", err.Error())
		})
	}
}
