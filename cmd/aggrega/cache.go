// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/cacheops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
)

// newCacheCmd builds the `aggrega cache` command family: inspect, reclaim, and
// wipe the on-disk caches the compiler populates:
//
//   - the workspace artifact cache under the configured cache directory
//     (default `.aggrega/cache`), holding pulled catalog entities, resolved
//     source artifacts, and pinned external inputs;
//   - the compiled-module cache, holding provider guests compiled once and
//     reused across every project on the machine;
//   - the plugin-describe cache, holding each guest's static self-report keyed
//     by the guest's content digest;
//   - the docs-bundle cache, holding the documentation bundle keyed by registry
//     and version, shared by every project on the machine.
//
// The commands here render; what they render is enumerated and removed by the
// shared cache-operations layer, which every other caller-facing surface reaches
// too. The group is attached before its children so that a child's invocation
// path is the one it will be invoked by when the registrar checks it against the
// operation table.
func attachCacheCommands(parent *cobra.Command) {
	c := &cobra.Command{
		Use:   "cache",
		Short: "Inspect and maintain the on-disk caches",
		Long: `Inspect and maintain the caches the compiler populates.

status  lists cache contents, one element per line, then a summary line.
prune   reclaims artifact-cache entries no workspace lockfile references.
evict   trims the compiled-module cache to a size cap, oldest first.
clear   wipes every cache, including the compiled-module store.`,
	}
	mustAttachGroup(parent, c)
	mustAttach(c, newCacheStatusCmd(), "cache-status")
	mustAttach(c, newCachePruneCmd(), "cache-prune")
	mustAttach(c, newCacheEvictCmd(), "cache-evict")
	mustAttach(c, newCacheClearCmd(), "cache-clear")
}

// resolveCacheLocations resolves the workspace and hands it to the shared layer,
// which fills the machine-wide roots.
//
// A workspace whose configuration is a link leading out of it stops the command
// with the resolution's own refusal, as every command reading that configuration
// stops. Where the workspace's cache sits is what the configuration decides, so a
// command that went on without it would report, prune or wipe the caches as
// though the workspace kept none, and the link the refusal names would go unsaid.
// Any other failure to resolve a workspace is not fatal: the command still
// operates on the machine-wide caches, which exist whatever the reader is
// standing in. It is not silent either. Where errOut is given, one line on it
// names the root that could not be used and why, in the resolution's own words
// — the ones every command refusing that root prints — and says that only the
// machine-wide caches are acted on, so a reader who named a workspace does not
// take a listing or a removal for one that covered it. A command that refuses
// to run without a workspace passes no errOut, since it acts on nothing.
func resolveCacheLocations(ctx context.Context, errOut io.Writer) (cacheops.Locations, error) {
	var root, cacheDir string
	ws, err := workspaceFromContext(ctx)
	switch {
	case err == nil:
		root = ws.Root()
		cacheDir = ws.Config().ResolvedArtifactCacheDir()
	case errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()):
		return cacheops.Locations{}, err
	case errOut != nil:
		fmt.Fprintf(errOut, "note: %s; only the machine-wide caches are acted on\n", err)
	}
	return cacheops.ResolveLocations(ctx, root, cacheDir), nil
}

func newCacheStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "List cache contents and a total summary",
		Long: `List the contents of every cache, one element per line, followed by a
summary line of the form "N elements, <human size>". The listing is
deterministic: identical cache state produces byte-identical output.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			loc, err := resolveCacheLocations(cmd.Context(), cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			return runCacheStatus(cmd.Context(), cmd.OutOrStdout(), loc)
		},
	}
}

// runCacheStatus prints every class's elements one per line and a global
// summary, each class heading carrying how far that class is shared and the
// summary followed by the machine-wide classes the listing covered. Output is a
// pure function of on-disk cache state.
//
// The reach is on a reading that changes nothing because a reader consults this
// before deciding whether to reclaim, and that is the moment the reach decides
// something.
func runCacheStatus(ctx context.Context, w io.Writer, loc cacheops.Locations) error {
	report, err := cacheops.Status(ctx, loc)
	if err != nil {
		return err
	}
	for _, cl := range report.Classes {
		fmt.Fprintf(w, "%s%s:\n", cl.Name, reachMarker(cl.Reach))
		if len(cl.Elements) == 0 {
			fmt.Fprintln(w, "  (empty)")
			continue
		}
		for _, el := range cl.Elements {
			if el.Note != "" {
				fmt.Fprintf(w, "  %s  %s  %s\n", el.Label, cacheops.HumanSize(el.Size), el.Note)
			} else {
				fmt.Fprintf(w, "  %s  %s\n", el.Label, cacheops.HumanSize(el.Size))
			}
		}
	}
	printProvenance(w, report.Provenance)
	fmt.Fprintf(w, "\n%d elements, %s\n", report.TotalCount, cacheops.HumanSize(report.TotalSize))
	printMachineWideReach(w, cacheops.MachineWideClasses(report.Classes))
	return nil
}

// reachMarker renders how far a class's or an element's contents are shared, as
// the marker that trails the thing it qualifies.
//
// The word inside it is the shared layer's own reach value, so a reader meets
// one vocabulary for this fact wherever the operation was invoked from rather
// than a per-surface rewording of it. It is carried on every class and every
// removal, not only the machine-wide ones: an unmarked line would leave the
// reach to be inferred from a convention the reader has to know first, and that
// inference is exactly what stating the reach exists to remove.
func reachMarker(r cacheops.Reach) string {
	return " [" + string(r) + "]"
}

// printMachineWideReach names the classes an operation acted on whose contents
// are shared across every project on the machine, and says what that means for
// a removal.
//
// The list is not written here: it is the shared layer's own computation over
// the classes the operation touched, so this cannot describe a machine-wide
// reclamation as workspace-local. An operation that touched no machine-wide
// class prints nothing, rather than a statement with an empty list in it.
//
// The sentence is about the content rather than about this run, which is what
// makes it true of a trim that removed nothing as well as of one that did: the
// class is shared either way, and that is what a reader deciding whether to
// reclaim needs before acting.
func printMachineWideReach(w io.Writer, machineWide []string) {
	if len(machineWide) == 0 {
		return
	}
	fmt.Fprintf(w, "machine-wide: %s — content there is shared with every project on this machine, so a removal is felt by all of them\n",
		strings.Join(machineWide, ", "))
}

// printHeldOpen names each element a reclamation left in place because another
// process has it open, with its reach, so a removal the platform refused reads as
// what it is rather than as a failure or as nothing at all. Only Windows refuses
// to remove an open file, so elsewhere the list is empty and this prints nothing.
func printHeldOpen(w io.Writer, held []cacheops.Removal) {
	for _, r := range held {
		fmt.Fprintf(w, "left in place %s: %s%s, open in another process\n", r.Class, r.Label, reachMarker(r.Reach))
	}
}

// printProvenance lists the resolved-plugin identities the provenance index
// holds — one line per recorded content digest, naming every {URI@version} the
// resolver loaded those exact guest bytes for. When a digest carries more than
// one identity (distinct URIs or versions that resolve to byte-identical bytes),
// the line is marked "shared by N". The listing is deterministic — digests and
// identities are enumerated in sorted order and no timestamps are printed — so
// identical index state produces byte-identical output. The identities are
// metadata about compiled modules, not reclaimable cache elements, so they are
// listed separately and excluded from the element count and size summary.
func printProvenance(w io.Writer, entries []cacheops.ProvenanceEntry) {
	fmt.Fprintf(w, "plugin provenance:\n")
	if len(entries) == 0 {
		fmt.Fprintln(w, "  (empty)")
		return
	}
	for _, e := range entries {
		line := strings.Join(e.Identities, ", ")
		if e.SharedBy > 0 {
			line += fmt.Sprintf("  shared by %d", e.SharedBy)
		}
		fmt.Fprintf(w, "  %s\n", line)
	}
}

func newCachePruneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "prune",
		Short: "Remove artifact-cache entries no lockfile references",
		Long: `Remove cache entries whose content digest no workspace lockfile references,
