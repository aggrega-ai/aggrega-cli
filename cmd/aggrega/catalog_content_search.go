// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/searchplugin"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// contentSearchResolver builds the offline plugin-URI resolver the content
// search drives. It is a package-level seam because the live resolver needs a
// resolved workspace the sandbox does not carry: a test injects a fixture
// Lookup, and production builds a workspace plugin-path resolver with a nil
// puller so resolution runs through the embedded, local, and cache tiers only —
// never a registry fetch. The content query is therefore offline by construction.
var contentSearchResolver = func(rw *workspaceconfig.ResolvedWorkspace) (pluginpath.Lookup, error) {
	cfg := rw.Config()
	r := pluginpath.Resolver{
		WorkspaceRoot: rw.Root(),
		CacheDir:      cfg.ResolvedArtifactCacheDir(),
	}
	return r.Resolve, nil
}

// runContentSearch runs the offline full-text content search: it builds the
// offline resolver, drives the shared search runner against the named plugin,
// and writes the ranked hits. It requires a non-empty query and a non-empty
// plugin URI — the content index to query — and never reaches the network: the
// runner loads the plugin under the capability-denied build profile, so the
// guest answers from its embedded index alone.
func runContentSearch(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, query, plugin string, limit int, filters map[string]string, out io.Writer) error {
	if query == "" {
		return errors.New("content search requires a non-empty query")
	}
	if plugin == "" {
		return errors.New("content search requires --plugin naming the search-index plugin URI to query")
	}
	resolve, err := contentSearchResolver(rw)
	if err != nil {
		return fmt.Errorf("content search: build offline plugin resolver: %w", err)
	}
	runner := searchplugin.Runner{Resolve: resolve}
	res, err := runner.Search(ctx, plugin, abi.SearchInput{Query: query, Limit: limit, Filters: filters})
	if err != nil {
		return fmt.Errorf("content search: run plugin %q offline: %w", plugin, err)
	}
	formatContentSearchResults(out, query, res)
	return nil
}

// formatContentSearchResults writes the deterministic ranked content listing in
// the guest's ranking order. A zero-hit search reports the empty result
// explicitly rather than printing a bare blank, so the operator can tell a
// completed empty search from a silent failure.
func formatContentSearchResults(out io.Writer, query string, res abi.SearchOutput) {
	if len(res.Results) == 0 {
		fmt.Fprintf(out, "No content results for %q.\n", query)
		return
	}
	fmt.Fprintf(out, "Content results for %q:\n", query)
	for i, r := range res.Results {
		fmt.Fprintf(out, "  %d. %s [%s] (score %g)\n", i+1, r.Title, r.DocID, r.Score)
		if r.Anchor != "" {
			fmt.Fprintf(out, "     anchor: %s\n", r.Anchor)
		}
		if r.Snippet != "" {
			fmt.Fprintf(out, "     %s\n", r.Snippet)
		}
	}
}
