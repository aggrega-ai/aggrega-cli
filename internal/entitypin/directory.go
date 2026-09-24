// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package entitypin

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// ErrEntitySharesItsDirectory is the loud halt raised over an entity that sits in
// one directory beside another entity, where the entity's kind is one that has a
// directory of its own: every kind except the few the entity-kind vocabulary lets
// share one, which are the top-level documents a workspace root holds side by
// side.
//
// It is raised at the two moments something is produced from an entity's
// directory, and at no other: the pin step, which writes the one lock file the
// directory has room for, and a publication, which packages the entity from its
// directory under a coordinate of its own. A directory holding several entities
// still loads, lints, validates and builds. The halt names the entity and every
// other entity beside it, so the reader knows which file to move.
// Category: CATALOG_ERROR. Match with errors.Is(err, ErrEntitySharesItsDirectory()).
func ErrEntitySharesItsDirectory() error { return errEntitySharesItsDirectory }

var errEntitySharesItsDirectory = cerrors.Register("entity-shares-its-directory", cerrors.Meta{
	ShortCode: "E0303",
	Message:   "the entity shares its directory with another entity, and an entity of its kind has a directory of its own",
	RulePlace: cerrors.RuleNoStageReaches("The pin step evaluates the rule once over each entity whose lock it is about to " +
		"write, and a publication evaluates it once over the entity it is about to package, listing the other " +
		"documents in the entity's directory. Neither moment is a compilation, and a directory listing is not a " +
		"document a compilation reads, so no declared stage holds the fact the rule turns on."),
})

// RefuseSharedDirectory returns the shared-directory halt,
// [ErrEntitySharesItsDirectory], for the entity document at entityPath when
// another entity sits in the same directory and the entity's kind is not one the
// entity-kind vocabulary lets share a directory. It returns nil for an entity
// alone in its directory, for an entity of a kind that may share one, and for a
// document that declares no entity kind at all, which is its caller's to refuse.
//
// # One predicate, asked at both producing moments
//
// The pin step asks it over every entity whose lock it is about to write, and a
// publication asks it over the entity it is about to package, whether or not
// that publication pins. Both reach this function rather than a copy of it, so
// the two cannot come to disagree about which directories are admitted, and a
// refusal from either is the same diagnostic.
//
// # What it reads, and what it never reads
//
// It reads which entities sit beside this one and nothing more: for each other
// YAML document directly in the directory, the `kind:` that makes it an entity —
// a kind in the entity-kind vocabulary, the same membership a catalog directory
// walk indexes by — and the `metadata.name` the diagnostic names it by. A
// document of no entity kind, such as a compose fragment, a Kubernetes manifest
// or a chart's own metadata, is not an entity and is not in the way. A
// subdirectory is not beside the entity at all, whatever it holds. A neighbour's
// declarations are never read, so nothing a neighbour declares, and nothing its
// sources would resolve to, can change the verdict — which is what lets the
// answer be the same with and without a network.
//
// The entity's own kind is read from its own document rather than taken from the
// caller, so no caller can hand in a kind the document does not declare.
func RefuseSharedDirectory(entityPath string) error {
	abs, err := filepath.Abs(entityPath)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", entityPath, err)
	}
	self, isEntity, err := readEntityPreamble(abs)
	if err != nil {
		return fmt.Errorf("read %s: %w", entityPath, err)
	}
	if !isEntity || entitykind.SharesDirectory(entitykind.Kind(self.kind)) {
		return nil
	}

	dir := filepath.Dir(abs)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("list the directory %s holding %s: %w", dir, filepath.Base(abs), err)
	}
	// os.ReadDir returns the entries sorted by file name, so the neighbours are
	// named in the same order on every run.
	var beside []neighbour
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}
		path := filepath.Join(dir, name)
		if path == abs {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("read %s beside %s: %w", name, filepath.Base(abs), err)
		}
		if info.IsDir() {
			continue
		}
		other, isEntity, err := readEntityPreamble(path)
		if err != nil {
			return fmt.Errorf("read %s beside %s: %w", name, filepath.Base(abs), err)
		}
		if isEntity {
			beside = append(beside, neighbour{file: name, kind: other.kind, name: other.name})
		}
	}
	if len(beside) == 0 {
		return nil
	}
	return sharedDirectory(abs, self, beside)
}

// entityPreamble is the part of a document the shared-directory predicate reads:
// the kind that makes it an entity, and the name a diagnostic names it by.
type entityPreamble struct {
	kind string
	name string
}

// neighbour is one other entity sitting in the directory being judged.
type neighbour struct {
	file string
	kind string
	name string
}

// readEntityPreamble reads the kind and name of the document at path, and reports
// whether its kind is an entity kind.
//
// The kind is decoded on its own before the name is attempted, so a document
// declaring an entity kind counts as an entity even where the rest of its
// metadata is malformed — the malformation is the catalog's to report when it
// reads that document, and it does not make the document any less in the way. A
// file that is not YAML, or declares no kind, is not an entity.
func readEntityPreamble(path string) (entityPreamble, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return entityPreamble{}, false, err
	}
	var kindOnly struct {
		Kind string `yaml:"kind"`
	}
	if yaml.Unmarshal(data, &kindOnly) != nil {
		return entityPreamble{}, false, nil
	}
	if !entitykind.IsKind(entitykind.Kind(kindOnly.Kind)) {
		return entityPreamble{}, false, nil
	}
	var named struct {
		Metadata struct {
			Name string `yaml:"name"`
		} `yaml:"metadata"`
	}
	_ = yaml.Unmarshal(data, &named)
	return entityPreamble{kind: kindOnly.Kind, name: named.Metadata.Name}, true, nil
}

// sharedDirectory composes the shared-directory halt for the entity at abs and
// the entities beside it, positioned at the entity's own document.
func sharedDirectory(abs string, self entityPreamble, beside []neighbour) error {
	condition := ErrEntitySharesItsDirectory()
	values := []string{fmt.Sprintf("%s %q", self.kind, self.name)}
	files := make([]string, 0, len(beside))
	for _, n := range beside {
		values = append(values, fmt.Sprintf("beside it: %s %q in %s", n.kind, n.name, n.file))
		files = append(files, n.file)
	}
	return cerrors.Raise(errEntitySharesItsDirectory, cerrors.CatalogError, cerrors.SourceLocation{File: abs},
		cerrors.ComposeMessage(condition, values, sharedDirectoryRemediation(files)), nil)
}

// sharedDirectoryRemediation is what the shared-directory halt tells its reader
// to do: give each entity a directory of its own, naming the files in the way,
// and naming the kinds that may share a directory so the reader can see the rule
// rather than infer it. The kinds are read from the vocabulary, so the sentence
// cannot come to name a set the predicate does not apply.
func sharedDirectoryRemediation(files []string) string {
	move := "move " + files[0] + " into a directory of its own"
	if len(files) > 1 {
		move = "move each of " + strings.Join(files, ", ") + " into a directory of its own"
	}
	advice := "remediation: give each entity a directory of its own — " + move +
		", together with any file only that entity reads"
	if kinds := sharingKinds(); kinds != "" {
		advice += "; only the " + kinds + " kinds may share a directory with other entities"
	}
	return advice
}

// sharingKinds renders the kinds that may share a directory as one English list,
// and the empty string where the vocabulary lets none share.
func sharingKinds() string {
	kinds := entitykind.DirectorySharing()
	names := make([]string, 0, len(kinds))
	for _, k := range kinds {
		names = append(names, string(k))
	}
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}
