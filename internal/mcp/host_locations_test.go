// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// What a caller is told about a place on this host, read off the one function
// that decides it and off the rewriting of prose that reads it.
//
// Every row is asserted on both transports. The listener half is the rule under
// test; the process-streams half is the control that it changes nothing for a
// caller that launched this process, so a function that rewrote everything would
// fail here rather than pass the listener rows.

// told reads a location an answer carries as the string it was told, and a
// location the answer kept back as the empty string — which no told location is,
// since an empty location is carried as a told empty string.
func told(loc *string) string {
	if loc == nil {
		return ""
	}
	return *loc
}

// theServedRoot is a root these rows judge against without touching a disk. It is
// named so that a sibling extending its name and a climb back into it can be
// spelled from it.
var theServedRoot = filepath.Join(string(filepath.Separator), "srv", "served-ws-4k2")

// TestReportedLocationTellsAListenerOnlyNamesInsideTheWorkspace drives the one
// function deciding what a caller may be told about a location.
func TestReportedLocationTellsAListenerOnlyNamesInsideTheWorkspace(t *testing.T) {
	for _, tc := range []struct {
		// scenario says what the location is, so a failure reads as a rule.
		scenario string
		loc      string
		// name and told are what a caller reaching a listener is answered.
		name string
		told bool
	}{
		{
			scenario: "a file inside the workspace is told by its name under the root",
			loc:      filepath.Join(theServedRoot, "catalog", "base", "api.yaml"),
			name:     "catalog/base/api.yaml",
			told:     true,
		},
		{
			scenario: "the workspace root itself is kept back",
			loc:      theServedRoot,
			told:     false,
		},
		{
			scenario: "a file outside the workspace is kept back",
			loc:      filepath.Join(string(filepath.Separator), "held", "signing-key-7c1.pem"),
			told:     false,
		},
		{
			scenario: "a sibling whose name extends the root's is outside it",
			loc:      theServedRoot + "-old" + string(filepath.Separator) + "release.yaml",
			told:     false,
		},
		{
			scenario: "a relative climb that would land back inside is kept back",
			loc:      filepath.Join("..", filepath.Base(theServedRoot), "release.yaml"),
			told:     false,
		},
		{
			scenario: "a value already spelled relative to the root is told as it stands",
			loc:      filepath.Join("catalog", "base", "api.yaml"),
			name:     "catalog/base/api.yaml",
			told:     true,
		},
		{
			scenario: "an interior climb that stays inside is told by where it lands",
			loc: theServedRoot + string(filepath.Separator) + "a" + string(filepath.Separator) + ".." +
				string(filepath.Separator) + "release.yaml",
			name: "release.yaml",
			told: true,
		},
		{
			scenario: "an empty value names no place and is told as it is",
			loc:      "",
			name:     "",
			told:     true,
		},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			listener := &toolWorkspace{root: theServedRoot, transport: transportHTTP}
			name, told := listener.reportedLocation(tc.loc)
			if told != tc.told || name != tc.name {
				t.Errorf("over a listener %q is told as (%q, %v), want (%q, %v)", tc.loc, name, told, tc.name, tc.told)
			}

			field, withheld := listener.reportedLocationField(tc.loc)
			if withheld == tc.told {
				t.Errorf("over a listener the field for %q is marked withheld=%v, and the location told=%v",
					tc.loc, withheld, tc.told)
			}
			if tc.told && (field == nil || *field != tc.name) {
				t.Errorf("over a listener the field for %q carries %v, want %q", tc.loc, field, tc.name)
			}
			if !tc.told && field != nil {
				t.Errorf("over a listener a kept-back %q still carries %q", tc.loc, *field)
			}

			streams := &toolWorkspace{root: theServedRoot, transport: transportStdio}
			if name, told := streams.reportedLocation(tc.loc); !told || name != tc.loc {
				t.Errorf("over the process's own streams %q is told as (%q, %v), want it unchanged", tc.loc, name, told)
			}
			if field, withheld := streams.reportedLocationField(tc.loc); withheld || field == nil || *field != tc.loc {
				t.Errorf("over the process's own streams the field for %q is (%v, withheld=%v), want it unchanged",
					tc.loc, field, withheld)
			}
		})
	}
}

