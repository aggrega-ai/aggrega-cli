// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// listedDirWorkspace lays down the shared Release-entity fixture — a Component
// whose pillar refers to the library Sizing in variant-sized/, the same Component
// referring to no library in variant-plain/, and a Release file for each — under the
// workspace configuration given, and opens it the way the daemon opens any
// workspace a request names.
func listedDirWorkspace(t *testing.T, config string) (*Server, string) {
	t.Helper()
	src, err := filepath.Abs(filepath.Join("..", "compiler", "testdata", "release-entity-check"))
	if err != nil {
		t.Fatalf("locate the fixture: %v", err)
	}
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(src)); err != nil {
		t.Fatalf("copy the fixture workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(config), 0o644); err != nil {
		t.Fatalf("write the workspace configuration: %v", err)
	}
	sess := newTestSession(t, root, bytes.NewReader(nil), &bytes.Buffer{})
	srv, err := sess.workspaceFor(root)
	if err != nil {
		t.Fatalf("open the workspace at %s: %v", root, err)
	}
	return srv, root
}

// TestValidateReadsAListedCatalogDirectoryWhereNothingExistsAsHoldingNothing is
// the invocation proof of what a catalog entity file carries when a catalog
// directory the Release reads is listed and nothing exists there: one request over
// the real framing per row, and the answer read whole.
//
// A listed directory where nothing exists is not in the stack the check is
// handed, so the check's verdict is the one it gives with that directory not
// listed at all — wherever the configuration lists it, for the workspace or for
// the Release the request named. A clean Component stays free of any error, and a
// finding the check makes keeps the check's own words, with nothing about the
// directory. The directory is named instead in one warning entry, leading the
// answer, whose text is the line the resolver composes for that Release's stack —
// the line `aggrega pull` and `aggrega build` print for it.
//
// The first row is the positive control: every directory there, the Component is
// clean and the answer carries no entry at all. The last row is the control on the
// other side: a Component the check does refuse is refused for its own fault, the
// listing beside it notwithstanding. Each row in between carries one listing where
// nothing exists, somewhere different, and must reach the control's verdict.
func TestValidateReadsAListedCatalogDirectoryWhereNothingExistsAsHoldingNothing(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		config  string
		release string
		// absent is the listed directory where nothing exists, empty for the row
		// listing none.
		absent string
		// carries are phrases the error entry must carry, and lacks phrases it may
		// not. A row with neither must carry no error entry.
		carries []string
		lacks   []string
	}{
		{name: "every directory the Release reads exists, and the answer carries no entry",
			config:  "workspace:\n  catalog_dirs:\n    - variant-sized\n",
			release: "release-sized.yaml"},
		{name: "a directory the workspace's list names where nothing exists leaves the Component clean and is named once",
			config:  "workspace:\n  catalog_dirs:\n    - variant-sized\n    - no-such-catalog\n",
			release: "release-sized.yaml", absent: "no-such-catalog"},
		{name: "a directory the Release's own list names where nothing exists leaves the Component clean and is named once",
			config:  "workspace:\n  releases:\n    release-sized.yaml:\n      catalog_dirs:\n        - variant-sized\n        - gone-for-sized\n",
			release: "release-sized.yaml", absent: "gone-for-sized"},
		{name: "a directory the workspace's list names, for a Release given no list of its own, leaves the Component clean and is named once",
			config: "workspace:\n  catalog_dirs:\n    - variant-sized\n    - no-such-catalog\n" +
				"  releases:\n    release-plain.yaml:\n      catalog_dirs:\n        - variant-plain\n",
			release: "release-sized.yaml", absent: "no-such-catalog"},
		{name: "a finding the check makes keeps the check's own words beside a listing where nothing exists",
			config:  "workspace:\n  catalog_dirs:\n    - variant-plain\n    - no-such-catalog\n",
			release: "release-sized.yaml", absent: "no-such-catalog",
			carries: []string{"SchemaLibrary Sizing not found in catalog"},
			lacks:   []string{"no-such-catalog", "does not exist"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, root := listedDirWorkspace(t, tc.config)
			file := filepath.Join(root, "variant-sized", "component", "db.yaml")
			res := validateContentOverTheWire(t, srv, file, mustRead(t, file), tc.release)
			messages := diagMessages(res.Diagnostics)

			// The line the resolver composes for this Release's stack, as the entry
			// naming the directory must read once the editor's prefix is laid on it.
			var composed bytes.Buffer
			srv.resolvedWorkspace.NoteAbsentCatalogDirs(&composed, filepath.Join(root, tc.release), nil)
			line := strings.TrimSpace(composed.String())
			if tc.absent == "" {
				if line != "" || len(res.Diagnostics) != 0 {
					t.Fatalf("want a clean answer over a stack leaving nothing out; the resolver composes %q and the answer carries %q",
						line, messages)
				}
				return
			}
			// The directory as the line names it: its last element, then the setting.
			names := string(filepath.Separator) + tc.absent + " is listed under "
			if !strings.Contains(line, names) {
				t.Fatalf("precondition: the resolver composes %q, which does not name %s", line, tc.absent)
			}
			want := closureAdvisoryDiagnostic(line).Message
			var named []int
			for i, d := range res.Diagnostics {
				if strings.Contains(d.Message, names) {
					named = append(named, i)
				}
			}
			if len(named) != 1 {
				t.Fatalf("want one entry naming %s; the answer carries %q", tc.absent, messages)
			}
			if got := res.Diagnostics[named[0]]; named[0] != 0 || got.Severity != editorSeverityWarning || got.Message != want {
				t.Errorf("the entry naming %s is entry %d at severity %v reading %q; want the first entry, a warning, reading %q",
					tc.absent, named[0], got.Severity, got.Message, want)
			}

			var entry string
			for _, d := range res.Diagnostics {
				if d.Severity == editorSeverityError {
					entry = d.Message
				}
			}
			if len(tc.carries) == 0 {
				if entry != "" {
					t.Errorf("want no error entry beside a listing where nothing exists; got %q", entry)
				}
				return
			}
			if entry == "" {
				t.Fatalf("want an error entry; the answer carries %q", messages)
			}
			for _, phrase := range tc.carries {
				if !strings.Contains(entry, phrase) {
					t.Errorf("the entry does not carry %q:\n%s", phrase, entry)
				}
			}
			for _, phrase := range tc.lacks {
				if strings.Contains(entry, phrase) {
					t.Errorf("the entry carries %q:\n%s", phrase, entry)
				}
			}
		})
	}
}

