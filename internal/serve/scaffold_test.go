// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

// The daemon's drafting leg. The oracle throughout is a direct call to the
// shared drafting layer over the same arguments the request carries, so these
// prove the method surfaces that layer faithfully rather than agreeing with a
// second copy of it. Since the tool protocol's leg is held to the same oracle,
// the two surfaces answer the same bytes for the same request without either
// test having to reach into the other's package.

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	schemas "github.com/aggrega-ai/aggrega-schemas"

	"github.com/aggrega-ai/aggrega-cli/internal/scaffoldops"
)

// callScaffoldEntity drives one aggrega/scaffoldEntity request through the
// production dispatch and serializer path and returns the decoded response.
func callScaffoldEntity(t *testing.T, srv *Server, params map[string]any) jsonRPCResponse {
	t.Helper()
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/scaffoldEntity", params))
	return runOneRequest(t, srv, &in, &out)
}

// decodeScaffoldResult decodes a successful response's result payload.
func decodeScaffoldResult(t *testing.T, resp jsonRPCResponse) scaffoldEntityResult {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("aggrega/scaffoldEntity returned an error envelope: %+v", resp.Error)
	}
	var got scaffoldEntityResult
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatalf("decode result: %v (body: %s)", err, resp.Result)
	}
	return got
}

// TestScaffoldEntityAnswersWithTheSharedDraft is the invocation proof: the
// method runs, and what it returns is byte-for-byte what the shared drafting
// layer produces for the same kind and the same name.
//
// The comparison is against the layer rather than against text written here,
// because text written here would be a second statement of the same document and
// could only ever prove that this file and the layer were edited together.
func TestScaffoldEntityAnswersWithTheSharedDraft(t *testing.T) {
	srv := newTestServer(t, t.TempDir(), nil, nil)

	for _, kind := range []string{"component", "aggregate", "release", "recipe", "manifest", "feature", "plugin"} {
		t.Run(kind, func(t *testing.T) {
			want, err := scaffoldops.Skeleton(kind, "drafted-name")
			if err != nil {
				t.Fatalf("the oracle refused %q: %v", kind, err)
			}

			got := decodeScaffoldResult(t, callScaffoldEntity(t, srv, map[string]any{
				"kind": kind,
				"name": "drafted-name",
			}))
			if got.Kind != kind {
				t.Errorf("the answer reports kind %q for a request about %q", got.Kind, kind)
			}
			if got.Skeleton != want {
				t.Errorf("the daemon's draft is not the shared one.\n got:\n%s\nwant:\n%s", got.Skeleton, want)
			}
			if strings.TrimSpace(got.Skeleton) == "" {
				t.Errorf("the answer carries no document")
			}
		})
	}
}

// TestScaffoldEntityAnswersWithoutAName pins the optional half of the request:
// omitting the name is a request in its own right, not a malformed one, and it
// leaves a placeholder where the seed would have gone.
func TestScaffoldEntityAnswersWithoutAName(t *testing.T) {
	srv := newTestServer(t, t.TempDir(), nil, nil)

	want, err := scaffoldops.Skeleton("component", "")
	if err != nil {
		t.Fatalf("the oracle refused: %v", err)
	}
	got := decodeScaffoldResult(t, callScaffoldEntity(t, srv, map[string]any{"kind": "component"}))
	if got.Skeleton != want {
		t.Fatalf("an unnamed draft is not the shared one.\n got:\n%s\nwant:\n%s", got.Skeleton, want)
	}
}

// TestScaffoldEntityRefusesAKindNoSchemaAnswersTo pins that the refusal carries
// the corrective text naming the whole valid set — the thing that lets a client
// fix a typo without a second call — rather than an empty document or a bare
// code.
//
// It also pins the band: an unrecognised kind is a malformed request, which the
// client corrects, and not a state of the workspace it would ask a person to
// repair.
func TestScaffoldEntityRefusesAKindNoSchemaAnswersTo(t *testing.T) {
	srv := newTestServer(t, t.TempDir(), nil, nil)

	resp := callScaffoldEntity(t, srv, map[string]any{"kind": "compoent"})
	if resp.Error == nil {
		t.Fatalf("a kind no schema answers to was answered rather than refused: %s", resp.Result)
	}
	if resp.Error.Code != jsonRPCInvalidParams {
		t.Errorf("the refusal carries code %d, not the invalid-params code %d",
			resp.Error.Code, jsonRPCInvalidParams)
	}
	if resp.Error.Message != scaffoldops.UnknownKindGuidance("compoent") {
		t.Errorf("the refusal is not the shared guidance:\n got: %s\nwant: %s",
			resp.Error.Message, scaffoldops.UnknownKindGuidance("compoent"))
	}
	for _, kind := range schemas.Kinds() {
		if !strings.Contains(resp.Error.Message, kind) {
			t.Errorf("the refusal omits the valid kind %q: %s", kind, resp.Error.Message)
		}
	}
	if len(resp.Result) != 0 {
		t.Errorf("a refused request still carried a result: %s", resp.Result)
	}
}

