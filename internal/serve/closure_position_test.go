// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
)

// closureFaultCorpus is the set of Release documents whose closure
// resolution fails, one per failing stage that reaches the wire. Each row
// carries the whole authored workspace, so the position a row asserts can
// be read against the document text sitting beside it rather than against
// a coordinate nobody can check.
//
// The rows with a wantEnd* pair are the stages whose fault is attributable
// to a field OF THE RELEASE — the document the envelope's file field
// already names — so the span is derivable. The rows without one are the
// honest point-or-file-level cases, and each states why it has no span.
type closureFaultRow struct {
	name string
	// files is the workspace content, keyed by workspace-relative path.
	files map[string]string
	// release is the workspace-relative Release the request names.
	release string
	// wantLine, wantColumn, wantEndLine, wantEndColumn are the 1-based span
	// the envelope must carry. All four zero means the envelope must carry
	// no position at all.
	wantLine, wantColumn, wantEndLine, wantEndColumn int
	// wantPrefix and wantMessage pin the refusal's own text, held alongside
	// the span deliberately. This surface moves WHERE a fault is drawn and
	// must move nothing about WHAT is reported, so the two travel in one row:
	// a change that reworded a refusal while relocating it fails here rather
	// than passing a span-only assertion.
	//
	// The text is pinned from BOTH ends because neither end alone is enough,
	// and that was measured rather than assumed. A containment check is blind
	// to a reword that only PREPENDS — "load manifest …" still contains
	// itself after becoming "could not load manifest …" — so the opening is
	// anchored as a prefix. A prefix alone stops at the first temporary
	// directory the message names, which is why the condition's own sentence
	// is pinned separately as a fragment.
	wantPrefix  string
	wantMessage string
	// why records the reason a row carries no span, so a later reader does
	// not mistake an honest file-level emission for a missing feature.
	why string
}

const corpusManifest = `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: corpus-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type: {}
`

const corpusAggregate = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: corpus-aggregate
  version: "1.0.0"
spec:
  components: {}