// TestValidatePublishesADependencyFailureOnlyOnAggregaDocuments is the invocation
// proof of where a Release whose dependencies cannot be resolved is reported: on
// the Release's own document and on every document the daemon reads as one of
// Aggrega's own, and on no YAML it reads as no kind of its own — a file that is
// not about anything the Release depends on.
//
// Every Release in the workspace fails to resolve (it has no Manifest), so each
// row's answer is decided by which document it names and nothing else. The first
// rows are the positive controls: the Release, its Aggregate, and the workspace
// configuration, which the daemon reads by its name, each carry the failure. The
// foreign rows each send one kind of document the daemon reads as none of its own,
// and each answer is empty. The last row holds the one exception: the Release's own
// document keeps the failure while its header, mid-edit, no longer classifies.
func TestValidatePublishesADependencyFailureOnlyOnAggregaDocuments(t *testing.T) {
	t.Parallel()
	srv, root := entityCheckWorkspace(t)
	at := func(rel string) string { return filepath.Join(root, rel) }
	const dependencies = "Dependencies: "
	foreign := map[string]string{
		".github/workflows/ci.yml": "name: package-and-publish\non:\n  push:\n    tags: [\"v*\"]\n" +
			"jobs:\n  package:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n",
		"compose.yaml": "services:\n  web:\n    image: registry.example/web:1.0.0\n    ports: [\"8080:8080\"]\n",
		"gadget.yaml":  "apiVersion: aggrega.ai/v1alpha1\nkind: Gadget\nmetadata:\n  name: not-a-kind\n",
	}
	for rel, content := range foreign {
		if err := os.MkdirAll(filepath.Dir(at(rel)), 0o755); err != nil {
			t.Fatalf("create the directory for %s: %v", rel, err)
		}
		if err := os.WriteFile(at(rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	cases := []struct {
		name string
		file string
		// edit, when set, turns the file's bytes into the buffer the request carries.
		edit func(string) string
		// reported is whether the answer carries the dependency failure; a row that
		// does not must answer nothing at all.
		reported bool
	}{
		{name: "the Release's own document carries it", file: at("release-sized.yaml"), reported: true},
		{name: "the Release's Aggregate carries it", file: at("aggregate.yaml"), reported: true},
		{name: "the workspace configuration, read by its name, carries it",
			file: at(workspaceconfig.ConfigFilename), reported: true},
		{name: "a continuous-integration workflow carries nothing", file: at(".github/workflows/ci.yml")},
		{name: "a Compose file carries nothing", file: at("compose.yaml")},
		{name: "a document naming Aggrega's API group with a kind the daemon does not read carries nothing",
			file: at("gadget.yaml")},
		{name: "the Release's own document keeps it while its header does not classify",
			file:     at("release-sized.yaml"),
			edit:     func(s string) string { return strings.Replace(s, "kind: Release", "kind: Releas", 1) },
			reported: true},
	}
	// Not parallel: the rows share one daemon, and two name one file.
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content := mustRead(t, tc.file)
			if tc.edit != nil {
				edited := tc.edit(content)
				if edited == content {
					t.Fatal("the edit changed nothing, so the row would send the file as saved")
				}
				content = edited
			}
			res := validateContentOverTheWire(t, srv, tc.file, content, "release-sized.yaml")
			messages := diagMessages(res.Diagnostics)
			if !tc.reported {
				if len(res.Diagnostics) != 0 {
					t.Fatalf("a document the daemon reads as no kind of its own carries %q", messages)
				}
				return
			}
			if !strings.Contains(strings.Join(messages, "\n"), dependencies) {
				t.Errorf("the dependency failure is not reported here: %q", messages)
			}
		})
	}
}

