// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullops

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/entitypin"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
)

// adviseIgnoreRuleDifferences writes one advisory to out for every Recipe and
// FeatureRecipe this Release builds whose local source directory was pinned under
// ignore rules other than the ones the plugin serving its capability in this
// workspace declares. It never halts: the entity's files are the ones its recorded
// rules kept, a publication carried exactly those, and a render here reads exactly
// those, so the advisory tells the reader that this workspace's plugin would have
// chosen differently — which matters only if that plugin needs a file the recorded
// rules left out, and then the build says so itself, naming the file.
//
// Each advisory names both plugins and the difference: the recorded plugin by the
// name the lock records, which carries no version, and the serving plugin by its
// entity reference. The recorded side is the
// plugin block of the lock beside the entity — the author's own lock for an entity
// in one of this workspace's catalog directories, and the lock a publication
// carried for one resolved from a registry — and a section recording no plugin is
// read as a plugin declaring nothing. A lock recording no section at all made no
// choice of rules and is not compared. The workspace side is the plugin the same
// provider resolution a build runs selects, over providerBindings. An entity whose
// source is not a local directory is passed over, because no rule applies to it,
// and so is a capability no plugin here serves or one whose resolution fails:
// the pull's own provider resolution reports those.
func adviseIgnoreRuleDifferences(out io.Writer, lazyView *catalog.LazyLookup, entities []closure.ResolvedRef,
	used map[string][]closure.ComponentSourceInfra, providerBindings map[string]string) {
	loader := lazyView.ClosureRecipeLoader()
	documents := lazyView.RecipeLookupWithManifest(nil)
	seen := map[string]bool{}
	for _, ent := range entities {
		if len(used[ent.URI]) == 0 {
			continue
		}
		var kind, name, version, capability, document string
		var found bool
		if r, err := loader.LoadRecipe(ent.URI); err == nil {
			kind, name, version, capability = "Recipe", r.Metadata.Name, r.Metadata.Version, r.Spec.Capability
			document, found = documents.RecipeDocument(r)
		} else if fr, err := loader.LoadFeatureRecipe(ent.URI); err == nil {
			kind, name, version, capability = "FeatureRecipe", fr.Metadata.Name, fr.Metadata.Version, fr.Spec.Capability
			document, found = documents.FeatureRecipeDocument(fr)
		}
		if !found || seen[document] {
			continue
		}
		seen[document] = true
		_, authored := entitypin.WritableHome(lazyView, kind, capability, name, version)
		if line, differ := ignoreRuleDifference(document, kind, name, capability, authored, lazyView, providerBindings); differ {
			fmt.Fprintln(out, line)
		}
	}
}

// ignoreRuleDifference compares the plugin rules the lock beside document records
// with the ones the plugin serving capability in this workspace declares, and
// returns the advisory line and true where they differ. authored says the entity
// sits in one of this workspace's catalog directories, where pinning it again is
// the reader's own step; an entity resolved from a registry was pinned by its
// publisher, and the advisory offers no step it cannot take.
func ignoreRuleDifference(document, kind, name, capability string, authored bool, lazyView *catalog.LazyLookup,
	providerBindings map[string]string) (string, bool) {
	data, err := os.ReadFile(document)
	if err != nil {
		return "", false
	}
	if _, walks, err := catalog.LocalSourceDirectory(data, document); err != nil || !walks {
		return "", false
	}
	lock, _, err := recipepin.ReadLock(document)
	if err != nil || lock.Ignore == nil {
		// A lock recording no ignore section recorded no choice of rules at all:
		// every file under the directory is the entity's, and there is nothing a
		// plugin's rules can be said to differ from. Advising there would print at
		// every pull of every such entity, and a reader taught that a line prints
		// regardless stops reading it.
		return "", false
	}
	served, ok, err := lazyView.ServingProvider(capability, providerBindings)
	if err != nil || !ok {
		return "", false
	}
	recordedPlugin, recordedPatterns, recordedFiles := "no plugin", []string(nil), []string(nil)
	if lock.Ignore.Plugin != nil {
		p := lock.Ignore.Plugin
		recordedPlugin, recordedPatterns, recordedFiles = p.Name, p.Patterns, p.Files
	}
	var herePatterns, hereFiles []string
	if served.Ignore != nil {
		herePatterns, hereFiles = served.Ignore.Patterns, served.Ignore.Files
	}
	if sameList(recordedPatterns, herePatterns) && sameList(recordedFiles, hereFiles) {
		return "", false
	}
	line := fmt.Sprintf("warning: %s %q was pinned under the ignore rules of %s (%s), and the plugin serving %q in "+
		"this workspace, %s, declares %s. Its files are the ones the recorded rules kept, and a build here reads "+
		"those; where %s needs a file they left out, its build fails naming that file",
		kind, name, recordedPlugin, describeRules(recordedPatterns, recordedFiles), capability, served.Ref,
		describeRules(herePatterns, hereFiles), served.Ref)
	if authored {
		line += fmt.Sprintf(". Running `aggrega pin` on it records the rules of %s instead", served.Ref)
	}
	return line, true
}

// describeRules spells a plugin's declared rules for the advisory.
func describeRules(patterns, files []string) string {
	if len(patterns) == 0 && len(files) == 0 {
		return "no ignore rules"
	}
	var parts []string
	if len(patterns) > 0 {
		parts = append(parts, "patterns "+strings.Join(patterns, " "))
	}
	if len(files) > 0 {
		parts = append(parts, "ignore files "+strings.Join(files, " "))
	}
	return strings.Join(parts, "; ")
}

// sameList reports whether two lists hold the same strings in the same order.
func sameList(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
