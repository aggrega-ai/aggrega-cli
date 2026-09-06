// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/internal/registryops"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// searchNow supplies the clock the staleness advisory compares a snapshot's age
// against. It is a package seam so tests pin freshness deterministically.
var searchNow = time.Now

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
registry's index is searched.

The version reported for an entity is the one a resolution would take: a
version a publisher has retracted, where the retraction carries a signature
this workspace's trust policy verifies, is not reported. Such an entity is
still listed, and the versions left out are named in a NOTES column, so an
entity whose every published version is retracted is reported as existing with
no version to take rather than disappearing from the result. Pass
--include-retracted to report the highest published version whatever its
retraction status; the NOTES column names the retracted versions either way.
The same holds for 'aggrega search -o json', where the 'version' field carries
the version a resolution would take and a 'retracted' array names the versions
left out.`,
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
	c.Flags().BoolVar(&opts.IncludeRetracted, "include-retracted", false, "Report the highest published version even where a verified marker retracts it. By default the reported version is one a resolution would take.")
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
	// IncludeRetracted reports the highest published version whatever its
	// retraction status, rather than the version a resolution would take. It
	// changes only the reported version: the retracted versions are named in the
	// result either way.
	IncludeRetracted bool
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

	res, err := registryops.Search(rw, registryops.Query{
		Text:             opts.Query,
		Kinds:            opts.Kinds,
		Selector:         opts.Selector,
		Capabilities:     opts.Capabilities,
		PluginType:       opts.PluginType,
		Registry:         opts.Registry,
		Limit:            opts.Limit,
		IncludeRetracted: opts.IncludeRetracted,
	}, errOut, searchNow())

	// A configured registry with no index yet is named before anything else,
	// including before a failure is reported. The hosts that could not be consulted
	// are the most useful thing a caller can be told when the search then finds
	// nothing to read at all, so they are never withheld by the error path.
	for _, host := range res.MissingIndexHosts {
		fmt.Fprintf(errOut, "registry %s: no local index; run 'aggrega registry update'\n", host)
	}

	if err != nil {
		// A malformed selector is the caller's own input error and is reported in
		// its own words; everything else is a failure of the search and carries the
		// command's name.
		var selErr *registryops.SelectorError
		if errors.As(err, &selErr) {
			return err
		}
		return fmt.Errorf("aggrega search: %w", err)
	}
	if res.NoRegistriesConfigured {
		fmt.Fprintln(out, "No registries configured.")
		return nil
	}

	// Freshness lines to errOut, hosts ascending, so the order is deterministic
	// regardless of config order and stdout stays result-only.
	for _, li := range res.Indexes {
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
	if res.Stale {
		fmt.Fprintln(errOut, "One or more indexes are stale; run 'aggrega registry update' to refresh.")
	}

	switch opts.Output {
	case "json":
		return writeSearchJSON(out, res.Indexes, res.Records, res.Screening)
	case "names":
		for _, rec := range res.Records {
			fmt.Fprintln(out, rec.URI)
		}
		return nil
	default:
		return writeSearchTable(out, res.Records, res.Screening)
	}
}

// retractionNote renders the cell naming what a record's screening found, or the
// empty string when it found nothing. It is the one home of that wording, so the
// same finding cannot be described two ways.
//
// The note names the retracted versions whichever version is being reported, and
// says outright when nothing is left to take — an entity that exists and holds no
// selectable version has to be reported as exactly that, because a result which
// merely omitted it would be indistinguishable from the entity not existing.
func retractionNote(s registryops.Screening) string {
	if len(s.Retracted) == 0 {
		return ""
	}
	note := "retracted: " + strings.Join(s.Retracted, ", ")
	if s.Selected == "" {
		note += " (no version remains)"
	}
	return note
}

// reportedVersionCell renders the version cell, naming the absence explicitly when
// screening is what emptied it. A record that carries no version for any other
// reason is rendered exactly as it was before, so a result meeting no withdrawal is
// untouched.
func reportedVersionCell(version string, s registryops.Screening) string {
	if version == "" && len(s.Retracted) > 0 {
		return "(none)"
	}
	return version
}

// writeSearchTable writes the matched records as a deterministic aligned table.
// A zero-match search prints an explicit "(no matches)" line rather than a bare
// blank, so the operator can tell a completed empty search from a silent failure.
//
// The NOTES column appears only when some matched record has something to report,
// which keeps a result that meets no withdrawal byte-identical to the result the
// unscreened path produced. It is deliberately not a permanent column: adding one
// unconditionally would change every existing invocation's output to carry a
// column that is empty on almost all of them.
func writeSearchTable(out io.Writer, records []registryindex.Record, screening map[string]registryops.Screening) error {
	if len(records) == 0 {
		fmt.Fprintln(out, "(no matches)")
		return nil
	}
	notes := make([]string, len(records))
	anyNote := false
	for i, rec := range records {
		notes[i] = retractionNote(screening[rec.URI])
		if notes[i] != "" {
			anyNote = true
		}
	}
	var buf bytes.Buffer
	w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	header := "URI\tKIND\tVERSION\tCATEGORY"
	if anyNote {
		header += "\tNOTES"
	}
	fmt.Fprintln(w, header)
	for i, rec := range records {
		row := fmt.Sprintf("%s\t%s\t%s\t%s", rec.URI, rec.Kind, reportedVersionCell(rec.Version, screening[rec.URI]), rec.Category)
		if anyNote {
			row += "\t" + notes[i]
		}
		fmt.Fprintln(w, row)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	// A row with nothing to report still opens the notes cell, and the column
	// before an opened cell is padded — so such a row would end in padding it has
	// no content for. Trimming it keeps every row free of trailing whitespace. It
	// is a no-op on a result with no notes column, where the last cell already ends
	// the line unpadded.
	for _, line := range strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n") {
		fmt.Fprintln(out, strings.TrimRight(line, " "))
	}
	return nil
}

// searchRecord is one matched entity as the machine-readable result reports it:
// the index record, plus the versions screened out of selection.
//
// The record is embedded rather than nested, so every field it carries is emitted
// exactly where it was before and a result meeting no withdrawal is byte-identical.
// Retracted is emitted only when there is something to report, and it names the
// versions a resolution would refuse — so a consumer reads the product's own
// verdict rather than re-deriving one from the raw deprecation entries, which is
// the derivation that would be free to disagree.
type searchRecord struct {
	registryindex.Record
	Retracted []string `json:"retracted,omitempty"`
}

// searchJSON is the machine-readable search result: the freshness timestamp per
// searched host and the matched records in deterministic order. The updated map
// is keyed by host; encoding/json emits string map keys in sorted order, so the
// object is byte-stable across runs of the same input.
type searchJSON struct {
	Updated map[string]string `json:"updated"`
	Records []searchRecord    `json:"records"`
}

// writeSearchJSON writes the matched records and the per-host freshness map as a
// single indented JSON object with a trailing newline. A host whose index
// carries no timestamp is omitted from the updated map rather than emitted with a
// zero time.
//
// The version each record carries is the version a resolution would take, which is
// the machine-readable half of the same contract the table states: an entity whose
// every published version is retracted carries an empty version alongside the
// retracted list, rather than being dropped from the result.
func writeSearchJSON(out io.Writer, indexes []registryindex.Loaded, records []registryindex.Record, screening map[string]registryops.Screening) error {
	updated := make(map[string]string, len(indexes))
	for _, li := range indexes {
		if li.HasTimestamp {
			updated[li.Host] = li.LastUpdated.Format(time.RFC3339)
		}
	}
	reported := make([]searchRecord, 0, len(records))
	for _, rec := range records {
		reported = append(reported, searchRecord{Record: rec, Retracted: screening[rec.URI].Retracted})
	}
	return writeStructured(out, "search", searchJSON{Updated: updated, Records: reported})
}
