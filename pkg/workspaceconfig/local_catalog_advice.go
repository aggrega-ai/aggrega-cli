// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"fmt"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
)

// CatalogDirsSetting is the settings path a reader writes to list the
// directories a catalog search reads, spelled as a diagnostic names it. It is
// exported for the same reason ConventionalCatalogDir is: several surfaces tell
// a reader which setting to edit, and a second spelling in a message goes on
// reading as correct after this one moves.
const CatalogDirsSetting = "workspace.catalog_dirs"

// LocalCatalogAdviceNote returns the note a surface appends to a lookup that
// found no entity, telling the reader where this workspace reads local entities
// from and what to change so the one they named is read. It is ready to append:
// it opens with its own line break and indent, or it is empty.
//
// It is EMPTY for a workspace whose reader wrote the catalog-directory list as
// an empty list. That list is the statement that this workspace has no local
// catalog, so advice about where local entities are read from contradicts what
// the reader declared, and a diagnostic that argues with a declaration is worse
// than one that says nothing.
//
// It is composed here because the two facts it reads live here and nowhere
// earlier: whether the reader wrote the empty list, which authoredEmpty is the
// one home of, and the resolved workspace root. The third fact — that a lookup
// for a named entity came back with nothing — belongs to the surface that
// performed the lookup, and that surface supplies it by calling. So the note
// attaches to a failure and is never raised on a run that resolved what it was
// asked for.
//
// The text carries no verdict. Appending it changes what a reader is told and
// never what the run decided, so a surface that appends it ends with exactly the
// status it would have ended with.
func (r *ResolvedWorkspace) LocalCatalogAdviceNote() string {
	if r == nil || authoredEmpty(r.config.Workspace.CatalogDirs) {
		return ""
	}
	where := fmt.Sprintf(
		"local entities are read from `%s/` at the workspace root and from every directory listed under `%s` in %s.",
		ConventionalCatalogDir, CatalogDirsSetting, ConfigFilename)
	remedy := fmt.Sprintf(
		"Put the entity under `%s/`, or list the directory holding it under `%s`.",
		ConventionalCatalogDir, CatalogDirsSetting)
	if n := rootEntityDocuments(r.root); n > 0 {
		return fmt.Sprintf("\n  note: %s The workspace root itself holds %s declaring an entity kind, and the root is read only while it is listed there. %s",
			where, documentCount(n), remedy)
	}
	return fmt.Sprintf("\n  note: %s %s", where, remedy)
}

// rootEntityDocuments counts the documents sitting at the workspace root itself
// that declare an entity kind.
//
// It reads that one directory and does not descend. Counting a tree would be the
// whole-workspace walk a catalog search no longer performs, so a number taken
// from one would describe a search the reader cannot ask for — and a reader
// whose entities sit in a subdirectory is still told where entities are read
// from, just without a count.
//
// The test is the kind an author may write, never the file extension. A
// workspace root ordinarily carries YAML that has nothing to do with this
// product — a container-composition file, a linter's settings, a
// continuous-integration definition — and counting those would put a number in
// front of a reader that nothing they do can change. A document whose top-level
// kind is not one of the entity kinds is not counted, and neither is one that
// does not parse: a count is offered as a fact, so a file it cannot read is a
// file it does not speak for.
func rootEntityDocuments(root string) int {
	if root == "" {
		return 0
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if ext := filepath.Ext(e.Name()); ext != ".yaml" && ext != ".yml" {
			continue
		}
		data, rerr := os.ReadFile(filepath.Join(root, e.Name()))
		if rerr != nil {
			continue
		}
		var hdr struct {
			Kind string `yaml:"kind"`
		}
		if uerr := yaml.Unmarshal(data, &hdr); uerr != nil {
			continue
		}
		if entitykind.IsKind(entitykind.Kind(hdr.Kind)) {
			count++
		}
	}
	return count
}

// documentCount renders a count of documents with its noun agreeing, so the
// sentence reads the same way whether one document was found or several.
func documentCount(n int) string {
	if n == 1 {
		return "1 document"
	}
	return fmt.Sprintf("%d documents", n)
}
