// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestDispatchReleaseCandidatesByContentKind pins the behavior the IDE
// delegates to: the candidate set is every `kind: Release` document under the
// workspace root, determined by content — not by filename. A Release named
// without a conventional prefix (prod.yaml) IS included; a non-Release file
// named like one (release-notes.yaml) is NOT; a Release in a subdirectory is
// found (the walk is recursive). This is exactly what a basename glob got wrong
// in both directions.
func TestDispatchReleaseCandidatesByContentKind(t *testing.T) {
	root := t.TempDir()
	writeFile := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const release = "apiVersion: aggrega.ai/v1alpha1\nkind: Release\nmetadata:\n  name: x\n"
	// A Release with no conventional prefix — the glob missed it.
	writeFile("prod.yaml", release)
	// A Release in a subdirectory — the shallow glob missed it.
	writeFile("envs/staging.yaml", release)
	// Named like a Release but NOT one — the glob wrongly matched it.
	writeFile("release-notes.yaml", "title: notes\nbody: changelog\n")

	out := &bytes.Buffer{}
	srv := newTestServer(t, root, nil, out)
	req := framedRequest(1, "aggrega/releaseCandidates", map[string]any{})
	var reqBody jsonRPCRequest
	bodyStart := bytes.Index(req, []byte("\r\n\r\n")) + 4
	if err := json.Unmarshal(req[bodyStart:], &reqBody); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if err := srv.dispatchReleaseCandidates(context.Background(), &reqBody); err != nil {
		t.Fatalf("dispatchReleaseCandidates: %v", err)
	}
	resp := decodeOneResponse(t, out)
	var result releaseCandidatesResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	got := append([]string(nil), result.Releases...)
	sort.Strings(got)
	want := []string{filepath.Join(root, "envs", "staging.yaml"), filepath.Join(root, "prod.yaml")}
	if len(got) != len(want) {
		t.Fatalf("releases: got %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("releases[%d]: got %q; want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}