// aWorkspaceHoldingLocationsOutsideItsRoot resolves a workspace whose cascade
// holds a config file outside its root and whose catalog stack reads a directory
// outside it, as the listener would resolve it, and returns that workspace with
// the two outside locations.
//
// The machine-wide layer is redirected at a file of this test's own, so the
// cascade read here is the one written here rather than whoever runs the tests.
func aWorkspaceHoldingLocationsOutsideItsRoot(t *testing.T, transport transportMode) (tw *toolWorkspace,
	outsideConfig, outsideCatalog string) {

	t.Helper()
	base := t.TempDir()
	outsideConfig = filepath.Join(base, "machine-wide-9d3", "config.yaml")
	outsideCatalog = filepath.Join(base, "outside-catalog-9d3")
	root := filepath.Join(base, "served-ws-9d3")
	for _, dir := range []string{filepath.Dir(outsideConfig), outsideCatalog, root} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("make %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(outsideConfig, []byte("registries: []\n"), 0o644); err != nil {
		t.Fatalf("write the machine-wide layer: %v", err)
	}
	t.Setenv(workspaceconfig.EnvConfigOverride, outsideConfig)
	t.Setenv(signingKeyEnvVarName, "")
	document := "workspace:\n  catalog_dirs: [" + outsideCatalog + "]\noffline: true\n"
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(document), 0o644); err != nil {
		t.Fatalf("write the workspace's own configuration: %v", err)
	}

	s := &server{startDir: root, transport: transport}
	resolved, err := s.resolveToolWorkspace("", nil)
	if err != nil {
		t.Fatalf("resolve the workspace: %v", err)
	}
	return resolved, outsideConfig, outsideCatalog
}

// TestReportedTextSpellsEachHeldLocationAsTheCallerMayBeToldIt drives the text
// form over prose naming each kind of location a workspace holds, and over prose
// whose look-alikes it must leave alone.
func TestReportedTextSpellsEachHeldLocationAsTheCallerMayBeToldIt(t *testing.T) {
	listener, outsideConfig, outsideCatalog := aWorkspaceHoldingLocationsOutsideItsRoot(t, transportHTTP)
	root := listener.root
	sep := string(filepath.Separator)

	for _, tc := range []struct {
		scenario string
		text     string
		// want is what a caller reaching a listener is told.
		want string
	}{
		{
			scenario: "a file under the root, ended by a colon",
			text:     "open " + root + sep + "imports" + sep + "missing.json: no such file or directory",
			want:     "open imports/missing.json: no such file or directory",
		},
		{
			scenario: "the root itself, closing a sentence",
			text:     "Entities in " + root + ".",
			want:     "Entities in " + keptBackLocationWords + ".",
		},
		{
			scenario: "several occurrences in one text",
			text:     "load release " + root + sep + "release.yaml: open " + root + sep + "release.yaml: gone",
			want:     "load release release.yaml: open release.yaml: gone",
		},
		{
			scenario: "a quoted location",
			text:     `send "` + root + sep + "release.yaml" + `" again`,
			want:     `send "release.yaml" again`,
		},
		{
			scenario: "a sibling whose name extends the root's is left as written",
			text:     "found " + root + "-old" + sep + "release.yaml",
			want:     "found " + root + "-old" + sep + "release.yaml",
		},
		{
			scenario: "the root followed by a character that continues a segment name is left as written",
			text:     "copied to " + root + ".bak" + sep + "release.yaml",
			want:     "copied to " + root + ".bak" + sep + "release.yaml",
		},
		{
			scenario: "the root inside a longer path is not the root",
			text:     "mounted at " + sep + "mnt" + root + sep + "release.yaml",
			want:     "mounted at " + sep + "mnt" + root + sep + "release.yaml",
		},
		{
			scenario: "a climb out of the root spelled under it is kept back",
			text:     "read " + root + sep + ".." + sep + "elsewhere.yaml failed",
			want:     "read " + keptBackLocationWords + " failed",
		},
		{
			scenario: "the workspace's own config file is named under the root",
			text:     "offline posture is set by `offline: true` in " + filepath.Join(root, workspaceconfig.ConfigFilename),
			want:     "offline posture is set by `offline: true` in " + workspaceconfig.ConfigFilename,
		},
		{
			scenario: "a config-file layer outside the root is kept back",
			text:     "the registries come from " + outsideConfig + ", lowest precedence first",
			want:     "the registries come from " + keptBackLocationWords + ", lowest precedence first",
		},
		{
			scenario: "a file in a catalog directory outside the root is kept back",
			text:     "[CATALOG_ERROR] " + filepath.Join(outsideCatalog, "components", "widget.yaml") + ": bad shape",
			want:     "[CATALOG_ERROR] " + keptBackLocationWords + ": bad shape",
		},
		{
			scenario: "a location the workspace does not hold is not recognised",
			text:     "cached at " + sep + "var" + sep + "cache" + sep + "aggrega",
			want:     "cached at " + sep + "var" + sep + "cache" + sep + "aggrega",
		},
		{
			scenario: "text naming no location is told as it is",
			text:     "pull cannot combine 'verify' with 'upgrade_targets'.",
			want:     "pull cannot combine 'verify' with 'upgrade_targets'.",
		},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			if got := listener.reportedText(tc.text); got != tc.want {
				t.Errorf("over a listener\n  %q\nis told as\n  %q\nwant\n  %q", tc.text, got, tc.want)
			}
		})
	}

	// The control, over the same workspace resolved for the process's own streams:
	// every row's text is told exactly as it was composed.
	streams, _, _ := aWorkspaceHoldingLocationsOutsideItsRoot(t, transportStdio)
	for _, text := range []string{
		"open " + streams.root + sep + "imports" + sep + "missing.json: no such file or directory",
		"Entities in " + streams.root + ".",
		"[CATALOG_ERROR] " + streams.root + sep + "catalog" + sep + "x.yaml: bad shape",
	} {
		if got := streams.reportedText(text); got != text {
			t.Errorf("over the process's own streams\n  %q\nis told as\n  %q\nwant it unchanged", text, got)
		}
	}
	if got := streams.reportedTexts(nil); got != nil {
		t.Errorf("an absent list is told as %v, want it absent", got)
	}
	if got := listener.reportedTexts(nil); got != nil {
		t.Errorf("an absent list is told to a listener caller as %v, want it absent", got)
	}
}

