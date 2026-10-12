// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"fmt"
	"slices"

	"github.com/aggrega-ai/aggrega-cli/pkg/ociref"
)

// importerEntry is where one entry of the composed importer list was written:
// the configuration file, the key the file lists it under — plugins.importer or
// plugins.importer_append — its position under that key, and its uri as the file
// wrote it, before a relative path in it is anchored to the file's directory.
type importerEntry struct {
	file  string
	key   string
	index int
	uri   string
}

// String names the entry the way a reader finds it: the key and position a
// schema refusal would name, the file, and the uri as written there.
func (e importerEntry) String() string {
	return fmt.Sprintf("%s[%d] in %s (uri %q)", e.key, e.index, e.file, e.uri)
}

// importerEntries records, as the cascade composes the importer list, where each
// entry of the composed list was written, so a refusal naming an entry can name
// the file to edit. The composed list itself carries no such record — an entry is
// a PluginRef whichever layer wrote it — and resolution is the one moment the
// entry and the file it came from are both in hand.
//
// It follows the composition rather than performing it, which stays with the
// overlay and applyAppends: a file writing plugins.importer replaces the base list
// in force, a file not writing it leaves the base to the file that did, and every
// appended entry joins after the final base, layer by layer in the order the
// cascade reads them. Each method is called where resolve hands the same layer to
// the composition, which is what keeps the record and the list entry for entry.
type importerEntries struct {
	base     []importerEntry
	appended []importerEntry
}

// fromConfigFile records the contribution of a configuration file read as a
// layer of its own — the global file, or a directory's base file — as resolve
// loads it, before any relative path in it is anchored.
//
// A file decodes onto the documented defaults, which carry no importer list, so
// a non-nil plugins.importer is one the file writes. The schema admits only a
// list there, so a file writing the key is a file whose overlay replaced the base.
func (r *importerEntries) fromConfigFile(path string, layer Config) {
	if layer.Plugins.Importer != nil {
		r.base = entriesWritten(path, "plugins.importer", layer.Plugins.Importer)
	}
	r.appended = append(r.appended, entriesWritten(path, "plugins.importer_append", layer.Plugins.ImporterAppend)...)
}

// fromCIOverlay records the contribution of a CI-owned overlay. It replaces
// nothing: both of its importer lists are appended, its plugins.importer entries
// ahead of its plugins.importer_append ones, as foldCIAdditive folds them.
func (r *importerEntries) fromCIOverlay(path string, layer Config) {
	r.appended = append(r.appended, entriesWritten(path, "plugins.importer", layer.Plugins.Importer)...)
	r.appended = append(r.appended, entriesWritten(path, "plugins.importer_append", layer.Plugins.ImporterAppend)...)
}

// composed returns where each entry of the composed importer list was written,
// in list order: the base list in force, then every appended entry.
func (r *importerEntries) composed() []importerEntry {
	return append(slices.Clip(r.base), r.appended...)
}

// entriesWritten records each of refs as written under key in the file at path.
func entriesWritten(path, key string, refs []PluginRef) []importerEntry {
	out := make([]importerEntry, len(refs))
	for i, ref := range refs {
		out[i] = importerEntry{file: path, key: key, index: i, uri: ref.URI}
	}
	return out
}

// checkUniqueImporterAttachments returns a wrapped ErrDuplicateImporterAttachment
// when two entries of the composed importer list name one importer: each entry's
// uri, with any ":<version>" split off, is the importer it names, so
// "importer/agent-flows:1.0.0" and "importer/agent-flows:2.0.0" name one importer.
//
// An entry whose uri is a filesystem path names no importer and is not compared:
// such a uri is anchored to its file's directory at load and is never an
// importer's identity, and splitting a "version" off one would read a drive
// letter's colon as a version separator. Which uris are paths is the loader's own
// reading, LooksLikeOCIURI.
//
// The list is walked in order and the first entry naming an importer an earlier
// entry named is the one reported, beside that earlier entry, so the refusal is
// the same on every run. written is where each entry was written, entry for
// entry; were it ever not to line up with refs, the refusal names the entries by
// their place in the composed list rather than naming a file it cannot vouch for.
// The check keys only on the uri's text — it never resolves, loads, or describes
// the importer named.
func checkUniqueImporterAttachments(refs []PluginRef, written []importerEntry) error {
	if len(written) != len(refs) {
		written = nil
	}
	describe := func(i int) string {
		if written == nil {
			return fmt.Sprintf("entry %d of the composed plugins.importer list (uri %q)", i, refs[i].URI)
		}
		return written[i].String()
	}
	first := make(map[string]int, len(refs))
	for i, ref := range refs {
		if !LooksLikeOCIURI(ref.URI) {
			continue
		}
		importer := ociref.VersionlessBase(ref.URI)
		earlier, named := first[importer]
		if !named {
			first[importer] = i
			continue
		}
		return fmt.Errorf("workspaceconfig: importer %q is attached by %s and again by %s; keep exactly one plugins.importer entry per importer, its uri naming the version you want: %w",
			importer, describe(earlier), describe(i), ErrDuplicateImporterAttachment())
	}
	return nil
}
