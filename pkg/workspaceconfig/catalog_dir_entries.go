// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// catalogDirEntry is where one configured catalog directory was written: the
// configuration file and the list in it — the key path from the document root to
// the list, `workspace.catalog_dirs`, its `_append` companion, or a Release entry's
// own — its position in that list, and the entry as the file wrote it, before it
// was anchored to the file's directory. An entry an environment variable set names
// the variable instead of a file, and carries no key path.
type catalogDirEntry struct {
	file     string
	variable string
	path     []string
	index    int
	written  string
}

// setting names the list the entry sits in the way a reader finds it in the file:
// the workspace's own list by its dotted key, and a Release entry's list as that
// entry's key under `workspace.releases`, spelled as the file spells it.
func (e catalogDirEntry) setting() string {
	if len(e.path) == 4 {
		return fmt.Sprintf("`%s` of the `%s` entry %q", e.path[3], ReleaseCatalogsSetting, e.path[2])
	}
	return "`" + strings.Join(e.path, ".") + "`"
}

// line returns the 1-based line of the entry in its file, read from the file as
// it stands, or 0 where the file no longer reads or no longer carries the entry.
//
// It is asked only where a refusal names the entry, so a resolution that refuses
// nothing never reads a file a second time. A list written in block style places
// each entry on a line of its own; one written in flow style shares the line its
// key is on, which is the line reported for it.
func (e catalogDirEntry) line() int {
	if e.file == "" {
		return 0
	}
	data, err := os.ReadFile(e.file)
	if err != nil {
		return 0
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return 0
	}
	idx := yamlpos.BuildLineIndexWithEncoding(data, yamlpos.PositionEncodingUTF8)
	if r, ok := yamlpos.EntryRange(&doc, idx, append(slices.Clone(e.path), strconv.Itoa(e.index))); ok {
		return r.Start.Line + 1
	}
	if span, ok := yamlpos.KeyRange(&doc, idx, e.path...); ok {
		return span.Line
	}
	return 0
}

// catalogDirEntries records, as the cascade composes the workspace's
// `catalog_dirs`, where each entry of the composed list was written, so a refusal
// of a listed directory can name the file and the line to edit. The composed list
// carries no such record — an entry is a path whichever layer wrote it — and
// resolution is the one moment the entry and the file it came from are both in
// hand.
//
// It follows the composition rather than performing it, which stays with the
// overlay, applyAppends and the environment overlay: a file writing
// `catalog_dirs` replaces the base list in force, a file not writing it leaves the
// base to the file that did, every appended entry joins after the final base,
// layer by layer in the order the cascade reads them, and an environment variable
// setting the list replaces all of it. Each method is called where resolve hands
// the same layer to the composition, which is what keeps the record and the list
// entry for entry. A Release entry's own lists carry their record on the entry
// itself (ReleaseCatalogConfig), composed where the entries are.
type catalogDirEntries struct {
	base     []catalogDirEntry
	appended []catalogDirEntry
}

// fromConfigFile records the contribution of a configuration file read as a layer
// of its own — the global file, or a directory's base file — as resolve loads it,
// before any path in it is anchored.
//
// A file decodes onto the documented defaults, which carry no catalog list, so a
// non-nil `catalog_dirs` is one the file writes, and an empty one written empty
// replaces the base all the same.
func (r *catalogDirEntries) fromConfigFile(path string, layer Config) {
	if layer.Workspace.CatalogDirs != nil {
		r.base = catalogDirsWritten(path, "catalog_dirs", layer.Workspace.CatalogDirs)
	}
	r.appended = append(r.appended, catalogDirsWritten(path, "catalog_dirs_append", layer.Workspace.CatalogDirsAppend)...)
}

