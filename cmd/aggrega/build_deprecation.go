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
// The first halting source encountered fails the build. Advisories for every
// deprecated entity write to diagOut regardless. Iteration is over the sorted
// closure-entity URIs so the surfaced advisory/halt order is deterministic and
// independent of closure order.
func enforceClosureDeprecation(cl closure.Closure, lf *lockfile.Lockfile, lazyView *catalog.LazyLookup, buildCfg workspaceconfig.BuildConfig, diagOut io.Writer) error {
	threshold := deprecation.Severity(buildCfg.HaltOnDeprecatedSeverity)

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
				return fmt.Errorf("%w: %s is deprecated (%s): %s: %w",
					deprecation.ErrDeprecatedEntity, e.URI, rec.Severity, rec.Message, errHaltThreshold(threshold))
			}
			fmt.Fprintf(diagOut, "  deprecation: %s is deprecated (%s): %s\n", e.URI, rec.Severity, rec.Message)
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