// TestScaffoldEntityRefusesARequestThatNamesNoKind pins the other refusal. The
// kind is what the answer is derived from, so there is nothing to fall back on
// and nothing sensible to guess; the message names the contract first and the
// fault second, so a client author learns what to send from the one message.
func TestScaffoldEntityRefusesARequestThatNamesNoKind(t *testing.T) {
	srv := newTestServer(t, t.TempDir(), nil, nil)

	for _, tc := range []struct {
		name   string
		params map[string]any
	}{
		{"no kind key at all", map[string]any{}},
		{"an empty kind", map[string]any{"kind": ""}},
		{"a kind that is only space", map[string]any{"kind": "   "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := callScaffoldEntity(t, srv, tc.params)
			if resp.Error == nil {
				t.Fatalf("a request naming no kind was answered: %s", resp.Result)
			}
			if resp.Error.Code != jsonRPCInvalidParams {
				t.Errorf("the refusal carries code %d, not the invalid-params code %d",
					resp.Error.Code, jsonRPCInvalidParams)
			}
			if !strings.Contains(resp.Error.Message, "kind must name the entity kind to draft") {
				t.Errorf("the refusal does not state the contract: %s", resp.Error.Message)
			}
		})
	}
}

// TestScaffoldEntityAnswersOnAWorkspaceHoldingNothing pins the reach the method
// deliberately does not have. Every other method here is answered from a
// document the session opened or from the workspace root it resolved; this one
// is answered from the schema set the binary carries, so an empty directory is
// enough — which is exactly the workspace an author starting a first document is
// sitting in.
func TestScaffoldEntityAnswersOnAWorkspaceHoldingNothing(t *testing.T) {
	empty := t.TempDir()
	srv := newTestServer(t, empty, nil, nil)

	got := decodeScaffoldResult(t, callScaffoldEntity(t, srv, map[string]any{
		"kind": "aggregate",
		"name": "first-document",
	}))
	if !strings.Contains(got.Skeleton, "first-document") {
		t.Fatalf("the draft did not carry the requested name:\n%s", got.Skeleton)
	}
}

// TestScaffoldEntityWritesNothing is the property the reference page states and
// the one this method exists to keep: the answer is a document to write, not a
// document that was written, so the moment a new file reaches disk stays with
// the client.
//
// It drafts every kind the binary carries rather than one, because a write path
// added for a single kind would slip past a sampled check. The workspace holds a
// file before the requests so the snapshot is discriminating over content as
// well as over presence — an empty directory compared against an empty directory
// would pass on a snapshot helper that read nothing.
func TestScaffoldEntityWritesNothing(t *testing.T) {
	ws := t.TempDir()
	if err := writeScaffoldProbeFile(ws); err != nil {
		t.Fatalf("seed the workspace: %v", err)
	}
	srv := newTestServer(t, ws, nil, nil)

	before := treeSnapshot(t, ws)
	if len(before) == 0 {
		t.Fatal("the snapshot read nothing, so comparing it against itself would assert nothing")
	}
	beforeDirs := scaffoldWorkspaceDirs(t, ws)

	for _, kind := range schemas.Kinds() {
		resp := callScaffoldEntity(t, srv, map[string]any{"kind": kind, "name": "written-nowhere"})
		if resp.Error != nil {
			t.Fatalf("aggrega/scaffoldEntity refused %q: %+v", kind, resp.Error)
		}
	}

	if after := treeSnapshot(t, ws); !reflect.DeepEqual(before, after) {
		t.Fatalf("drafting changed the workspace's files.\nbefore: %v\nafter:  %v", before, after)
	}
	if after := scaffoldWorkspaceDirs(t, ws); !reflect.DeepEqual(beforeDirs, after) {
		t.Fatalf("drafting changed the workspace's directories.\nbefore: %v\nafter:  %v", beforeDirs, after)
	}
}

// writeScaffoldProbeFile puts one file in the workspace so the content snapshot
// above has something to compare. Without it the check would run over an empty
// directory, where a snapshot that read nothing at all would still compare equal
// to itself.
func writeScaffoldProbeFile(root string) error {
	return os.WriteFile(filepath.Join(root, "already-here.yaml"), []byte("kind: NotAggrega\n"), 0o644)
}

// scaffoldWorkspaceDirs lists every directory under root, root-relative and
// sorted.
//
// It sits beside the content snapshot rather than replacing it. That snapshot
// records regular files only, so a directory created and left empty moves
// nothing in it — and "no file was written" is the weaker claim when a path can
// appear without one.
func scaffoldWorkspaceDirs(t *testing.T, root string) []string {
	t.Helper()
	var dirs []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		dirs = append(dirs, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(dirs)
	return dirs
}