`

func closureFaultCorpus() []closureFaultRow {
	return []closureFaultRow{
		{
			// The Release's manifest reference names a file that is not
			// there. The fault belongs to `spec.manifest`, so that key is
			// what an editor underlines.
			name: "manifest reference names no file",
			files: map[string]string{
				"release.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: corpus-release
  environment: test
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./absent-manifest.yaml
`,
				"aggregate.yaml": corpusAggregate,
				"release.lock":   "",
			},
			release:     "release.yaml",
			wantPrefix:  `load manifest "./absent-manifest.yaml": file load failed:`,
			wantMessage: "the file could not be read, or its bytes did not parse",
			// `  manifest: …` sits on line 9 at column 3; the key token is
			// 8 runes, so the end column is 11.
			wantLine: 9, wantColumn: 3, wantEndLine: 9, wantEndColumn: 11,
		},
		{
			// The manifest resolves; the aggregate reference does not. The
			// fault moves to `spec.aggregate` and so does the span.
			name: "aggregate reference names no file",
			files: map[string]string{
				"release.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: corpus-release
  environment: test
  version: "1.0.0"
spec:
  manifest: ./manifest.yaml
  aggregate: ./absent-aggregate.yaml
`,
				"manifest.yaml": corpusManifest,
				"release.lock":  "",
			},
			release:     "release.yaml",
			wantPrefix:  `load aggregate "./absent-aggregate.yaml": file load failed:`,
			wantMessage: "the file could not be read, or its bytes did not parse",
			// `  aggregate: …` sits on line 9 at column 3; the key token is
			// 9 runes, so the end column is 12.
			wantLine: 9, wantColumn: 3, wantEndLine: 9, wantEndColumn: 12,
		},
		{
			// A child Release inherits its manifest reference from the base
			// it extends and declares none of its own, so the walk cannot
			// reach `.spec.manifest` and lands on the nearest key that does
			// exist. That is the documented degradation of the shared walk:
			// a coarser position, never a wrong one.
			name: "manifest reference is inherited, so the walk degrades to the enclosing key",
			files: map[string]string{
				"release.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: corpus-child-release
  environment: test
  version: "1.0.0"
spec:
  extends: ./base.yaml
`,
				"base.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: corpus-base-release
  environment: test
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
`,
				"aggregate.yaml": corpusAggregate,
				"manifest.yaml":  corpusManifest,
				"release.lock":   "",
			},
			release:     "release.yaml",
			wantPrefix:  `load manifest "": aggregate/manifest reference unresolvable:`,
			wantMessage: `no Manifest named "" in the Release directory, catalog, or registry`,
			// `spec:` sits on line 7 at column 1; the key token is 4 runes,
			// so the end column is 5.
			wantLine: 7, wantColumn: 1, wantEndLine: 7, wantEndColumn: 5,
		},
		{
			name: "release document does not parse",
			files: map[string]string{
				"release.yaml": "apiVersion: aggrega.ai/v1alpha1\nkind: Release\nspec:\n  manifest: [unclosed\n",
				"release.lock": "",
			},
			release:     "release.yaml",
			wantPrefix:  "load release ",
			wantMessage: "document envelope validation failed: envelope: parse",
			why:         "the Release itself failed to load, so no stage names a key and there is no parsed document to walk",
		},
		{
			name: "release carries no spec block at all",
			files: map[string]string{
				"release.yaml": `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: corpus-release
  environment: test
  version: "1.0.0"
`,
				"release.lock": "",
			},
			release:     "release.yaml",
			wantPrefix:  "load release ",
			wantMessage: "spec: missing required property: spec. correct the document to match its kind's schema",
			why:         "the document envelope refuses a Release with no spec before any stage that names a key runs",
		},
	}
}

// writeCorpusWorkspace materialises one row's files under a fresh temp
// root and returns the root.
func writeCorpusWorkspace(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return root
}

// irGraphFaultEnvelope drives aggrega/irGraph against the row's workspace
// and returns the refusal's code, message and decoded data payload. It
// goes through the request path production uses, so what it reports is
// what a client receives rather than what a helper returns.
func irGraphFaultEnvelope(t *testing.T, row closureFaultRow) (int, string, errorData) {
	t.Helper()
	root := writeCorpusWorkspace(t, row.files)
	var out bytes.Buffer
	srv := newTestServer(t, root, nil, &out)
	var in bytes.Buffer
	in.Write(framedRequest(1, "aggrega/irGraph", irGraphParams{Release: row.release}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error == nil {
		t.Fatalf("expected a refusal for %q; got result: %s", row.name, resp.Result)
	}
	var data errorData
	if resp.Error.Data != nil {
		raw, err := json.Marshal(resp.Error.Data)
		if err != nil {
			t.Fatalf("re-marshal data: %v", err)
		}
		if err := json.Unmarshal(raw, &data); err != nil {
			t.Fatalf("decode data: %v", err)
		}
	}
	return resp.Error.Code, resp.Error.Message, data
}

// TestIrGraphClosureFailureCarriesTheOffendingSpan is the corpus proof for
// the error-envelope span: a closure-resolution failure attributable to a
// Release key carries that key's start AND end position, and every other
// failing stage keeps the file-level shape rather than inventing one.
//
// The refusal's code and its own text are asserted in the same rows as the
// span, which is what makes this a before-and-after instrument rather than a
// span check. Relocating where a fault is drawn must move nothing about what
// is reported, and the failure it guards against is the plausible one: a
// change that positions a refusal and rewords it in the same edit. Each row
// also logs its whole envelope, so a diff of the logged lines across a change
// shows at a glance which field moved.
func TestIrGraphClosureFailureCarriesTheOffendingSpan(t *testing.T) {
	t.Parallel()
	for _, row := range closureFaultCorpus() {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			code, message, data := irGraphFaultEnvelope(t, row)
			t.Logf("CORPUS %s | code=%d | line=%d column=%d end_line=%d end_column=%d | file=%q | message=%s",
				row.name, code, data.Line, data.Column, data.EndLine, data.EndColumn, filepath.Base(data.File), message)

			if code != errEntityNotResolvable {
				t.Errorf("code = %d; want %d — this surface relocates a fault and never reclassifies one", code, errEntityNotResolvable)
			}
			if !strings.HasPrefix(message, row.wantPrefix) {
				t.Errorf("message should still open with %q; got %q", row.wantPrefix, message)
			}
			if !strings.Contains(message, row.wantMessage) {
				t.Errorf("message should still carry %q; got %q", row.wantMessage, message)
			}
			if filepath.Base(data.File) != filepath.Base(row.release) {
				t.Errorf("file = %q; want the Release the request named, %q", data.File, row.release)
			}
			if got := [4]int{data.Line, data.Column, data.EndLine, data.EndColumn}; got != [4]int{row.wantLine, row.wantColumn, row.wantEndLine, row.wantEndColumn} {
				t.Errorf("span = %d:%d-%d:%d; want %d:%d-%d:%d",
					got[0], got[1], got[2], got[3],
					row.wantLine, row.wantColumn, row.wantEndLine, row.wantEndColumn)
			}
			if row.why != "" && (data.EndLine != 0 || data.EndColumn != 0) {
				t.Errorf("row carries an end position it should not: %s", row.why)
			}
		})
	}
}

// TestClosureStageFieldAttribution pins which closure-resolution stages
// are attributable to a Release key and which are deliberately left
// unpositioned. It is a table over the whole stage set rather than over the
// positioned ones, because the value of this contract is in the stages that
// claim NOTHING: each of those would be a fabricated position if it were
// wired, and a table covering only the wired stages could not tell a
// deliberate omission from a forgotten one.
func TestClosureStageFieldAttribution(t *testing.T) {
	t.Parallel()
	cases := []struct {
		stage compilerops.ClosureStage
		name  string
		want  string
		why   string
	}{
		{
			stage: compilerops.ClosureStageResolveManifest,
			name:  "resolve manifest",
			want:  ".spec.manifest",
		},
		{
			stage: compilerops.ClosureStageComposeManifest,
			name:  "compose manifest extends chain",
			want:  ".spec.manifest",
		},
		{
			stage: compilerops.ClosureStageLoadAggregate,
			name:  "load aggregate",
			want:  ".spec.aggregate",
		},
		{
			stage: compilerops.ClosureStageLoadRelease,
			name:  "load release",
			want:  "",
			why:   "the document that would be walked is the one that failed to load",
		},
		{
			stage: compilerops.ClosureStageComputeClosure,
			name:  "compute closure",
			want:  "",
			why:   "the fault spans the catalog rather than one document",
		},
		{
			stage: compilerops.ClosureStageManifestReferenceShape,
			name:  "manifest reference shape",
			want:  "",
			why:   "the fault belongs to the Manifest, so a Release key would be a false anchor",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := releaseFieldForClosureStage(tc.stage)
			if got != tc.want {
				t.Errorf("field = %q; want %q", got, tc.want)
			}
			if tc.want == "" && got != "" {
				t.Errorf("stage claims a key it must not: %s", tc.why)
			}
		})
	}
}

// TestClosureErrorDataFallsBackWhenTheWalkResolvesNothing proves the
// unresolvable-walk fallback directly, because the request path cannot reach
// it: a stage that names a Release key runs only after the document envelope
// has accepted the Release, and that gate requires a spec mapping — which is
// exactly what the walk needs in order to resolve. So the branch is
// unreachable end-to-end BY CONSTRUCTION rather than untested, and this is
// where it is exercised.
//
// The two other misses are covered here too, because neither is reachable
// end-to-end either: a Release that resolves far enough to name a key is by
// then a file that reads and parses.
func TestClosureErrorDataFallsBackWhenTheWalkResolvesNothing(t *testing.T) {
	t.Parallel()
	// A document with no root mapping key the path's first segment can
	// match. The walk returns nothing for `.spec.manifest`, so the payload
	// must keep its file-level shape.
	const noSpec = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: corpus-release
`
	root := t.TempDir()
	present := filepath.Join(root, "release.yaml")
	if err := os.WriteFile(present, []byte(noSpec), 0o644); err != nil {
		t.Fatalf("write release: %v", err)
	}
	unparseable := filepath.Join(root, "broken.yaml")
	if err := os.WriteFile(unparseable, []byte("spec:\n  manifest: [unclosed\n"), 0o644); err != nil {
		t.Fatalf("write broken release: %v", err)
	}

	srv := newTestServer(t, root, nil, &bytes.Buffer{})
	cases := []struct {
		name string
		path string
	}{
		{name: "the walk resolves nothing for the path", path: present},
		{name: "the file no longer parses", path: unparseable},
		{name: "the file no longer reads", path: filepath.Join(root, "vanished.yaml")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := srv.closureErrorData(tc.path, compilerops.ClosureStageResolveManifest)
			want := newErrorData(tc.path, 0, 0)
			if *got != *want {
				t.Errorf("payload = %+v; want the file-level shape %+v", *got, *want)
			}
		})
	}
}
