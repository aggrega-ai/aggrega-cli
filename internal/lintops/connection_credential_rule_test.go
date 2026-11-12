// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package lintops

// The rule comparing the two ends of a key-checked connection, run the way the
// Release-scoped pass runs it: the documents assembled by the host from a
// resolved Release, dispatched once to the guest the binary ships, and the
// finding reported against the Release. Each case differs from the agreeing one
// in exactly one end, so a silence and a finding are each attributable to that
// end.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// credentialMarker is written as one end's literal locator. A locator can be the
// secret itself, so it must appear in no line the run reports.
const credentialMarker = "PLANTED-CREDENTIAL-MARKER-a91c"

// keyCheckingTarget declares the slot the standard catalog names for the key a
// server requires of every caller.
const keyCheckingTarget = `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: example/ai-model/server
  version: 1.0.0
spec:
  featuresSchema:
    api_key:
      selector: "feature.aggrega.ai/kind=secret"
      sensitive: true
  schema:
    values:
      type: object
`

// keyPresentingCaller declares the slot a connection on its model socket fills
// with the key the caller presents.
const keyPresentingCaller = `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: example/ai-agent/caller
  version: 1.0.0
spec:
  usesSchema:
    model:
      featuresSchema:
        credential:
          selector: "feature.aggrega.ai/kind=secret"
          sensitive: true
  schema:
    values:
      type: object
`

// twoEndedAggregate writes the server and its caller, each end's key attachment
// reading the given locator; an empty locator leaves that end's slot unfilled.
func twoEndedAggregate(targetLocator, connectionLocator string) string {
	var b strings.Builder
	b.WriteString(`apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: two-ended
  version: "1.0.0"
spec:
  parameters:
    shared_key:
      type: string
  components:
    model:
      type: example/ai-model/server:1.0.0
`)
	if targetLocator != "" {
		b.WriteString(`      features:
        api_key:
          type: core/referenced-secret
          values:
            origin: "` + targetLocator + `"
`)
	}
	b.WriteString(`    agent:
      type: example/ai-agent/caller:1.0.0
      uses:
        model:
          primary:
            target: model
`)
	if connectionLocator != "" {
		b.WriteString(`            features:
              credential:
                type: core/referenced-secret
                values:
                  origin: "` + connectionLocator + `"
`)
	}
	return b.String()
}

// runTwoEndedCheck assembles the Release-scoped documents for aggregateYAML over
// the two Components above, dispatches the shipped rule set's Release-scoped rules
// to the shipped guest, and returns the diagnostic report and what was collected.
func runTwoEndedCheck(t *testing.T, aggregateYAML string, resolve func(string, bool) bool) (string, Report) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte("workspace: {}\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}

	var agg types.Aggregate
	if err := yaml.Unmarshal([]byte(aggregateYAML), &agg); err != nil {
		t.Fatalf("parse aggregate: %v", err)
	}
	rel := &types.Release{APIVersion: "aggrega.ai/v1beta1", Kind: "Release"}
	rel.Metadata.Name = "two-ended-release"
	rc := &compilerops.ResolvedClosure{Release: rel, Aggregate: &agg, AggBytes: []byte(aggregateYAML), AggregatePath: "aggregate.yaml"}

	components := map[string]string{
		"example/ai-model/server:1.0.0": keyCheckingTarget,
		"example/ai-agent/caller:1.0.0": keyPresentingCaller,
	}
	lookup := func(uri string) (types.Component, bool) {
		body, ok := components[uri]
		if !ok {
			return types.Component{}, false
		}
		var c types.Component
		if err := yaml.Unmarshal([]byte(body), &c); err != nil {
			t.Fatalf("parse component %s: %v", uri, err)
		}
		return c, true
	}
	docs, err := releaseRuleDocumentsFor(rc, lookup)
	if err != nil {
		t.Fatalf("releaseRuleDocumentsFor: %v", err)
	}
	rules := releaseRuleIDs()
	if len(rules) == 0 {
		t.Fatal("the shipped rule set holds no rule reading a whole Release, so nothing below is dispatched")
	}

	releasePath := filepath.Join(root, "release.yaml")
	var out, diag bytes.Buffer
	collect := &FindingCollector{}
	if err := lintConventionsFiles(t.Context(), rw, nil, nil, nil, conventionsRun{
		policyResolve: resolve,
		releaseRules:  &releaseRuleCheck{rules: rules, docs: docs, path: releasePath},
	}, &out, &diag, collect); err != nil {
		t.Fatalf("the check must not halt on an advisory: %v\n%s", err, diag.String())
	}
	return diag.String(), collect.Report()
}

