// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// indexStaleAfter is the fixed age beyond which a loaded index is considered
// clearly old and search prints a one-line advisory to refresh it. It is a plain
// constant, not a configurable or tuned threshold: the advisory only nudges; it
// never forces a network refresh.
const indexStaleAfter = 30 * 24 * time.Hour

// searchNow supplies the clock the staleness advisory compares a snapshot's age
// against. It is a package seam so tests pin freshness deterministically.
var searchNow = time.Now

// searchIndexesStale reports whether any loaded snapshot is clearly old — its
// timestamp is absent, or older than indexStaleAfter relative to now — so search
// can print a single advisory line to refresh. It reads only the already-loaded
// timestamps and contacts no network.
func searchIndexesStale(indexes []loadedIndex, now time.Time) bool {
	for _, li := range indexes {
		if !li.HasTimestamp || now.Sub(li.LastUpdated) > indexStaleAfter {
			return true
		}
	}
	return false
}

// newSearchCmd builds `search [query]`, which searches the local index of the
// configured registries — built offline by `aggrega registry update` — for
// entities matching a free-text query and the given facet filters.
func newSearchCmd() *cobra.Command {
	var opts searchOptions
	c := &cobra.Command{
		Use:   "search [query]",
		Short: "Search the local index of configured registries",
		Long: `Search the local index of the configured registries for entities matching a
free-text query and optional facet filters.

The index is the offline snapshot written by 'aggrega registry update'; search
reads it and contacts no network. Run 'aggrega registry update' first, and again
whenever you want to pick up newly published entities. A freshness line naming
each searched registry and when its index was last updated is written to standard
error, so standard output carries only the result data.

The optional positional query is a case-insensitive substring matched against
each entity's name and description. The facet filters narrow the result further,
combining as AND across facets and OR within a repeatable facet:

  --kind          entity kind (component, feature, recipe, featurerecipe,
                  workload, plugin); repeatable, case-insensitive
  --capability    a plugin capability; repeatable
  --plugin-type   a plugin socket type (for example provider, governance)
  -l, --selector  a label selector over the entity's catalog labels

Category is matched through the label selector, not a dedicated flag: a catalog
category is the label component.aggrega.ai/category, so
'-l component.aggrega.ai/category=compute' scopes the result to that category.

With no --registry, every configured registry's index is searched and the
results are merged. With --registry (a host or a friendly alias), only that
registry's index is searched.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			if len(args) == 1 {
				opts.Query = args[0]
			}
			return searchRun(rw, cmd.OutOrStdout(), cmd.ErrOrStderr(), opts)
		},
	}
	c.Flags().StringArrayVar(&opts.Kinds, "kind", nil, "Entity kind to match (component|feature|recipe|featurerecipe|workload|plugin). Repeatable; a record matching any one is kept. Case-insensitive.")
	c.Flags().StringVarP(&opts.Selector, "selector", "l", "", "Label selector over the entity's catalog labels. Category is a label: -l component.aggrega.ai/category=compute.")
	c.Flags().StringArrayVar(&opts.Capabilities, "capability", nil, "Plugin capability to match. Repeatable; a record declaring any one is kept.")
	c.Flags().StringVar(&opts.PluginType, "plugin-type", "", "Plugin socket type to match (for example provider, governance, linter).")
	c.Flags().StringVar(&opts.Registry, "registry", "", "Search only this registry, addressed by host or friendly alias. Default searches every configured registry.")
	c.Flags().IntVar(&opts.Limit, "limit", 0, "Cap the number of results (0 means no cap).")
	c.Flags().StringVarP(&opts.Output, "output", "o", "table", "Output format: table|json|names.")
	return c
}

// searchOptions is the input contract for `aggrega search`, populated by the
// cobra command from its optional positional query and its flags.
type searchOptions struct {
	Query        string
	Kinds        []string
	Selector     string
	Capabilities []string
	PluginType   string
	// Registry is an optional single-registry token (host or alias). Empty
	// searches every configured registry.
	Registry string
	// Limit caps the result count; 0 means no cap.
	Limit int
	// Output is the result format: table, json, or names.
	Output string
}

// searchRun resolves the target registry host set, loads each host's local
// index, aggregates and filters the records, and writes the result in the
// requested format.
//
// The freshness line for each loaded host is written to errOut — every run,
// every format — so stdout carries only the machine-consumable result data and
// the json and names formats stay byte-clean.
//
// Host-level absence handling depends on which path selected the hosts. When the
// operator targets a single registry, a missing index there is a loud returned
// error naming the index build. When no argument selects every configured
// registry, a host with no index yet is noted on errOut and the search continues
// to the next host. If the effective host set is non-empty but not one index
// loaded, the run fails loudly rather than presenting an empty result as a
// successful search.
func searchRun(rw *workspaceconfig.ResolvedWorkspace, out, errOut io.Writer, opts searchOptions) error {
	if _, err := parseFormat("search", opts.Output, "table", "json", "names"); err != nil {
		return err
	}

	cfg := rw.Config()

	singleExplicit := opts.Registry != ""
	var hosts []string
	if singleExplicit {
		host, err := cfg.ResolveRegistry(opts.Registry)
		if err != nil {
			return fmt.Errorf("aggrega search: %w", err)
		}
		hosts = []string{host}
	} else {
		for _, r := range cfg.Registries {
			hosts = append(hosts, r.Host)
		}
	}
	if len(hosts) == 0 {
		fmt.Fprintln(out, "No registries configured.")
		return nil
	}

	root := rw.Root()
	cacheDir := cfg.ResolvedArtifactCacheDir()

	var loaded []loadedIndex
	for _, host := range hosts {
		idx, err := loadHostIndex(root, cacheDir, host)
		if err != nil {
			if errors.Is(err, errIndexNotBuilt) {
				if singleExplicit {
					return fmt.Errorf("aggrega search: registry %s has no local index; run 'aggrega registry update'", host)
				}
				fmt.Fprintf(errOut, "registry %s: no local index; run 'aggrega registry update'\n", host)
				continue
			}
			return fmt.Errorf("aggrega search: %w", err)
		}
		loaded = append(loaded, idx)
	}
	if len(loaded) == 0 {
		return fmt.Errorf("aggrega search: no local index found for any configured registry; run 'aggrega registry update'")
	}

	// Freshness lines to errOut, hosts ascending, so the order is deterministic
	// regardless of config order and stdout stays result-only.
	byHost := make([]loadedIndex, len(loaded))
	copy(byHost, loaded)
	sort.Slice(byHost, func(i, j int) bool { return byHost[i].Host < byHost[j].Host })
	for _, li := range byHost {
		if li.HasTimestamp {
			fmt.Fprintf(errOut, "Index for %s last updated %s\n", li.Host, li.LastUpdated.Format(time.RFC3339))
		} else {
			fmt.Fprintf(errOut, "Index for %s last updated (timestamp unavailable)\n", li.Host)
		}
	}

	// Thin staleness surface: advise, never force. When a loaded snapshot is
	// clearly old (or carries no timestamp) a one-line nudge to refresh is written
	// to errOut. Search itself never contacts the network; the refresh it points at
	// is the cheap conditional 'registry update'.
	if searchIndexesStale(byHost, searchNow()) {
		fmt.Fprintln(errOut, "One or more indexes are stale; run 'aggrega registry update' to refresh.")
	}

	var records []IndexRecord
	for _, li := range loaded {
		records = append(records, li.Records...)
	}
	sortIndexRecords(records)

	matched, err := filterRecords(records, searchFilter{
		Query:        opts.Query,
		Kinds:        opts.Kinds,
		Selector:     opts.Selector,
		Capabilities: opts.Capabilities,
		PluginType:   opts.PluginType,
	})
	if err != nil {
		return err
	}

	if opts.Limit > 0 && len(matched) > opts.Limit {
		matched = matched[:opts.Limit]
	}

	switch opts.Output {
	case "json":
		return writeSearchJSON(out, byHost, matched)
	case "names":
		for _, rec := range matched {
			fmt.Fprintln(out, rec.URI)
		}
		return nil
	default:
		return writeSearchTable(out, matched)
	}
}

// writeSearchTable writes the matched records as a deterministic aligned table.
// A zero-match search prints an explicit "(no matches)" line rather than a bare
// blank, so the operator can tell a completed empty search from a silent failure.
func writeSearchTable(out io.Writer, records []IndexRecord) error {
	if len(records) == 0 {
		fmt.Fprintln(out, "(no matches)")
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "URI\tKIND\tVERSION\tCATEGORY")
	for _, rec := range records {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", rec.URI, rec.Kind, rec.Version, rec.Category)
	}
	return w.Flush()
}

// searchJSON is the machine-readable search result: the freshness timestamp per
// searched host and the matched records in deterministic order. The updated map
// is keyed by host; encoding/json emits string map keys in sorted order, so the
// object is byte-stable across runs of the same input.
type searchJSON struct {
	Updated map[string]string `json:"updated"`
	Records []IndexRecord     `json:"records"`
}

// writeSearchJSON writes the matched records and the per-host freshness map as a
// single indented JSON object with a trailing newline. A host whose index
// carries no timestamp is omitted from the updated map rather than emitted with a
// zero time.
func writeSearchJSON(out io.Writer, indexes []loadedIndex, records []IndexRecord) error {
	updated := make(map[string]string, len(indexes))
	for _, li := range indexes {
		if li.HasTimestamp {
			updated[li.Host] = li.LastUpdated.Format(time.RFC3339)
		}
	}
	if records == nil {
		records = []IndexRecord{}
	}
	return writeStructured(out, "search", structuredIndented, searchJSON{Updated: updated, Records: records})
}
