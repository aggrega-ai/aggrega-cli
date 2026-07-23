// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lockfile_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// verdictSampleLockfile returns a minimal schema-valid lockfile carrying the
// supplied verdict-input records, so a write->load roundtrip exercises the
// verdict_inputs section through the real writer and the schema-validating loader.
func verdictSampleLockfile(vis []lockfile.VerdictInput) *lockfile.Lockfile {
	return &lockfile.Lockfile{
		Version:       1,
		Release:       "release.yaml",
		GeneratedAt:   "2026-07-23T00:00:00Z",
		VerdictInputs: vis,
	}
}

// TestVerdictInputs_DigestRoundTrip proves a verdict attachment's external
// rule-input digests survive WriteLockfile -> LoadLockfile (the schema-validating
// load admits the new section), carrying the attachment's authored URI, its role,
// and one digest per source in base-name order — and deliberately NO content
// (the cache-not-pin distinction from mutators.sources: a verdict input is
// recorded for audit, not reconstructed from the lockfile).
func TestVerdictInputs_DigestRoundTrip(t *testing.T) {
	in := []lockfile.VerdictInput{{
		Plugin: "governance/deny-gate:1.0.0",
		Role:   "ir-verdict",
		Sources: []lockfile.VerdictSourcePin{
			{Path: "rules.rego", Digest: "sha256:aaaa"},
			{Path: "extra.rego", Digest: "sha256:bbbb"},
		},
	}}
	path := filepath.Join(t.TempDir(), "release.lock")
	if err := lockfile.WriteLockfile(path, verdictSampleLockfile(in)); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// A verdict input is recorded for audit, never reconstructed — it must not
	// carry the source content the way the mutators section does.
	if strings.Contains(string(raw), "content:") {
		t.Errorf("verdict_inputs must record only digests, never content; got:\n%s", raw)
	}

	lf, _, err := lockfile.LoadLockfile(path)
	if err != nil {
		t.Fatalf("LoadLockfile (schema validation is part of the load): %v", err)
	}
	if len(lf.VerdictInputs) != 1 {
		t.Fatalf("verdict_inputs len = %d, want 1: %+v", len(lf.VerdictInputs), lf.VerdictInputs)
	}
	got := lf.VerdictInputs[0]
	if got.Plugin != "governance/deny-gate:1.0.0" || got.Role != "ir-verdict" {
		t.Errorf("verdict input identity roundtrip: got plugin=%q role=%q", got.Plugin, got.Role)
	}
	// Sources canonicalise to base-name order regardless of input order.
	if len(got.Sources) != 2 ||
		got.Sources[0].Path != "extra.rego" || got.Sources[0].Digest != "sha256:bbbb" ||
		got.Sources[1].Path != "rules.rego" || got.Sources[1].Digest != "sha256:aaaa" {
		t.Errorf("source digest roundtrip / ordering: got %+v", got.Sources)
	}
}

// TestVerdictInputs_ChangedDigestIsADiff proves the audit contract: two lockfiles
// that differ only in a recorded verdict-input digest — the exact effect a changed
// rule file produces on the next pull — write to different bytes, so a changed rule
// file surfaces as a lockfile diff. The same digest writes byte-identically.
func TestVerdictInputs_ChangedDigestIsADiff(t *testing.T) {
	mk := func(dg string) []lockfile.VerdictInput {
		return []lockfile.VerdictInput{{
			Plugin:  "governance/deny-gate:1.0.0",
			Role:    "ir-verdict",
			Sources: []lockfile.VerdictSourcePin{{Path: "rules.rego", Digest: dg}},
		}}
	}
	dir := t.TempDir()
	pA := filepath.Join(dir, "a.lock")
	pB := filepath.Join(dir, "b.lock")
	pA2 := filepath.Join(dir, "a2.lock")
	if err := lockfile.WriteLockfile(pA, verdictSampleLockfile(mk("sha256:before"))); err != nil {
		t.Fatalf("write A: %v", err)
	}
	if err := lockfile.WriteLockfile(pA2, verdictSampleLockfile(mk("sha256:before"))); err != nil {
		t.Fatalf("write A2: %v", err)
	}
	if err := lockfile.WriteLockfile(pB, verdictSampleLockfile(mk("sha256:after"))); err != nil {
		t.Fatalf("write B: %v", err)
	}
	a, _ := os.ReadFile(pA)
	a2, _ := os.ReadFile(pA2)
	b, _ := os.ReadFile(pB)
	if string(a) != string(a2) {
		t.Fatalf("same digest must write byte-identically:\n--- a ---\n%s\n--- a2 ---\n%s", a, a2)
	}
	if string(a) == string(b) {
		t.Fatalf("a changed rule-input digest must surface as a lockfile diff, but bytes were identical:\n%s", a)
	}
}

// TestVerdictInputs_WriteIsDeterministic proves the canonical order of the
// verdict_inputs section: entries sort by (plugin, role) so the same plugin
// attached at both verdict stages orders deterministically, and two independent
// writes of the same in-memory lockfile produce byte-identical output.
func TestVerdictInputs_WriteIsDeterministic(t *testing.T) {
	in := []lockfile.VerdictInput{
		{Plugin: "governance/gate:1.0.0", Role: "render-verdict", Sources: []lockfile.VerdictSourcePin{{Path: "r.rego", Digest: "sha256:1"}}},
		{Plugin: "governance/gate:1.0.0", Role: "ir-verdict", Sources: []lockfile.VerdictSourcePin{{Path: "r.rego", Digest: "sha256:2"}}},
		{Plugin: "governance/aaa:1.0.0", Role: "ir-verdict"},
	}
	dir := t.TempDir()
	p1 := filepath.Join(dir, "1.lock")
	p2 := filepath.Join(dir, "2.lock")
	if err := lockfile.WriteLockfile(p1, verdictSampleLockfile(in)); err != nil {
		t.Fatalf("write 1: %v", err)
	}
	if err := lockfile.WriteLockfile(p2, verdictSampleLockfile(in)); err != nil {
		t.Fatalf("write 2: %v", err)
	}
	b1, _ := os.ReadFile(p1)
	b2, _ := os.ReadFile(p2)
	if string(b1) != string(b2) {
		t.Fatalf("two writes diverged:\n--- 1 ---\n%s\n--- 2 ---\n%s", b1, b2)
	}
	// (plugin, role) ordering: aaa before gate; within gate, ir-verdict before render-verdict.
	lf, _, err := lockfile.LoadLockfile(p1)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := []struct{ plugin, role string }{
		{"governance/aaa:1.0.0", "ir-verdict"},
		{"governance/gate:1.0.0", "ir-verdict"},
		{"governance/gate:1.0.0", "render-verdict"},
	}
	if len(lf.VerdictInputs) != len(want) {
		t.Fatalf("verdict_inputs len = %d, want %d: %+v", len(lf.VerdictInputs), len(want), lf.VerdictInputs)
	}
	for i, w := range want {
		if lf.VerdictInputs[i].Plugin != w.plugin || lf.VerdictInputs[i].Role != w.role {
			t.Errorf("verdict_inputs[%d] = (%q,%q), want (%q,%q)", i,
				lf.VerdictInputs[i].Plugin, lf.VerdictInputs[i].Role, w.plugin, w.role)
		}
	}
}
