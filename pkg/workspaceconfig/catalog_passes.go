// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// CatalogPass is one pass of a check that reads a whole catalog rather than one
// Release's dependency closure: the Releases it is run for and the catalog
// directories it reads.
type CatalogPass struct {
	// Releases names the Releases the pass is run for, each spelled as the caller
	// supplied it, in the order supplied. It is empty for the single pass a
	// workspace that gives no Release directories of its own runs, which concerns
	// no one Release.
	Releases []string

	// Dirs is the search stack the pass reads, exactly as ResolveCatalogDirs
	// returns it. It is empty where nothing is read — an authored empty list, or
	// no list and no conventional directory — so the pass has nothing to check.
	Dirs []string

	// Named is the part of Dirs somebody named, exactly as NamedCatalogDirs returns
	// it for the same Release and flags, handed to whatever reads Dirs so the
	// conventional directory is read as named or not where the stack was built.
	Named []string
}

// NamesReleases reports whether the pass is run for named Releases, which is
// what tells a report to say which Releases each pass covered.
func (p CatalogPass) NamesReleases() bool {
	return len(p.Releases) > 0
}

// WholeCatalogPasses returns the passes a check of the whole catalog runs in this
// workspace.
//
// A workspace whose configuration names no Release under `workspace.releases`
// reads one catalog, its own, so it gets one pass over the workspace's stack and
// the pass names no Release. releases is not called in that case, so a caller
// that enumerates the workspace's Releases by walking it pays nothing for a
// workspace that never needs them.
//
// A workspace whose configuration names any Release there may keep catalog
// variants side by side, each read by its own Release, and a single pass over
// the union of their directories would read every variant at once: one variant's
// entity would shadow another's, and a check would be made against whichever
// variant came first. So it gets one pass for each Release releases reports, over
// the stack the resolver returns for that Release with flagDirs prepended, and
// never a pass over the union. Releases whose stacks are identical share one pass
// that names each of them, since checking one set of directories twice would
// report every finding twice. A Release the configuration does not name reads
// the workspace's own list, as it does everywhere else, so it shares a pass with
// every other such Release. Where releases reports none, the single workspace
// pass is the answer, because there is no Release to run a pass for. Two stacks
// spelling the same directories are one stack only where they also agree on which
// of them somebody named: the conventional directory is read differently when it
// is named, so a stack naming it and one carrying it unnamed are two passes.
//
// releases reports each Release file as a path relative to the workspace root,
// or as an absolute path, and each pass names its Releases in that spelling.
// Passes come in the order of the first Release each one names, so identical
// input yields identical passes.
func (r *ResolvedWorkspace) WholeCatalogPasses(releases func() []string, flagDirs []string) []CatalogPass {
	workspace := []CatalogPass{{Dirs: r.ResolveCatalogDirs("", flagDirs), Named: r.NamedCatalogDirs("", flagDirs)}}
	if r.config.Workspace.Releases.Len() == 0 || releases == nil {
		return workspace
	}
	var passes []CatalogPass
	for _, release := range releases() {
		file := release
		if !filepath.IsAbs(file) {
			file = filepath.Join(r.root, file)
		}
		dirs, named := r.ResolveCatalogDirs(file, flagDirs), r.NamedCatalogDirs(file, flagDirs)
		shared := slices.IndexFunc(passes, func(p CatalogPass) bool {
			return slices.Equal(p.Dirs, dirs) && slices.Equal(p.Named, named)
		})
		if shared >= 0 {
			passes[shared].Releases = append(passes[shared].Releases, release)
			continue
		}
		passes = append(passes, CatalogPass{Releases: []string{release}, Dirs: dirs, Named: named})
	}
	if len(passes) == 0 {
		return workspace
	}
	return passes
}

// AnyCatalogPassReads reports whether at least one of passes has a directory to
// read. A whole-catalog check over passes that read nothing would pass over no
// entity at all and read exactly like a pass over a healthy catalog, which is why
// its surfaces refuse instead.
func AnyCatalogPassReads(passes []CatalogPass) bool {
	return slices.ContainsFunc(passes, func(p CatalogPass) bool { return len(p.Dirs) > 0 })
}

// CatalogDirsSettingWords names, for a refusal, where a reader sets the catalog
// directories a whole-catalog check reads: the workspace's own list and the
// configuration file that carries it. Where perRelease is set — the workspace
// gives its Releases directories of their own — it also names a Release's own
// list, since a Release whose entry writes one does not read the workspace's, so
// the workspace's list alone would be a remedy that reaches nothing there. It is
// the one wording both whole-catalog refusals read, so the two cannot come to
// name different settings.
func CatalogDirsSettingWords(perRelease bool) string {
	if perRelease {
		return fmt.Sprintf("`%s`, or a Release's own `catalog_dirs` under `%s`, in %s",
			CatalogDirsSetting, ReleaseCatalogsSetting, ConfigFilename)
	}
	return fmt.Sprintf("`%s` in %s", CatalogDirsSetting, ConfigFilename)
}

// CatalogDirsSettingWordsFor names, for a refusal about the catalog directories
// one Release reads, where the configuration lists them: CatalogDirsSettingWords,
// naming a Release's own list as well wherever the configuration gives release an
// entry of its own, since such a Release reads that entry's directories. release
// is the Release file in the form ResolveCatalogDirs takes it. It reads the
// configuration and nothing passed on a command line, so it is the right words
// only for a stack resolved with no command-line directories.
func (r *ResolvedWorkspace) CatalogDirsSettingWordsFor(release string) string {
	_, own := r.releaseCatalog(release)
	return CatalogDirsSettingWords(own)
}

// CatalogPassHeading is the line a whole-catalog report opens a Release's pass
// with: the Releases it is run for and the catalog directories it reads, or that
// it reads none. dirs are spelled as the caller's surface spells a location, so
// each surface names the directories the way it names every other path it
// reports. It is the one wording of that line, so a terminal and a tool reading
// the same pass describe it alike.
func CatalogPassHeading(releases, dirs []string) string {
	subject, verb := "Release "+strings.Join(releases, ", "), "reads"
	pronoun := "it"
	if len(releases) != 1 {
		subject, verb, pronoun = "Releases "+strings.Join(releases, ", "), "read", "them"
	}
	if len(dirs) == 0 {
		return fmt.Sprintf("%s %s no catalog directory, so there is nothing to check for %s", subject, verb, pronoun)
	}
	return fmt.Sprintf("%s %s %s", subject, verb, strings.Join(dirs, ", "))
}
