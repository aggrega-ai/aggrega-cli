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

// newCatalogSearchCmd builds the `catalog search` command: it searches the
// configured registry for catalog entities through the shared registry-search
// primitive, scoped to the whole catalog surface.
func newCatalogSearchCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "search [query]",
		Short: "Search the registry for catalog entities",
		Long: `Search the configured registry for catalog entities.

The optional query is a case-insensitive substring match on the repository
path; an empty query lists every catalog repository. The scope is the whole
catalog — every entity kind — unlike 'aggrega plugin search', which scopes to
installable plugins.`,
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
			return runRegistrySearch(cmd.Context(), rw.Config(), query, catalogSearchScope, cmd.OutOrStdout())
		},
	}
	return c
}