// TestConnectionCredentialRuleThroughTheShippedGuest pins the rule end to end
// over the host's own assembly: an agreeing pair is silent, each of the three
// disagreements is reported once against the Release as an advisory carrying the
// rule's codes and naming both ends, and the planted locator appears in no report.
func TestConnectionCredentialRuleThroughTheShippedGuest(t *testing.T) {
	const shared = "{{ .parameters.shared_key }}"
	cases := []struct {
		name          string
		target, conn  string
		wantFinding   bool
		wantInMessage string
	}{
		{name: "both ends read one parameter", target: shared, conn: shared},
		{name: "the target's key slot is unfilled", target: "", conn: credentialMarker,
			wantFinding: true, wantInMessage: "leaves its api_key slot"},
		{name: "the connection carries no credential", target: credentialMarker, conn: "",
			wantFinding: true, wantInMessage: "carries nothing in its credential slot"},
		{name: "the two ends write different locators", target: shared, conn: credentialMarker,
			wantFinding: true, wantInMessage: "write their secret's locator differently"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report, collected := runTwoEndedCheck(t, twoEndedAggregate(tc.target, tc.conn), nil)
			if strings.Contains(report, credentialMarker) {
				t.Errorf("the report quotes a locator:\n%s", report)
			}
			var mine []Finding
			for _, f := range collected.Findings {
				if f.RuleID == ruleID(errConnectionCredentialMatchesTargetKey) {
					mine = append(mine, f)
				}
				if strings.Contains(f.Message, credentialMarker) {
					t.Errorf("a collected finding quotes a locator: %s", f.Message)
				}
			}
			if !tc.wantFinding {
				if len(mine) != 0 {
					t.Errorf("an agreeing pair drew %d finding(s):\n%s", len(mine), report)
				}
				return
			}
			if len(mine) != 1 {
				t.Fatalf("want exactly one finding; got %d:\n%s", len(mine), report)
			}
			wantRow := abi.SeverityWarn + ": " + errConnectionCredentialMatchesTargetKey.ErrorShortCode() + " " +
				ruleID(errConnectionCredentialMatchesTargetKey)
			if !strings.Contains(report, wantRow) {
				t.Errorf("the finding is advisory and carries its rule's codes (%q):\n%s", wantRow, report)
			}
			for _, want := range []string{tc.wantInMessage, `"primary"`, `"model"`, `"agent"`} {
				if !strings.Contains(mine[0].Message, want) {
					t.Errorf("the finding names %q; got %s", want, mine[0].Message)
				}
			}
		})
	}
}

// TestConnectionCredentialRuleRespectsItsSuppression runs the disagreeing pair
// twice, the suppression decided each way, so the silence is the suppression's.
func TestConnectionCredentialRuleRespectsItsSuppression(t *testing.T) {
	aggregate := twoEndedAggregate("{{ .parameters.shared_key }}", credentialMarker)
	count := func(suppress bool) int {
		_, collected := runTwoEndedCheck(t, aggregate, func(flag string, ws bool) bool {
			if flag == "allow-connection-credential-mismatches" {
				return suppress
			}
			return ws
		})
		n := 0
		for _, f := range collected.Findings {
			if f.RuleID == ruleID(errConnectionCredentialMatchesTargetKey) {
				n++
			}
		}
		return n
	}
	if got := count(false); got != 1 {
		t.Fatalf("with the suppression off the rule reports; got %d finding(s)", got)
	}
	if got := count(true); got != 0 {
		t.Errorf("with the suppression on the rule is silent; got %d finding(s)", got)
	}
}
