// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInspectPathRefusesAKindWrittenAsAListInTheAuthorsWords holds the refusal of
// a document whose kind is written as a list, read before the document is
// dispatched by kind, to the line, the field and the two shapes.
func TestInspectPathRefusesAKindWrittenAsAListInTheAuthorsWords(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "kind-list.yaml")
	doc := "apiVersion: aggrega.ai/v1alpha1\nkind:\n  - Component\nmetadata:\n  name: example/compute/probe\n"
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	err := inspectPath(context.Background(), nil, path, nil, inspectOptions{})
	if err == nil {
		t.Fatal("a kind written as a list was accepted")
	}
	const want = "line 3 (kind): a list is written where text is expected"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not say %q", err, want)
	}
	if strings.Contains(err.Error(), "cannot unmarshal") {
		t.Errorf("error %q carries the decoder's own text", err)
	}
}
