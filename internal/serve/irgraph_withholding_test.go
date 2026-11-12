// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// withholdingFixtureDir is a committed workspace whose author declared values
// sensitive at every position the graph carries authored data: a node's own
// value, an attachment on the node's own block, an attachment a connection
// carries, and the infra the Release writes on each attachment. Each declared
// value is a planted string, so a value reaching an answer is found by name.
const withholdingFixtureDir = "../compiler/testdata/governance-withholding"

// withholdingPlanted are the fixture's declared-sensitive values, one per
// position. Each is absent from a withheld answer and present in a revealed one.
var withholdingPlanted = []string{
	"planted-node-own-value",
	"planted-component-credential",
	"planted-connection-credential",
	"planted-release-infra",
	"planted-connection-infra",
}

// withholdingPublic are values in the same payloads that no declaration covers.
// They are in every answer, revealed or not, which is what makes the absence
// assertions above them capable of failing: a search that could not see into
// the answer would miss these too.
var withholdingPublic = []string{"db-key", "gpt-key", "weekly", "daily"}

// withholdingGraphWorkspace copies the fixture into a fresh workspace laid out
// the way the test server reads one — the catalog at catalog/, the Release, its
// Aggregate and its Manifest at the root — and writes the lockfile the graph
// request requires beside the Release, pinning the two Components by their
// content digest as a pull would record them.
func withholdingGraphWorkspace(t *testing.T) (ws, releaseRel string) {
	t.Helper()
	ws = t.TempDir()
	copyWithholdingFixture(t, filepath.Join(withholdingFixtureDir, "catalog", "base"), filepath.Join(ws, "catalog"))
	for _, name := range []string{"aggregate.yaml", "manifest.yaml", "release.yaml"} {
		body, err := os.ReadFile(filepath.Join(withholdingFixtureDir, name))
		if err != nil {
			t.Fatalf("read fixture %s: %v", name, err)
		}
		writeFixtureFile(t, filepath.Join(ws, name), string(body))
	}

	var entities []lockfile.Entity
	for _, c := range []struct{ uri, file string }{
		{"withhold/gateway", "gateway.yaml"},
		{"withhold/model", "model.yaml"},
	} {
		body, err := os.ReadFile(filepath.Join(ws, "catalog", "components", c.file))
		if err != nil {
			t.Fatalf("read the copied Component %s: %v", c.file, err)
		}
		entities = append(entities, lockfile.Entity{Kind: "component", URI: c.uri, ResolvedVersion: "1.0.0", Digest: contentDigestHex(body)})
	}
	releasePath := filepath.Join(ws, "release.yaml")
	if err := lockfile.WriteLockfile(lockfile.PathFor(releasePath), &lockfile.Lockfile{
		Version:     1,
		Release:     "./release.yaml",
		GeneratedAt: "2026-10-04T00:00:00Z",
		Entities:    entities,
	}); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}
	return ws, "release.yaml"
}

// copyWithholdingFixture copies a fixture directory tree into dst.
func copyWithholdingFixture(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), body, 0o644)
	})
	if err != nil {
		t.Fatalf("copy fixture %s: %v", src, err)
	}
}

// irGraphAnswerFor drives one build-graph request, with params exactly as
// given, through the production dispatch path and returns the graph it answered
// with. A map is accepted as well as the request struct, so a row can put a
// field on the wire that the struct's own encoding would omit.
func irGraphAnswerFor(t *testing.T, ws string, params any) string {
	t.Helper()
	var in, out bytes.Buffer
	srv := newPinIsolationServer(t, ws, &in, &out, 1)
	in.Write(framedRequest(1, "aggrega/irGraph", params))
	srv.in = bufio.NewReader(&in)
	srv.out = &out
	body, err := readMessage(srv.in)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	var req jsonRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	driveHandle(t, srv, &req, &out)
	graph := extractGraph(t, &out)
	if graph == nil {
		t.Fatal("the build-graph request produced no graph, so nothing below observes the withholding")
	}
	return string(graph)
}

