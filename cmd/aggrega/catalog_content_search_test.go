// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// withContentSearchResolver points the content-search resolver seam at a fixture
// Lookup that returns the given guest bytes for the duration of the test, so the
// content search runs offline without a workspace plugin cascade or a network.
func withContentSearchResolver(t *testing.T, guest []byte) {
	t.Helper()
	prev := contentSearchResolver
	contentSearchResolver = func(*workspaceconfig.ResolvedWorkspace) (pluginpath.Lookup, error) {
		return func(string) (pluginpath.ResolvedPlugin, error) {
			return pluginpath.ResolvedPlugin{Bytes: guest}, nil
		}, nil
	}
	t.Cleanup(func() { contentSearchResolver = prev })
}

// TestCatalogSearchContentOffline drives `catalog search --content` against the
// fixture search guest through the injected offline resolver, asserting ranked
// hits are printed with no network. The nil-puller offline load path handles
// the rest — the injected resolver hands the guest bytes directly.
func TestCatalogSearchContentOffline(t *testing.T) {
	guest := wasmtest.EnsureFixtureBytes(t, "search-go.wasm")
	withContentSearchResolver(t, guest)

	cmd := newCatalogSearchCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(withWorkspaceResolver(context.Background(), t.TempDir()))
	cmd.SetArgs([]string{"--content", "compiler render", "--plugin", "test://search-fixture"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("content search failed: %v\noutput:\n%s", err, out.String())
	}

	got := out.String()
	if !strings.Contains(got, "Content results for") {
		t.Fatalf("expected the content-results header:\n%s", got)
	}
	// The fixture guest ranks the pipeline doc first (both terms match), then
	// the two single-term matches; assert the ranked hits appear in order.
	wantOrdered := []string{
		"architecture/pipeline",
		"architecture/invariants",
		"language-reference/recipe",
	}
	prev := -1
	for _, doc := range wantOrdered {
		at := strings.Index(got, doc)
		if at < 0 {
			t.Fatalf("expected ranked hit %q in output:\n%s", doc, got)
		}
		if at < prev {
			t.Fatalf("ranked hit %q printed out of guest order:\n%s", doc, got)
		}
		prev = at
	}
}

// TestCatalogSearchContentRequiresPlugin asserts a content search without
// --plugin fails with an actionable error naming the missing flag, rather than
// silently degrading or reaching for a network.
func TestCatalogSearchContentRequiresPlugin(t *testing.T) {
	cmd := newCatalogSearchCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(withWorkspaceResolver(context.Background(), t.TempDir()))
	cmd.SetArgs([]string{"--content", "anything"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("content search without --plugin should return an error")
	}
	if !strings.Contains(err.Error(), "--plugin") {
		t.Fatalf("error should name the missing --plugin flag: %v", err)
	}
}