// TestValidateNamesTheReleaseADocumentIsCheckedAgainstWhereItIsNotTheReleasesOwn is
// the invocation proof of what the entry reporting a Release whose dependencies
// cannot be resolved says on each document checked against that Release: one
// request over the real framing per row, and the entry read whole.
//
// On the Release's own document the entry is the resolution's account behind the
// prefix and nothing else. On every other document the daemon reads as one of its
// own — another Release, the Aggregate, the workspace configuration — the entry says
// the document is checked against that Release, names it as a request names it, and
// then gives the same account. The account is read off the failure itself, not off
// any answer, so the first row pins the Release's own entry to the failure's words
// and every other row pins the sentence around them: a defect that dropped the
// Release's name from the sentence fails each of those rows and leaves the first
// green.
//
// Two failures reach the entry, and each has its rows: the closure that does not
// resolve, because the workspace holds no Manifest, and a lockfile that is a link
// leading out of the workspace, which is refused before the resolution runs.
func TestValidateNamesTheReleaseADocumentIsCheckedAgainstWhereItIsNotTheReleasesOwn(t *testing.T) {
	t.Parallel()
	const release = "release-sized.yaml"
	named := "Dependencies: this document is checked against the Release " + release +
		", whose dependencies cannot be resolved: "
	failures := []struct {
		name string
		// arrange turns the workspace into one where the Release fails this way, and
		// returns the failure's own words.
		arrange func(t *testing.T, srv *Server, root string) string
	}{
		{name: "the closure does not resolve",
			arrange: func(t *testing.T, srv *Server, _ string) string {
				releasePath := releasePathAsTheDaemonResolvesIt(t, srv, release)
				_, envelope := srv.resolveReleaseClosure(context.Background(), srv.catalogFor(releasePath).ForRequest(nil), releasePath)
				if envelope == nil || envelope.Message == "" {
					t.Fatal("precondition: the Release's closure resolves, so no row would report a failure")
				}
				return envelope.Message
			}},
		{name: "the lockfile leads out of the workspace",
			arrange: func(t *testing.T, srv *Server, _ string) string {
				lock := lockfile.PathFor(releasePathAsTheDaemonResolvesIt(t, srv, release))
				kept := filepath.Join(t.TempDir(), filepath.Base(lock))
				writeFixtureFile(t, kept, "version: 1\n")
				if err := os.Symlink(kept, lock); err != nil {
					t.Skipf("symbolic links are not available here: %v", err)
				}
				_, err := lockfile.LoadIfReadable(srv.workspaceRoot, lock)
				if err == nil {
					t.Fatal("precondition: the lockfile leading out is read rather than refused")
				}
				return err.Error()
			}},
	}
	for _, failure := range failures {
		t.Run(failure.name, func(t *testing.T) {
			t.Parallel()
			srv, root := entityCheckWorkspace(t)
			account := failure.arrange(t, srv, root)
			cases := []struct {
				name, file, want string
			}{
				{name: "the Release's own document carries the account alone",
					file: release, want: "Dependencies: " + account},
				{name: "another Release names the Release it is checked against",
					file: "release-plain.yaml", want: named + account},
				{name: "the Aggregate names the Release it is checked against",
					file: "aggregate.yaml", want: named + account},
				{name: "the workspace configuration names the Release it is checked against",
					file: workspaceconfig.ConfigFilename, want: named + account},
			}
			// Not parallel: the rows share one daemon.
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					file := filepath.Join(root, tc.file)
					res := validateContentOverTheWire(t, srv, file, mustRead(t, file), release)
					var entries []Diagnostic
					for _, d := range res.Diagnostics {
						if strings.HasPrefix(d.Message, "Dependencies: ") {
							entries = append(entries, d)
						}
					}
					if len(entries) != 1 {
						t.Fatalf("want one dependency entry; the answer carries %q", diagMessages(res.Diagnostics))
					}
					if got := entries[0]; got.Message != tc.want || got.Severity != editorSeverityError ||
						got.Line != 1 || got.Column != 1 {
						t.Errorf("the entry is %q at %d:%d, severity %v; want %q at 1:1, an error",
							got.Message, got.Line, got.Column, got.Severity, tc.want)
					}
				})
			}
		})
	}
}

// releasePathAsTheDaemonResolvesIt is the path the daemon reads the Release a
// request names as release at, resolved by the rule every request's Release goes
// through, so a failure read off it is the failure that request meets.
func releasePathAsTheDaemonResolvesIt(t *testing.T, srv *Server, release string) string {
	t.Helper()
	releasePath, refusal := resolveReleaseParam(srv.workspaceRoot, srv.wsConfig, release)
	if refusal != nil {
		t.Fatalf("resolve the Release %s: %+v", release, refusal)
	}
	return releasePath
}