retaining every referenced entry. The reference set is the union of the
digests pinned across every lockfile (*.lock) in the workspace.

Prune acts on the caches whose entries carry a content digest — the workspace
artifact cache (entities, source artifacts, external inputs) and the
plugin-describe cache — and on the compiled-module cache by reference: a compiled
plugin module is reclaimed when no lockfile still references the plugin it
belongs to, and the shared runtime module every plugin depends on is reclaimed
once the last plugin referring to it is pruned. A compiled module that cannot be
traced back to a plugin is retained rather than removed on a guess; that
remainder is reclaimed by "aggrega cache evict" or "aggrega cache clear".

Prune needs a pulled workspace: it fails when no lockfile is present.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// No notice: a prune with no workspace refuses rather than falling back.
			loc, err := resolveCacheLocations(cmd.Context(), nil)
			if err != nil {
				return err
			}
			return runCachePrune(cmd.Context(), cmd.OutOrStdout(), loc)
		},
	}
}

// runCachePrune renders what the shared prune removed. On a removal failure the
// elements removed before it are still named, so a partial prune is never
// silent, and the summary is withheld because the pass did not complete.
//
// The reach statement is printed before the failure is returned, for the same
// reason the removals are: a partial prune that reached a machine-wide class
// reached it, and withholding that alongside the incomplete summary would leave
// the loudest half of the answer unsaid.
func runCachePrune(ctx context.Context, w io.Writer, loc cacheops.Locations) error {
	res, err := cacheops.Prune(ctx, loc)
	for _, r := range res.Removed {
		fmt.Fprintf(w, "removed %s: %s%s\n", r.Class, r.Label, reachMarker(r.Reach))
	}
	printHeldOpen(w, res.HeldOpen)
	printMachineWideReach(w, res.MachineWide)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "\npruned %d element(s), %s reclaimed; %d retained\n",
		len(res.Removed), cacheops.HumanSize(res.ReclaimedBytes), res.RetainedCount)
	if len(res.HeldOpen) > 0 {
		fmt.Fprintf(w, "%d element(s) left in place because another process has them open; prune again once it has finished\n",
			len(res.HeldOpen))
	}

	// Report the compiled leaves retained for lack of attribution: a prune cannot
	// map them to a lockfile, so they are reclaimable only by the size-bounded
	// evict or the total clear.
	if res.UnattributedCount > 0 {
		fmt.Fprintf(w, "%d %s element(s) unattributed and not prune-eligible (~%s); reclaim with 'aggrega cache evict' or 'aggrega cache clear'\n",
			res.UnattributedCount, cacheops.CompiledModuleClassName, cacheops.HumanSize(res.UnattributedSize))
	}

	// Report the classes a prune cannot reclaim at all so the retained count above
	// is not read against the full set.
	for _, s := range res.NotPruneEligible {
		fmt.Fprintf(w, "%d %s element(s) not prune-eligible (~%s); reclaim with 'aggrega cache clear'\n",
			s.Count, s.Class, cacheops.HumanSize(s.Size))
	}
	return nil
}

