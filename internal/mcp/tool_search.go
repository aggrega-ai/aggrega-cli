// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/searchplugin"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
)

// searchArgs is the search tool's argument shape.
type searchArgs struct {
	Query          string            `json:"query"`
	Plugin         string            `json:"plugin"`
	Limit          int               `json:"limit"`
	Filters        map[string]string `json:"filters"`
	WorkspaceRoot  string            `json:"workspace_root"`
	ResponseFormat string            `json:"response_format"`
}

// searchHit is one ranked result in the search tool's payload. The fields carry
// the guest's result verbatim; the host interprets none of them.
type searchHit struct {
	DocID   string  `json:"doc_id"`
	Title   string  `json:"title"`
	Anchor  string  `json:"anchor,omitempty"`
	Snippet string  `json:"snippet,omitempty"`
	Score   float64 `json:"score"`
}

// searchResultPayload is the search tool's structured payload: the ranked hits
// the search plugin returned, in the guest's ranking order.
type searchResultPayload struct {
	Query string      `json:"query"`
	Hits  []searchHit `json:"hits"`
}

var searchToolSpec = toolSpec{
	name: "search",
	description: "Run a full-text query against a search-index plugin and return its ranked results, entirely offline. " +
		"Pass the query as `query` and the search plugin's URI as `plugin` (the index plugin to query). The plugin " +
		"resolves through the workspace's local, cached, and embedded plugin tiers — never a network fetch — and answers " +
		"from its embedded index, so results are deterministic and available offline. Each hit carries the document id, " +
		"title, an in-document anchor, a snippet of matching context, and the relevance score the plugin assigned, in " +
		"descending relevance order. Optional `limit` caps the result count and `filters` narrows by the plugin's own " +
		"field vocabulary. If no search plugin resolves, the call fails loudly with guidance rather than reaching for a " +
		"network. Use it to find the right documentation or catalog entry before authoring.",
	inputSchema: `{
  "type": "object",
  "properties": {
    "query": {
      "type": "string",
      "description": "The full-text query the search plugin answers. The plugin owns the query grammar."
    },
    "plugin": {
      "type": "string",
      "description": "The URI of the search-index plugin to query. Resolved offline through the workspace plugin tiers (local, cache, embedded)."
    },
    "limit": {
      "type": "integer",
      "description": "Optional cap on how many ranked results to return. Zero or absent asks the plugin for its own default count."
    },
    "filters": {
      "type": "object",
      "additionalProperties": { "type": "string" },
      "description": "Optional field filters that narrow the search, keyed by the plugin's own field names."
    },
    "workspace_root": {
      "type": "string",
      "description": "Optional workspace root override. Defaults to discovery from the server's working directory."
    },
    "response_format": {
      "type": "string",
      "enum": ["detailed", "concise"],
      "description": "detailed returns each hit's snippet and anchor; concise returns titles and scores. Defaults to detailed."
    }
  },
  "required": ["query", "plugin"]
}`,
	invoke: searchInvoke,
}

func searchInvoke(s *server, ctx context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	var a searchArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: "invalid search arguments: " + err.Error()}
		}
	}
	if strings.TrimSpace(a.Query) == "" {
		return nil, &rpcError{Code: codeInvalidParams, Message: "search requires a non-empty 'query'"}
	}
	if strings.TrimSpace(a.Plugin) == "" {
		return nil, &rpcError{Code: codeInvalidParams, Message: "search requires a 'plugin' URI naming the search-index plugin to query"}
	}

	resolve, rErr := s.searchPluginResolver(a.WorkspaceRoot)
	if rErr != nil {
		return errorResult(explainToolError("Could not build the offline plugin resolver for search.", rErr, "plugin"))
	}

	runner := searchplugin.Runner{Resolve: resolve}
	out, err := runner.Search(ctx, a.Plugin, abi.SearchInput{Query: a.Query, Limit: a.Limit, Filters: a.Filters})
	if err != nil {
		return errorResult(explainToolError(
			fmt.Sprintf("The search plugin %q could not be run offline. Pull or install the search plugin before querying it; search never fetches over the network.", a.Plugin),
			err, "plugin"))
	}

	payload := searchResultPayload{Query: a.Query, Hits: searchHits(out)}
	text := renderSearchText(payload, parseResponseFormat(a.ResponseFormat))
	return textResult(text, payload)
}

// searchPluginResolver returns the plugin-URI resolver the search runner drives.
// The injected seam wins when set (a test's fixture resolver); otherwise a
// workspace plugin-path resolver is built with a nil puller, so resolution runs
// through the embedded, local, and cache tiers only — never a registry fetch.
func (s *server) searchPluginResolver(workspaceRoot string) (searchResolver, error) {
	if s.searchResolve != nil {
		return s.searchResolve, nil
	}
	tw, err := s.resolveToolWorkspace(workspaceRoot, nil)
	if err != nil {
		return nil, err
	}
	r := pluginpath.Resolver{
		WorkspaceRoot: tw.root,
		CacheDir:      tw.cacheDir,
	}
	return r.Resolve, nil
}

// searchHits converts the guest's ranked output into the tool payload, in the
// guest's ranking order (the host preserves order and interprets no field).
func searchHits(out abi.SearchOutput) []searchHit {
	hits := make([]searchHit, 0, len(out.Results))
	for _, r := range out.Results {
		hits = append(hits, searchHit{
			DocID:   r.DocID,
			Title:   r.Title,
			Anchor:  r.Anchor,
			Snippet: r.Snippet,
			Score:   r.Score,
		})
	}
	return hits
}

// renderSearchText renders the human-readable ranked result list. The concise
// form lists title and score; the detailed form adds the anchor and snippet.
func renderSearchText(p searchResultPayload, format string) string {
	if len(p.Hits) == 0 {
		return fmt.Sprintf("Search for %q returned no results.", p.Query)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Search for %q returned %d result(s):\n", p.Query, len(p.Hits))
	for i, h := range p.Hits {
		fmt.Fprintf(&b, "  %d. %s [%s] (score %g)\n", i+1, h.Title, h.DocID, h.Score)
		if format == responseFormatDetailed {
			if h.Anchor != "" {
				fmt.Fprintf(&b, "     anchor: %s\n", h.Anchor)
			}
			if h.Snippet != "" {
				fmt.Fprintf(&b, "     %s\n", h.Snippet)
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