// fromCIOverlay records the contribution of a CI-owned overlay. It replaces
// nothing: both of its lists are appended, its `catalog_dirs` entries ahead of its
// `catalog_dirs_append` ones, as foldCIAdditive folds them.
func (r *catalogDirEntries) fromCIOverlay(path string, layer Config) {
	r.appended = append(r.appended, catalogDirsWritten(path, "catalog_dirs", layer.Workspace.CatalogDirs)...)
	r.appended = append(r.appended, catalogDirsWritten(path, "catalog_dirs_append", layer.Workspace.CatalogDirsAppend)...)
}

// fromEnvironment records the list an environment variable set, where one of the
// variables the environment overlay applied (applied, as applyEnvOverlay reports
// them) is the one bound to `workspace.catalog_dirs`: that variable replaces the
// whole composed list, so it is the whole record. dirs is the list it set.
func (r *catalogDirEntries) fromEnvironment(applied, dirs []string) {
	for _, b := range envBindings {
		if b.configPath != CatalogDirsSetting || !slices.Contains(applied, b.envVar) {
			continue
		}
		r.base = make([]catalogDirEntry, len(dirs))
		for i, d := range dirs {
			r.base[i] = catalogDirEntry{variable: b.envVar, index: i, written: d}
		}
		r.appended = nil
	}
}

// composed returns where each entry of the composed list was written, in list
// order: the base list in force, then every appended entry.
func (r *catalogDirEntries) composed() []catalogDirEntry {
	return append(slices.Clip(r.base), r.appended...)
}

// catalogDirsWritten records each of dirs as written under `workspace.<key>` in
// the file at path.
func catalogDirsWritten(path, key string, dirs []string) []catalogDirEntry {
	out := make([]catalogDirEntry, len(dirs))
	for i, d := range dirs {
		out[i] = catalogDirEntry{file: path, path: []string{"workspace", key}, index: i, written: d}
	}
	return out
}

// releaseCatalogDirsWritten records each of dirs as written under a Release
// entry's key — `catalog_dirs` or `catalog_dirs_append` — in the entry the file at
// path keys as release.
func releaseCatalogDirsWritten(path, release, key string, dirs []string) []catalogDirEntry {
	out := make([]catalogDirEntry, len(dirs))
	for i, d := range dirs {
		out[i] = catalogDirEntry{file: path, path: []string{"workspace", "releases", release, key}, index: i, written: d}
	}
	return out
}

// catalogDirRefusal is the refusal of a configured catalog directory the stack
// for release cannot read: dir as the stack carries it, fault saying what the file
// system holds there instead of a directory, and from, where the record has it,
// where the entry was written.
//
// It names the entry as written, the path it resolved to, the configuration file
// and line that carry it and the list it sits in, and says what to do — so the
// reader goes straight to the one line to fix. An entry an environment variable
// set names the variable. Where no record lines up with the list, it names the
// setting the list is read from instead of a file it cannot vouch for. It wraps
// ErrConfigInvalid, as every refusal of a value the configuration carries does.
func (r *ResolvedWorkspace) catalogDirRefusal(release, dir string, fault catalogDirFault, from *catalogDirEntry) error {
	resolved := dir
	if abs, err := filepath.Abs(dir); err == nil {
		resolved = abs
	}
	switch {
	case from == nil:
		return fmt.Errorf("%s lists %s, %s; %s, or remove the entry: %w",
			r.CatalogDirsSettingWordsFor(release), resolved, fault.what, fault.remedy, ErrConfigInvalid())
	case from.variable != "":
		return fmt.Errorf("%s: the entry %q names %s, %s; %s, or remove the entry from the variable: %w",
			EnvironmentVariableWords(from.variable), from.written, resolved, fault.what, fault.remedy, ErrConfigInvalid())
	}
	where := from.file
	if line := from.line(); line > 0 {
		where = fmt.Sprintf("%s:%d", from.file, line)
	}
	return fmt.Errorf("%s: the %s entry %q names %s, %s; %s, or remove the entry: %w",
		where, from.setting(), from.written, resolved, fault.what, fault.remedy, ErrConfigInvalid())
}
