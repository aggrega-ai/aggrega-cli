// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"io"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// enforceClosureDeprecation evaluates each closure entity's and plugin's
// deprecation status from TWO sources under one build-time gate and halts, or
// emits an advisory, per the workspace build policy. It reads ONLY the lockfile
// (the retroactive marker recorded at pull) and the resolved entity metadata
// (the author-time flag) — it never touches a registry, preserving the
// ahead-of-time boundary that build is offline and lockfile-only.
//
// The two sources:
//
//   - the lockfile Deprecation record, a retroactive marker discovered and
//     verified at pull time, governed by build.halt_on_deprecated_severity:
//     halt when the recorded severity meets or exceeds the threshold in the
//     ordered set warning < error < retracted (default "error"), advisory
//     otherwise;
//   - the entity's author-time metadata.deprecated flag, governed by
//     build.halt_on_deprecated: halt when the entity is deprecated AND the flag
//     is set, advisory otherwise.
//
// Only the first of those two reports an observation moment, and the asymmetry is
// deliberate: a recorded marker states what a registry reported at some past pull,
// so how stale it might be is part of the fact, while metadata.deprecated is read
// from the resolved entity in this build and has no separate moment to state.
//
// The first halting source encountered fails the build. Advisories for every
// deprecated entity write to diagOut regardless. Iteration is over the sorted
// closure-entity URIs so the surfaced advisory/halt order is deterministic and
// independent of closure order.
func enforceClosureDeprecation(cl closure.Closure, lf *lockfile.Lockfile, lazyView *catalog.LazyLookup, buildCfg workspaceconfig.BuildConfig, diagOut io.Writer) error {
	threshold := deprecation.Severity(buildCfg.HaltOnDeprecatedSeverity)
	// One stamp governs every recorded marker in this lockfile, so it is rendered
	// once rather than per entity.
	asOf := lockfileAsOfSuffix(lf.GeneratedAt)

	recByURI := make(map[string]*lockfile.DeprecationRecord)
	for i := range lf.Entities {
		if lf.Entities[i].Deprecation != nil {
			recByURI[lf.Entities[i].URI] = lf.Entities[i].Deprecation
		}
	}
	for i := range lf.Plugins {
		if lf.Plugins[i].Deprecation != nil {
			recByURI[lf.Plugins[i].URI] = lf.Plugins[i].Deprecation
		}
	}

	// Evaluate catalog entities AND Wasm plugins under one gate: both are
	// consumed by the build and both can carry either deprecation source. Their
	// URI namespaces are disjoint, so a single sorted walk is unambiguous and
	// deterministic.
	refs := make([]closure.ResolvedRef, 0, len(cl.Entities)+len(cl.Plugins))
	refs = append(refs, cl.Entities...)
	refs = append(refs, cl.Plugins...)
	sort.Slice(refs, func(i, j int) bool { return refs[i].URI < refs[j].URI })

	for _, e := range refs {
		// Retroactive source: a verified marker recorded at pull, gated by the
		// severity threshold.
		if rec, ok := recByURI[e.URI]; ok {
			if deprecation.MeetsThreshold(deprecation.Severity(rec.Severity), threshold) {
				// The moment qualifies the recorded fact, so it sits with the
				// message rather than after the threshold clause, which would
				// read as though the policy were what had been recorded.
				return fmt.Errorf("%w: %s is deprecated (%s): %s%s: %w",
					deprecation.ErrDeprecatedEntity, e.URI, rec.Severity, rec.Message, asOf, errHaltThreshold(threshold))
			}
			fmt.Fprintf(diagOut, "  deprecation: %s is deprecated (%s): %s%s\n", e.URI, rec.Severity, rec.Message, asOf)
		}

		// Author-time source: metadata.deprecated on the resolved entity, gated
		// by the halt_on_deprecated bool.
		if entityDeprecated(e, lazyView) {
			if buildCfg.HaltOnDeprecated {
				return fmt.Errorf("%w: %s is marked deprecated by its author (metadata.deprecated: true)",
					deprecation.ErrDeprecatedEntity, e.URI)
			}
			fmt.Fprintf(diagOut, "  deprecation: %s is marked deprecated by its author (metadata.deprecated: true)\n", e.URI)
		}
	}
	return nil
}

// errHaltThreshold renders the threshold half of a halt message. It is a plain
// error, not a sentinel: the halt's sentinel is ErrDeprecatedEntity, which the
// message wraps first; this only names the policy that triggered it.
func errHaltThreshold(threshold deprecation.Severity) error {
	return fmt.Errorf("halt_on_deprecated_severity is %q", threshold)
}

// lockfileAsOfSuffix renders when a recorded marker was observed — the moment the
// lockfile carrying it was generated — together with the command that refreshes it,
// so a reader can tell a stale record from a fresh one. A withdrawal is mutable
// registry state, so the record says what the registry reported at that pull and
// never what it reports now; the suffix exists to stop the message implying a
// currency the offline build cannot provide.
//
// The stamp can only lag the true observation, never lead it: it is set solely when
// a pull generates the lockfile, a re-pull that changes nothing keeps the earlier
// stamp, and the build preserves whatever it loaded. So the suffix errs toward
// reporting the record as older than it is, which is the safe direction.
//
// generatedAt is the lockfile's own stamp, already RFC 3339 when present; empty
// means the lockfile carries none, which is reported as such rather than rendered
// as a zero moment.
//
// This is deliberately NOT shared with the as-of wording on the index-served
// surfaces (the pull-time withdrawal notices and the inspect deprecation block).
// The two report different facts: a different provenance (the local registry index
// versus the lockfile), a different refresh command, and a different input (a parsed
// instant plus a presence flag versus an already-formatted string, so no formatting
// rule is duplicated here). Folding them together would take a template with every
// distinguishing part injected, which would single-source nothing.
func lockfileAsOfSuffix(generatedAt string) string {
	if generatedAt == "" {
		return " (recorded in the lockfile at pull, which carries no generation time; re-run `aggrega pull` to refresh it)"
	}
	return fmt.Sprintf(" (recorded in the lockfile at pull, as of %s; re-run `aggrega pull` to refresh it)", generatedAt)
}

// entityDeprecated reports whether the closure entity carries an author-time
// metadata.deprecated: true, read from the resolved entity via the same lazy
// lookup the build resolves everything else through (lockfile-pinned, offline).
// The entity's KindHint routes to the right accessor; an entity whose kind
// carries no deprecation flag, or one that does not resolve, reports false.
func entityDeprecated(e closure.ResolvedRef, lazyView *catalog.LazyLookup) bool {
	switch e.KindHint {
	case "component":
		if c, ok := lazyView.Component(e.URI); ok {
			return c.Metadata.Deprecated
		}
	case "feature":
		if f, ok := lazyView.Feature(e.URI); ok {
			return f.Metadata.Deprecated
		}
	case "recipe":
		if r, ok := lazyView.RecipeByURI(e.URI); ok {
			return r.Metadata.Deprecated
		}
	case "feature_recipe":
		if fr, ok := lazyView.FeatureRecipeByURI(e.URI); ok {
			return fr.Metadata.Deprecated
		}
	case "plugin":
		if p, ok := lazyView.PluginByURI(e.URI); ok {
			return p.Metadata.Deprecated
		}
	}
	return false
}