func newCacheEvictCmd() *cobra.Command {
	var maxSize string
	c := &cobra.Command{
		Use:   "evict --max-size <size>",
		Short: "Trim the compiled-module cache to a size cap, oldest first",
		Long: `Trim the compiled-module cache to a size cap by removing the
least-recently-compiled modules first, until the cache total is at or below
--max-size. The compiled-module cache is machine-global: every provider guest is
compiled once and reused across every project on the machine, so over time it can
dominate on-disk cache use.

Unlike prune (which reclaims artifact-cache entries no lockfile references) and
clear (which wipes every cache), evict is size-bounded and touches only the
compiled-module cache. Evicted modules recompile on demand on the next build, so
eviction loses no irrecoverable state.

--max-size is required and accepts a binary-unit size in the same vocabulary the
status summary prints — for example 5GiB, "4.5 GiB", or 512MiB — or a bare
integer byte count. The unit suffix is case-insensitive and an optional space
may separate the number from the unit.

This verb is the on-demand trim, at a cap you give it once. The cache is also
held to a standing cap without being asked: every invocation that loads a plugin
applies the ` + "`compiled_module_cache.max_bytes`" + ` setting (53687091200 bytes, 50 GiB,
by default) before it compiles anything, using this same removal order. Use this
verb to reclaim space now or below the standing cap; change the setting to move
the standing cap itself. When the cache already fits the cap, evict reports a
no-op and removes nothing.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(maxSize) == "" {
				return fmt.Errorf("cache evict: --max-size is required")
			}
			capBytes, err := cacheops.ParseHumanSize(maxSize)
			if err != nil {
				return fmt.Errorf("cache evict: invalid --max-size: %w", err)
			}
			if capBytes <= 0 {
				return fmt.Errorf("cache evict: --max-size must be greater than zero, got %q", maxSize)
			}
			loc, err := resolveCacheLocations(cmd.Context(), cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			return runCacheEvict(cmd.Context(), cmd.OutOrStdout(), loc, capBytes)
		},
	}
	c.Flags().StringVar(&maxSize, "max-size", "", "size cap for the compiled-module cache (e.g. 5GiB, 512MiB, or a byte count); required")
	return c
}

// runCacheEvict renders the outcome of the shared trim. When the cache already
// fits the cap it prints a single no-op line and removes nothing; otherwise it
// names each removed leaf and closes with a summary.
//
// A removal failure is reported loudly, after naming the leaves that were
// removed before it, so a partial trim is never silent.
// The cap is read back off the shared result rather than off the argument this
// function was handed. The two hold the same quantity, and reading the result's
// is what keeps them unable to disagree: the trim applied the cap the result
// reports, so that is the cap the answer names.
func runCacheEvict(ctx context.Context, w io.Writer, loc cacheops.Locations, capBytes int64) error {
	res, err := cacheops.Evict(ctx, loc, capBytes)
	if res.WithinCapBefore && err == nil {
		fmt.Fprintf(w, "compiled-module cache is %s, within cap %s; nothing evicted\n",
			cacheops.HumanSize(res.TotalBefore), cacheops.HumanSize(res.CapBytes))
		printMachineWideReach(w, res.MachineWide)
		return nil
	}
	for _, l := range res.Removed {
		fmt.Fprintf(w, "evicted %s (%s)%s\n", l.Label, cacheops.HumanSize(l.Size), reachMarker(l.Reach))
	}
	printHeldOpen(w, res.HeldOpen)
	if err != nil {
		// A trim that removed leaves before it failed reached what it reached, so
		// the reach statement travels with the partial answer. A refusal that
		// removed nothing has no reach to report and states only why it refused.
		if len(res.Removed) > 0 {
			printMachineWideReach(w, res.MachineWide)
		}
		return err
	}
	fmt.Fprintf(w, "evicted %d leaf(s), %s reclaimed; %s retained (cap %s)\n",
		len(res.Removed), cacheops.HumanSize(res.ReclaimedBytes), cacheops.HumanSize(res.RemainingBytes), cacheops.HumanSize(res.CapBytes))
	printMachineWideReach(w, res.MachineWide)
	return nil
}

// armCompiledModuleCacheCap arms the size cap the compiled-module cache is held
// to, reading it from the resolved configuration's `compiled_module_cache`
// block. The root command calls it once, before any command body runs and so
// before any plugin could be loaded.
//
// The cap is handed over as a function rather than a value so that resolving the
// configuration stays deferred: a command that never loads a plugin never opens
// the compiled-module cache, so the function is never called and no workspace is
// discovered. When resolution fails there is no configured answer to honour, so
// the function reports the built-in default and the cache stays capped rather
// than becoming unbounded because a workspace could not be found.
func armCompiledModuleCacheCap(ctx context.Context) {
	wasm.SetCompileCacheCapResolver(func() int64 { return compiledModuleCacheCap(ctx) })
}

// compiledModuleCacheCap reads the compiled-module cache's size cap out of the
// resolved configuration. It is the whole of what arming defers, split out so the
// reading can be exercised against a fixture workspace without arming anything.
//
// A workspace that does not resolve yields the built-in default rather than no
// cap: a cache that cannot find its setting is still a cache that should not grow
// without bound.
func compiledModuleCacheCap(ctx context.Context) int64 {
	ws, err := workspaceFromContext(ctx)
	if err != nil {
		return wasm.DefaultCompileCacheCapBytes
	}
	return ws.Config().CompiledModuleCache.MaxBytes
}

func newCacheClearCmd() *cobra.Command {
	var assumeYes bool
	c := &cobra.Command{
		Use:   "clear",
		Short: "Wipe every cache, including compiled modules",
		Long: `Wipe every cache: the workspace artifact cache (entities, source artifacts,
external inputs) and the caches every project on the machine shares (compiled
modules, plugin descriptors and documentation bundles). This is the total-wipe
verb; unlike prune it reclaims the compiled-module cache and the documentation
bundles as well. Cleared caches repopulate on the next build, and the
documentation bundle on the next read of the documentation.

Because the wipe is irreversible, clear asks for confirmation before removing
anything. Pass --yes to skip the prompt for non-interactive use; without --yes
on a non-interactive stdin it refuses rather than blocking on a prompt no one
can answer.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// The locations are resolved before the reader is asked, so a
			// workspace the wipe cannot proceed in is refused before anyone is
			// asked to consent to it.
			loc, err := resolveCacheLocations(cmd.Context(), cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			if err := confirmCacheClear(cmd.OutOrStdout(), cmd.InOrStdin(), assumeYes); err != nil {
				return err
			}
			return runCacheClear(cmd.Context(), cmd.OutOrStdout(), loc)
		},
	}
	c.Flags().BoolVar(&assumeYes, "yes", false, "skip the confirmation prompt (for non-interactive/CI use)")
	return c
}