// TestTheOnePointEveryResultLeavesThroughRewritesTextAndNotThePayload drives the
// point dispatch hands every tool's result through.
//
// The payload half is the property that keeps the transports comparable: a
// location a tool left in a structured field must reach the comparison that looks
// for it, so this point must not quietly repair it.
func TestTheOnePointEveryResultLeavesThroughRewritesTextAndNotThePayload(t *testing.T) {
	for _, tc := range []struct {
		scenario  string
		transport transportMode
		wantText  string
	}{
		{
			scenario:  "over a listener the text is spelled as the caller may be told it",
			transport: transportHTTP,
			wantText:  "wrote release.lock",
		},
		{
			scenario:  "over the process's own streams the text is handed on as composed",
			transport: transportStdio,
			wantText:  "wrote " + filepath.Join(theServedRoot, "release.lock"),
		},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			structured, err := json.Marshal(struct {
				Lockfile string `json:"lockfile"`
			}{Lockfile: filepath.Join(theServedRoot, "release.lock")})
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			res := &callToolResult{
				Content:           []toolTextContent{{Type: "text", Text: "wrote " + filepath.Join(theServedRoot, "release.lock")}},
				StructuredContent: structured,
			}
			tw := &toolWorkspace{root: theServedRoot, transport: tc.transport}
			s := &server{transport: tc.transport}
			answered, _ := tw.answer(res, nil)
			got := s.withoutHostLocations(answered)
			if got.Content[0].Text != tc.wantText {
				t.Errorf("the text reached the caller as %q, want %q", got.Content[0].Text, tc.wantText)
			}
			if !strings.Contains(string(got.StructuredContent), filepath.Join(theServedRoot, "release.lock")) {
				t.Errorf("the structured payload was rewritten on the way out: %s", got.StructuredContent)
			}
		})
	}

	if got := (&server{transport: transportHTTP}).withoutHostLocations(nil); got != nil {
		t.Errorf("an absent result came back as %+v", got)
	}
}

// TestAServedWorkspaceIsResolvedOnlyForAListener pins that the answer a tool
// carried no workspace for is read against nothing resolved over the process's
// own streams, and against the served workspace over a listener.
func TestAServedWorkspaceIsResolvedOnlyForAListener(t *testing.T) {
	root := listenerRootNaming(t, "")

	streams := (&server{startDir: root, transport: transportStdio}).servedWorkspace()
	if streams.root != "" || streams.rw != nil {
		t.Errorf("over the process's own streams a workspace was resolved: root %q", streams.root)
	}

	listener := (&server{startDir: root, transport: transportHTTP}).servedWorkspace()
	if listener.rw == nil || listener.root == "" {
		t.Fatalf("over a listener no workspace was resolved for the answer")
	}
	if _, told := listener.reportedLocation(listener.root); told {
		t.Errorf("the served workspace's root %q is told to a listener caller", listener.root)
	}
}
