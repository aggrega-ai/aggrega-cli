// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/thirdparty"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
)

// TestVersionHumanFormat pins the human-readable line's structural
// invariants: starts with the literal "aggrega ", contains "commit",
// and contains "go1." (the Go version prefix every supported
// runtime emits). Tolerant of whatever version value
// debug.ReadBuildInfo returns at test time — typically "(devel)" in
// source-tree go-test runs.
func TestVersionHumanFormat(t *testing.T) {
	var buf bytes.Buffer
	if err := runVersion(&buf, false, false); err != nil {
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
	if err := runVersion(&buf, true, false); err != nil {
		t.Fatalf("runVersion: %v", err)
	}
	var info struct {
		Version    string `json:"version"`
		Committed  string `json:"committed"`
		Commit     string `json:"commit"`
		ABIVersion string `json:"abiVersion"`
		GoVersion  string `json:"goVersion"`
		Platform   string `json:"platform"`
	}
	if err := json.Unmarshal(buf.Bytes(), &info); err != nil {
		t.Fatalf("unmarshal: %v (raw: %s)", err, buf.String())
	}
	if info.Version == "" {
		t.Errorf("version field empty")
	}
	if info.ABIVersion != wasm.ABIVersion {
		t.Errorf("abiVersion = %q; want %q", info.ABIVersion, wasm.ABIVersion)
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
	// Not parallel: mutates the process-global version seam other tests read.
	orig := version
	version = "v9.9.9"
	defer func() { version = orig }()

	var buf bytes.Buffer
	if err := runVersion(&buf, true, false); err != nil {
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

// TestVersionABIVersionReported pins that both output forms carry the
// plugin ABI version the host speaks: the JSON wire shape exposes it
// under "abiVersion" and the human line shows an "abi" segment. The
// value is the build-time constant, so it is asserted against the
// single source rather than a literal.
func TestVersionABIVersionReported(t *testing.T) {
	var jsonBuf bytes.Buffer
	if err := runVersion(&jsonBuf, true, false); err != nil {
		t.Fatalf("runVersion json: %v", err)
	}
	var info struct {
		ABIVersion string `json:"abiVersion"`
	}
	if err := json.Unmarshal(jsonBuf.Bytes(), &info); err != nil {
		t.Fatalf("unmarshal: %v (raw: %s)", err, jsonBuf.String())
	}
	if info.ABIVersion != wasm.ABIVersion {
		t.Errorf("json abiVersion = %q; want %q", info.ABIVersion, wasm.ABIVersion)
	}

	var humanBuf bytes.Buffer
	if err := runVersion(&humanBuf, false, false); err != nil {
		t.Fatalf("runVersion human: %v", err)
	}
	if want := "abi " + wasm.ABIVersion; !strings.Contains(humanBuf.String(), want) {
		t.Errorf("expected human line to contain %q; got %q", want, humanBuf.String())
	}
}

// TestVersionHumanLineIncludesABISegment pins the placement of the abi
// segment in the human line: after the commit hash, before the Go
// version.
func TestVersionHumanLineIncludesABISegment(t *testing.T) {
	got := formatVersionLine(versionInfo{
		Version:    "1.2.3",
		Committed:  "2026-07-09T18:07:57Z",
		Commit:     "62c43d5babcdef",
		ABIVersion: "v1beta1",
		GoVersion:  "go1.26.0",
	})
	want := "aggrega 1.2.3 (committed 2026-07-09T18:07:57Z, commit 62c43d5b, abi v1beta1, go1.26.0)\n"
	if got != want {
		t.Errorf("formatVersionLine with abi:\n got %q\nwant %q", got, want)
	}
}

// TestVersionEmbeddedPointer proves the plain human output carries the one-line
// embedded-plugin pointer after the version line: the count, a version, and the
// literal command reference. The embedded set is compiled in, so the count is
// positive in a normal build.
func TestVersionEmbeddedPointer(t *testing.T) {
	var buf bytes.Buffer
	if err := runVersion(&buf, false, false); err != nil {
		t.Fatalf("runVersion: %v", err)
	}
	out := buf.String()
	summaries := collectEmbeddedSummaries()
	if len(summaries) == 0 {
		t.Skip("no embedded plugins in this build; pointer line is intentionally omitted")
	}
	for _, want := range []string{
		"embedded",
		countOfNoun(len(summaries), "plugin", "plugins") + " version",
		"`aggrega plugin list` for details",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plain output missing embedded pointer fragment %q; got:\n%s", want, out)
		}
	}
}

// TestVersionVerboseTable proves --verbose replaces the pointer with the
// embedded-plugin table: the count/version header and the four-column header row.
func TestVersionVerboseTable(t *testing.T) {
	var buf bytes.Buffer
	if err := runVersion(&buf, false, true); err != nil {
		t.Fatalf("runVersion verbose: %v", err)
	}
	out := buf.String()
	summaries := collectEmbeddedSummaries()
	if len(summaries) == 0 {
		if !strings.Contains(out, "No embedded plugins.") {
			t.Errorf("empty embedded set should say so; got:\n%s", out)
		}
		return
	}
	for _, want := range []string{
		"Embedded plugins (",
		"TYPE", "NAME", "CAPABILITIES", "COMMIT",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("verbose output missing %q; got:\n%s", want, out)
		}
	}
	// The verbose form replaces the pointer line rather than duplicating it.
	if strings.Contains(out, "`aggrega plugin list` for details") {
		t.Errorf("verbose output should not also print the pointer line; got:\n%s", out)
	}
}

// TestVersionVerboseJSON proves --verbose -o json adds the embeddedPlugins array
// while the plain -o json wire shape omits it.
func TestVersionVerboseJSON(t *testing.T) {
	summaries := collectEmbeddedSummaries()

	var plain bytes.Buffer
	if err := runVersion(&plain, true, false); err != nil {
		t.Fatalf("runVersion json: %v", err)
	}
	var plainGeneric map[string]any
	if err := json.Unmarshal(plain.Bytes(), &plainGeneric); err != nil {
		t.Fatalf("unmarshal plain: %v", err)
	}
	if _, present := plainGeneric["embeddedPlugins"]; present {
		t.Errorf("plain -o json should omit embeddedPlugins; got:\n%s", plain.String())
	}

	var verbose bytes.Buffer
	if err := runVersion(&verbose, true, true); err != nil {
		t.Fatalf("runVersion verbose json: %v", err)
	}
	var doc struct {
		EmbeddedPlugins []struct {
			URI          string   `json:"uri"`
			Type         string   `json:"type"`
			Capabilities []string `json:"capabilities"`
			Version      string   `json:"version"`
			Commit       string   `json:"commit"`
		} `json:"embeddedPlugins"`
	}
	if err := json.Unmarshal(verbose.Bytes(), &doc); err != nil {
		t.Fatalf("unmarshal verbose: %v\n%s", err, verbose.String())
	}
	if len(doc.EmbeddedPlugins) != len(summaries) {
		t.Errorf("embeddedPlugins len = %d, want %d", len(doc.EmbeddedPlugins), len(summaries))
	}
}

// TestSharedEmbeddedVersion covers the shared-version derivation: a single
// stamp is returned as-is, an empty stamp reads "(unknown)", and divergent
// stamps read "(mixed)".
func TestSharedEmbeddedVersion(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   []embeddedPluginSummary
		want string
	}{
		{"uniform", []embeddedPluginSummary{{version: "0.4.0"}, {version: "0.4.0"}}, "0.4.0"},
		{"empty stamp", []embeddedPluginSummary{{version: ""}, {version: ""}}, "(unknown)"},
		{"divergent", []embeddedPluginSummary{{version: "0.4.0"}, {version: "0.5.0"}}, "(mixed)"},
		{"none", nil, "(mixed)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := sharedEmbeddedVersion(tc.in); got != tc.want {
				t.Errorf("sharedEmbeddedVersion(%s) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

// TestVersionJSONIsValidJSON guards against accidental shape drift
// that would still be syntactically valid Go (e.g., a struct field
// rename) but break the extension's parse. Decodes into a permissive
// map and asserts no unexpected top-level keys appear.
func TestVersionJSONIsValidJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := runVersion(&buf, true, false); err != nil {
		t.Fatalf("runVersion: %v", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(buf.Bytes(), &generic); err != nil {
		t.Fatalf("unmarshal generic: %v", err)
	}
	// committed is optional (omitempty): allowed when present, not
	// required, since a build without vcs.time omits it.
	allowed := map[string]bool{"version": true, "committed": true, "commit": true, "abiVersion": true, "goVersion": true, "platform": true}
	for k := range generic {
		if !allowed[k] {
			t.Errorf("unexpected top-level key %q in version JSON", k)
		}
	}
	required := map[string]bool{"version": true, "commit": true, "abiVersion": true, "goVersion": true, "platform": true}
	for k := range required {
		if _, present := generic[k]; !present {
			t.Errorf("missing expected key %q in version JSON", k)
		}
	}
}

// TestVersionJSONIsIndented pins the byte shape of the structured version output
// through the real command path, which is the part the shared writer's own tests
// cannot cover: they prove the writer emits the indented shape, and this proves
// this command still routes through it. A change that re-rendered this payload on
// its way out, or bypassed the shared writer for this surface alone, would still
// produce valid JSON, would still satisfy every other test in this file, and would
// move a user-visible surface with nothing else in the Go build or test run
// noticing. That silence is the reason the shape is asserted literally here.
//
// The verbose form is asserted too, because it is the larger payload and is
// therefore where a divergent rendering would most plausibly be introduced.
func TestVersionJSONIsIndented(t *testing.T) {
	for _, tc := range []struct {
		name    string
		verbose bool
	}{{"plain", false}, {"verbose", true}} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := runVersion(&buf, true, tc.verbose); err != nil {
				t.Fatalf("runVersion: %v", err)
			}
			out := buf.String()

			if n := strings.Count(out, "\n"); n < 2 {
				t.Errorf("expected the payload to span several lines; got %d newline(s) in:\n%q", n, out)
			}
			if !strings.HasSuffix(out, "}\n") || strings.HasSuffix(out, "}\n\n") {
				t.Errorf("expected the payload to end with a brace and exactly one newline; got %q", out)
			}
			if !strings.Contains(out, "\n  \"") {
				t.Errorf("expected two-space indentation in the payload; got %q", out)
			}

			// Both directions: the indented spelling present, and the unspaced
			// spelling absent so a reversion fails here rather than silently.
			for _, want := range []string{`"version": "`, `"abiVersion": "`} {
				if !strings.Contains(out, want) {
					t.Errorf("expected the indented form %s in the payload; got %q", want, out)
				}
			}
			for _, unwanted := range []string{`"version":"`, `"abiVersion":"`} {
				if strings.Contains(out, unwanted) {
					t.Errorf("expected no unspaced form %s in the payload; got %q", unwanted, out)
				}
			}
		})
	}
}

// runVersionCmd executes the version command with args and returns its stdout and
// the error it ended with.
func runVersionCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	c := newVersionCmd()
	c.SetOut(&out)
	c.SetErr(&bytes.Buffer{})
	c.SilenceUsage = true
	c.SilenceErrors = true
	c.SetArgs(args)
	err := c.Execute()
	return out.String(), err
}

// TestVersionLicensesPrintsEmbeddedNotices pins --licenses to the embedded text,
// unchanged, and --licenses -o text to the same output.
func TestVersionLicensesPrintsEmbeddedNotices(t *testing.T) {
	for _, args := range [][]string{{"--licenses"}, {"--licenses", "-o", "text"}} {
		got, err := runVersionCmd(t, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if got != thirdparty.Notices() {
			t.Errorf("%v: output differs from thirdparty.Notices()", args)
		}
	}
}

// TestVersionLicensesRefusesCombinedFlags pins the refusal of --licenses with
// -o json and with --verbose, each naming both flags.
func TestVersionLicensesRefusesCombinedFlags(t *testing.T) {
	const jsonMsg = "--licenses prints plain text and cannot be combined with -o json (--output json)"
	const verboseMsg = "--licenses prints the notices only and cannot be combined with --verbose"
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--licenses", "-o", "json"}, jsonMsg},
		{[]string{"--licenses", "--output", "json"}, jsonMsg},
		{[]string{"--licenses", "--verbose"}, verboseMsg},
	}
	for _, tc := range cases {
		got, err := runVersionCmd(t, tc.args...)
		if err == nil {
			t.Fatalf("%v: no error", tc.args)
		}
		if err.Error() != tc.want {
			t.Errorf("%v: error = %q; want %q", tc.args, err.Error(), tc.want)
		}
		if got != "" {
			t.Errorf("%v: wrote output despite the refusal: %q", tc.args, got)
		}
	}
}