// TestServeIrGraphWithholdsDeclaredValuesUnlessAsked is the daemon's half of
// the rule every surface handing the graph out follows: the values an author
// declared sensitive are withheld unless the caller asks for them, and asking
// is one field of one name.
//
// The rows cover the three ways a request can stand on the field — absent, put
// on the wire as false, and true — because absent and false must be the same
// answer, and the struct's own encoding never sends false, so only a row that
// writes the field by hand puts that case on the wire at all.
func TestServeIrGraphWithholdsDeclaredValuesUnlessAsked(t *testing.T) {
	t.Parallel()

	for _, row := range []struct {
		name     string
		params   func(release string) any
		revealed bool
	}{
		{"a request that names no reveal", func(r string) any { return irGraphParams{Release: r} }, false},
		{"a request that sends the reveal as false", func(r string) any { return map[string]any{"release": r, "unredacted": false} }, false},
		{"a request that asks for the reveal", func(r string) any { return irGraphParams{Release: r, Unredacted: true} }, true},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			ws, rel := withholdingGraphWorkspace(t)
			graph := irGraphAnswerFor(t, ws, row.params(rel))

			for _, value := range withholdingPlanted {
				if got := strings.Contains(graph, value); got != row.revealed {
					t.Errorf("the answer carries %q: %v, want %v\n--- graph ---\n%s", value, got, row.revealed, graph)
				}
			}
			if got := strings.Contains(graph, redact.WithheldMarkerKey()); got == row.revealed {
				t.Errorf("the answer carries the withheld marker: %v, want %v", got, !row.revealed)
			}
			for _, value := range withholdingPublic {
				if !strings.Contains(graph, value) {
					t.Errorf("the answer does not carry %q, which no declaration covers; withholding it declares public data secret", value)
				}
			}
		})
	}
}

// TestServeIrGraphDocumentLeavesThePipelineGraphUnmutated proves the answer is
// computed on a copy. The graph handed to the serializer is the pipeline's own,
// so a withholding that wrote its markers into it in place would reach every
// later reader of the same run while each answer still looked right.
//
// It drives the daemon's own preview to obtain the graph, rather than building
// one, so what is asserted unmutated is the graph production hands over.
func TestServeIrGraphDocumentLeavesThePipelineGraphUnmutated(t *testing.T) {
	t.Parallel()
	ws, rel := withholdingGraphWorkspace(t)
	var in, out bytes.Buffer
	srv := newPinIsolationServer(t, ws, &in, &out, 1)

	preview, env := srv.previewRelease(context.Background(), rel, srv.irGraphPipelineOptions())
	if env != nil {
		t.Fatalf("previewRelease %s: %s", rel, env.Message)
	}
	graph := preview.pipeline.Completed().IRGraph
	compiled, err := json.Marshal(graph)
	if err != nil {
		t.Fatalf("marshal the pipeline's graph: %v", err)
	}
	for _, value := range withholdingPlanted {
		if !bytes.Contains(compiled, []byte(value)) {
			t.Fatalf("precondition: the pipeline's graph does not carry %q, so an unmutated graph cannot be told from a withheld one", value)
		}
	}

	for _, row := range []struct {
		name       string
		unredacted bool
	}{
		{"the withheld answer", false},
		{"the revealed answer", true},
	} {
		t.Run(row.name, func(t *testing.T) {
			doc, err := irGraphDocument(graph, row.unredacted)
			if err != nil {
				t.Fatalf("irGraphDocument: %v", err)
			}
			after, err := json.Marshal(graph)
			if err != nil {
				t.Fatalf("marshal the pipeline's graph again: %v", err)
			}
			if !bytes.Equal(after, compiled) {
				t.Errorf("answering mutated the pipeline's own graph\n--- before ---\n%s\n--- after ---\n%s", compiled, after)
			}
			if row.unredacted && !bytes.Equal(doc, compiled) {
				t.Errorf("the revealed answer is not the graph as compiled\n--- answer ---\n%s\n--- compiled ---\n%s", doc, compiled)
			}
			if !row.unredacted {
				for _, value := range withholdingPlanted {
					if bytes.Contains(doc, []byte(value)) {
						t.Errorf("the withheld answer carries %q", value)
					}
				}
			}
		})
	}
}