// confirmCacheClear gates the total cache wipe behind the shared interactive
// confirmation. With assumeYes it consents immediately. On a non-interactive
// stdin without assumeYes it refuses — returning an error rather than blocking
// on a prompt no one can answer. Otherwise it prompts [y/N] on w, reads a line
// from r, and consents only on an explicit yes.
func confirmCacheClear(w io.Writer, r io.Reader, assumeYes bool) error {
	return confirmation{
		question:   "This wipes every cache, including the compiled-module store. Proceed? [y/N] ",
		noTerminal: "cache clear: refusing to wipe every cache on a non-interactive stdin without --yes",
		declined:   "cache clear: aborted",
	}.ask(w, r, assumeYes)
}

// runCacheClear renders what the shared wipe removed. On a removal failure the
// subtrees cleared before it are still named, and the summary is withheld — but
// the reach statement is not, for the same reason it is not withheld from a
// partial prune.
func runCacheClear(ctx context.Context, w io.Writer, loc cacheops.Locations) error {
	res, err := cacheops.Clear(ctx, loc)
	for _, c := range res.Cleared {
		fmt.Fprintf(w, "cleared %s (%s)%s\n", c.Class, cacheops.HumanSize(c.Size), reachMarker(c.Reach))
		if c.HeldOpenFiles > 0 {
			fmt.Fprintf(w, "  left in place: %d file(s) (%s) another process has open; clear again once it has finished\n",
				c.HeldOpenFiles, cacheops.HumanSize(c.HeldOpenBytes))
		}
	}
	printMachineWideReach(w, res.MachineWide)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "\ncleared %d cache(s), %s reclaimed\n", len(res.Cleared), cacheops.HumanSize(res.ReclaimedBytes))
	return nil
}
