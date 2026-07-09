// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestVersionHumanFormat pins the human-readable line's structural
// invariants: starts with the literal "aggrega ", contains "commit",
// and contains "go1." (the Go version prefix every supported
// runtime emits). Tolerant of whatever version value
// debug.ReadBuildInfo returns at test time — typically "(devel)" in
// source-tree go-test runs.
func TestVersionHumanFormat(t *testing.T) {
	var buf bytes.Buffer
	if err := runVersion(&buf, false); err != nil {
		t.Fatalf("runVersion: %v", err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "aggrega ") {
		t.Errorf("expected output to start with %q; got %q", "aggrega ", out)
	}
	if !strings.Contains(out, "commit") {
		t.Errorf("expected output to contain 'commit'; got %q", out)
	}
	if !strings.Contains(out, "go1.") {
		t.Errorf("expected output to contain a 'go1.' Go-version prefix; got %q", out)
	}
}

// TestVersionJSONShape pins the wire contract the extension's
// version-check helper consumes. Asserts the four always-present
// camelCase fields are present and non-empty.
func TestVersionJSONShape(t *testing.T) {
	var buf bytes.Buffer
	if err := runVersion(&buf, true); err != nil {
		t.Fatalf("runVersion: %v", err)
	}
	var info struct {
		Version   string `json:"version"`
		Committed string `json:"committed"`
		Commit    string `json:"commit"`
		GoVersion string `json:"goVersion"`
		Platform  string `json:"platform"`
	}
	if err := json.Unmarshal(buf.Bytes(), &info); err != nil {
		t.Fatalf("unmarshal: %v (raw: %s)", err, buf.String())
	}
	if info.Version == "" {
		t.Errorf("version field empty")
	}
	if info.GoVersion == "" {
		t.Errorf("goVersion field empty")
	}
	if info.Platform == "" {
		t.Errorf("platform field empty")
	}
	if !strings.Contains(info.Platform, "/") {
		t.Errorf("platform should contain '/'; got %q", info.Platform)
	}
}

// TestVersionHumanLineWithTimestamp pins the human line's shape when a
// commit timestamp is present: the "committed <ts>" segment appears
// before the commit hash, and the hash is truncated to eight chars.
func TestVersionHumanLineWithTimestamp(t *testing.T) {
	got := formatVersionLine(versionInfo{
		Version:   "1.2.3",
		Committed: "2026-07-09T18:07:57Z",
		Commit:    "62c43d5babcdef",
		GoVersion: "go1.26.0",
	})
	want := "aggrega 1.2.3 (committed 2026-07-09T18:07:57Z, commit 62c43d5b, go1.26.0)\n"
	if got != want {
		t.Errorf("formatVersionLine with timestamp:\n got %q\nwant %q", got, want)
	}
}

// TestVersionHumanLineOmitsTimestamp pins that an absent commit
// timestamp drops the "committed" segment entirely rather than
// printing a placeholder.
func TestVersionHumanLineOmitsTimestamp(t *testing.T) {
	got := formatVersionLine(versionInfo{
		Version:   "1.2.3",
		Commit:    "62c43d5babcdef",
		GoVersion: "go1.26.0",
	})
	want := "aggrega 1.2.3 (commit 62c43d5b, go1.26.0)\n"
	if got != want {
		t.Errorf("formatVersionLine without timestamp:\n got %q\nwant %q", got, want)
	}
	if strings.Contains(got, "committed") {
		t.Errorf("expected no 'committed' segment when timestamp absent; got %q", got)
	}
}

// TestVersionJSONCommittedOmittedWhenEmpty pins that the committed
// field is omitted from the wire shape when empty (omitempty), so a
// build without vcs.time does not emit an empty committed field.
func TestVersionJSONCommittedOmittedWhenEmpty(t *testing.T) {
	b, err := json.Marshal(versionInfo{
		Version:   "1.2.3",
		Commit:    "62c43d5b",
		GoVersion: "go1.26.0",
		Platform:  "linux/amd64",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "committed") {
		t.Errorf("committed should be omitted when empty; got %s", b)
	}
}

// TestVersionJSONCommittedPresentWhenSet pins that a populated commit
// timestamp emits under the camelCase "committed" key.
func TestVersionJSONCommittedPresentWhenSet(t *testing.T) {
	b, err := json.Marshal(versionInfo{
		Version:   "1.2.3",
		Committed: "2026-07-09T18:07:57Z",
		Commit:    "62c43d5b",
		GoVersion: "go1.26.0",
		Platform:  "linux/amd64",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"committed":"2026-07-09T18:07:57Z"`) {
		t.Errorf("expected committed field in wire shape; got %s", b)
	}
}

// TestNormalizeVersion pins the v-less version contract: tag-shaped
// values lose the leading "v"; sentinels, bare hashes, and already
// v-less values pass through unchanged.
func TestNormalizeVersion(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"v0.1.2", "0.1.2"},
		{"0.1.2", "0.1.2"},
		{"v0.1.2-dev+abc123f", "0.1.2-dev+abc123f"},
		{"v0.1.0-5-gabc1234", "0.1.0-5-gabc1234"},
		{"(devel)", "(devel)"},
		{"(unknown)", "(unknown)"},
		{"abc123f", "abc123f"},
		{"v", "v"},
		{"", ""},
	}
	for _, c := range cases {
		if got := normalizeVersion(c.in); got != c.want {
			t.Errorf("normalizeVersion(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

// TestVersionStampedVLess pins that a v-prefixed ldflags stamp is
// reported v-less on the JSON wire shape the extension consumes.
func TestVersionStampedVLess(t *testing.T) {
	orig := version
	version = "v9.9.9"
	defer func() { version = orig }()

	var buf bytes.Buffer
	if err := runVersion(&buf, true); err != nil {
		t.Fatalf("runVersion: %v", err)
	}
	var info struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(buf.Bytes(), &info); err != nil {
		t.Fatalf("unmarshal: %v (raw: %s)", err, buf.String())
	}
	if info.Version != "9.9.9" {
		t.Errorf("stamped v9.9.9 should report as 9.9.9; got %q", info.Version)
	}
}

// TestVersionJSONIsValidJSON guards against accidental shape drift
// that would still be syntactically valid Go (e.g., a struct field
// rename) but break the extension's parse. Decodes into a permissive
// map and asserts no unexpected top-level keys appear.
func TestVersionJSONIsValidJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := runVersion(&buf, true); err != nil {
		t.Fatalf("runVersion: %v", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(buf.Bytes(), &generic); err != nil {
		t.Fatalf("unmarshal generic: %v", err)
	}
	// committed is optional (omitempty): allowed when present, not
	// required, since a build without vcs.time omits it.
	allowed := map[string]bool{"version": true, "committed": true, "commit": true, "goVersion": true, "platform": true}
	for k := range generic {
		if !allowed[k] {
			t.Errorf("unexpected top-level key %q in version JSON", k)
		}
	}
	required := map[string]bool{"version": true, "commit": true, "goVersion": true, "platform": true}
	for k := range required {
		if _, present := generic[k]; !present {
			t.Errorf("missing expected key %q in version JSON", k)
		}
	}
}
