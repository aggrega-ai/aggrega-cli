// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package externalinputs_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/externalinputs"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathsegmenttest"
)

// TestEveryNameTheStoreWritesIsOneEveryPlatformAccepts acquires an external input
// through the fetch a render's acquisition runs, so the store writes the blob
// under the name it derives from the input's digest, and holds every name beneath
// the store to the one validity predicate.
func TestEveryNameTheStoreWritesIsOneEveryPlatformAccepts(t *testing.T) {
	t.Parallel()
	srv, _ := countingServer(t, []byte("acquired payload bytes"))
	root := t.TempDir()
	svc := externalinputs.NewService(root, true, &http.Client{}, nil)
	if _, err := svc.Fetch(context.Background(), srv.URL+"/Input:File.yaml", "recipe.helm.demo"); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if judged := pathsegmenttest.RequireTreeValid(t, root); judged != 2 {
		t.Errorf("judged %d names, want the digest directory and the blob", judged)
	}
}
