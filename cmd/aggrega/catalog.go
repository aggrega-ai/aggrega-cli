// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"github.com/spf13/cobra"
)

// newCatalogCmd builds the `catalog` parent command grouping the
// catalog-registry subcommands.
func newCatalogCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "catalog",
		Short: "Work with the catalog registry",
	}
	c.AddCommand(newCatalogSearchCmd())
	return c
}

// newCatalogSearchCmd builds the `catalog search` command. Without --content it
// searches the configured registry for catalog entities through the shared
// registry-search primitive, scoped to the whole catalog surface. With
// --content it runs an offline full-text query over catalog and documentation
// content, answered by a search-index plugin, entirely without a network.
func newCatalogSearchCmd() *cobra.Command {
	var (
		content        bool
		plugin         string
		limit          int
		filters        map[string]string
		noDeprecations bool
	)
	c := &cobra.Command{
		Use:   "search [query]",
		Short: "Search the registry for catalog entities",
		Long: `Search catalog content in one of two modes.

Without --content, the query is a case-insensitive substring match on the
registry repository path; an empty query lists every catalog repository. The
scope is the whole catalog — every entity kind — unlike 'aggrega plugin
search', which scopes to installable plugins.

With --content, the query is a full-text search over catalog and documentation
content, answered offline by the search-index plugin named with --plugin. The
plugin resolves through the workspace's local, cached, and embedded plugin
tiers — never a network fetch — and returns ranked hits from its embedded
index. --limit caps the result count and --filter key=value (repeatable)
narrows by the plugin's own field vocabulary.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			query := ""
			if len(args) == 1 {
				query = args[0]
			}
			if content {
				return runContentSearch(cmd.Context(), rw, query, plugin, limit, filters, cmd.OutOrStdout())
			}
			return runRegistrySearch(cmd.Context(), rw.Config(), query, catalogSearchScope, !noDeprecations, cmd.OutOrStdout())
		},
	}
	c.Flags().BoolVar(&content, "content", false,
		"Search catalog and documentation content full-text (offline) instead of listing registry repository names")
	c.Flags().StringVar(&plugin, "plugin", "",
		"Search-index plugin URI to query; required with --content")
	c.Flags().IntVar(&limit, "limit", 0,
		"Cap on how many ranked content hits to return; 0 asks the plugin for its own default (--content only)")
	c.Flags().StringToStringVar(&filters, "filter", nil,
		"Field filter key=value narrowing a content search, repeatable (--content only)")
	c.Flags().BoolVar(&noDeprecations, "no-deprecations", false,
		"Skip the per-result deprecation-status probe for faster searches")
	return c
}
