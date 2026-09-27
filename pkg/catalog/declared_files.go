// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/sourceref"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// DeclaredFiles is what one entity's own declarations name beside its document:
// the files a publication of the entity carries, each under the declaration that
// names it.
//
// It is the one answer to "which files belong to this entity", and it is built
// from declarations rather than from a directory listing. A file sitting beside
// the entity that nothing it declares names is not part of it, whatever it is
// called, so a note or a workspace's own files never travel with an entity and
// never change the shape it publishes in. Two entities sharing a directory each
// carry only what they declare, because no declaration of one can name the
// other's files.
//
// The set is what the compiler can determine from the entity's document alone,
// with no provider and no Release in hand. Which files inside a declared source
// an ecosystem would treat as belonging to it is a provider's question and is not
// asked here: a declared source directory contributes every file under it, bar the
// hidden ones.
type DeclaredFiles struct {
	// Document is the path of the entity document the declarations were read
	// from, as the caller named it.
	Document string
	// Declarations holds each declaration that names content beside the
	// document, in the order the entity's kind declares its surfaces and, within
	// one surface, the order its entries were authored in.
	Declarations []Declaration
}

// Declaration is one surface of an entity that names content beside its
// document, and the files it contributes.
type Declaration struct {
	// Surface is where the entity declares the content, in the document's own
	// field path: spec.source, spec.valuesMapping, spec.workload,
	// spec.artifacts.<name>, spec.schema and each of its pillars,
	// spec.usesSchema.<socket>.valueSchema and .infraSchema — or entity.lock,
	// the pins the author's pin step records beside the entity.
	Surface string
	// Files lists each file the declaration contributes, as a slash-separated
	// path relative to the directory holding the document, in lexical order. A
	// declaration naming a path that is not on disk contributes none, which is
	// exactly what a publication could carry of it.
	Files []string
	// WholeDirectory is the authored reference of a local source written as a
	// template, and is empty otherwise. Such a source picks its directory only
	// once a Release fills the template in, so no read of the document can say
	// which directory it names; its contribution is therefore every file in the
	// entity's own directory, bar the hidden ones, so whichever one a Release
	// picks has travelled. That is more than the source needs, and it is said
	// rather than done silently: a publication reports it.
	WholeDirectory string
}

// Files returns every file the declarations contribute, once each and in lexical
// order, leaving out the entity document itself — which every publication
// carries, and which a declaration naming the entity's whole directory reaches as
// well.
func (d DeclaredFiles) Files() []string {
	document := filepath.Base(d.Document)
	seen := map[string]bool{document: true}
	var out []string
	for _, decl := range d.Declarations {
		for _, f := range decl.Files {
			if seen[f] {
				continue
			}
			seen[f] = true
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

// WholeDirectory returns the authored reference of the templated local source
// that made the entity's whole directory part of what it carries, or "" when no
// declaration did.
func (d DeclaredFiles) WholeDirectory() string {
	for _, decl := range d.Declarations {
		if decl.WholeDirectory != "" {
			return decl.WholeDirectory
		}
	}
	return ""
}

// LocalSourceFiles returns the files the entity's local source contributes to
// what it carries, and whether the entity declares a local source at all.
//
// They are the files of its spec.source declaration, bar two that the declaration
// can reach without being about them: the entity document, which is the entity
// rather than an input it reads, and the lock beside it, which cannot record its
// own digest. Both lie inside the source only where the source's directory is the
// entity's own — a source naming that directory, or one written as a template,
// which contributes every file there.
//
// This is the subject of the lock's local source pins: the pin step records one
// pin per file named here and a build compares the same files, so what the lock
// holds for a source is exactly what a publication carries for it.
func (d DeclaredFiles) LocalSourceFiles() ([]string, bool) {
	for _, decl := range d.Declarations {
		if decl.Surface != sourceSurface {
			continue
		}
		document := filepath.Base(d.Document)
		files := make([]string, 0, len(decl.Files))
		for _, f := range decl.Files {
			if f == document || f == recipepin.LockFileName {
				continue
			}
			files = append(files, f)
		}
		return files, true
	}
	return nil, false
}

// LocalSource is an entity's local source as it stands beside its document:
// whether the entity declares one at all, and the pins its files digest to now.
//
// The two answers are kept apart because they are different facts. An entity
// that declares no local source and one whose declared source holds no file
// both have no pins here, and they are not the same entity: the second still
// reads its source, so a lock's pins for it are pins of an input it reads, and
// comparing them with no file is the change to the source it is.
type LocalSource struct {
	// Declared reports whether the entity declares a local source. It is the
	// declared set's answer — the spec.source declaration [DeclaredFiles.LocalSourceFiles]
	// reads — and never a reading of whether the source holds any file.
	Declared bool

	// Pins is what the pin step records for the source now, one per file
	// [DeclaredFiles.LocalSourceFiles] names, in path order. It is empty where
	// the entity declares no local source and where the source it declares holds
	// no file.
	Pins []types.LocalSourceDigest
}

// RecordedPinsInForce returns the pins of recorded — the local source pins a
// lock beside the entity records — that are pins of this entity's local source:
// every one of them where the entity declares a local source, and none where it
// declares none.
//
// A lock's local source pins are the pins of the one local source its entity
// declares, so beside an entity that declares none they pin nothing the entity
// reads. They are then passed over, as a pin for any other input an entity no
// longer reads is: nothing a build compares is missing, and the next pin drops
// them. A pull holding the lock to the pin step and a verifying pull both read a
// lock's local source pins through this, and a build compares them only over a
// source it forwards as local, which is a source the entity declares — so the
// three compare a lock's local source pins beside the same entities.
func (s LocalSource) RecordedPinsInForce(recorded []types.LocalSourceDigest) []types.LocalSourceDigest {
	if !s.Declared {
		return nil
	}
	return recorded
}

// LocalSourceOf reads the local source of the entity document data found at
// path: whether the entity declares one, and the pins the pin step records for
// it, one per file [DeclaredFiles.LocalSourceFiles] names.
//
// It reads the entity through [DeclaredFilesOf], the one function that says which
// files an entity carries, so the pin step, a build's comparison and a
// publication's bundle cannot come to disagree about what a source contributes.
func LocalSourceOf(data []byte, path string) (LocalSource, error) {
	declared, err := DeclaredFilesOf(data, path)
	if err != nil {
		return LocalSource{}, err
	}
	files, declaresLocalSource := declared.LocalSourceFiles()
	if !declaresLocalSource {
		return LocalSource{}, nil
	}
	dir, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return LocalSource{}, err
	}
	pins, err := recipepin.LocalSourcePins(dir, files)
	if err != nil {
		return LocalSource{}, err
	}
	return LocalSource{Declared: true, Pins: pins}, nil
}

// LocalSourceAt is [LocalSourceOf] over the entity document on disk at path.
func LocalSourceAt(path string) (LocalSource, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return LocalSource{}, fmt.Errorf("read %s: %w", path, err)
	}
	return LocalSourceOf(data, path)
}

// LocalSourcePinsAt returns the pins the pin step records now for the local
// source of the entity document on disk at path — [LocalSourceAt]'s pins — or
// none where the entity declares no local source.
//
// It is the reading a build is handed through its catalog lookup, and a build
// reaches it only over a source it forwards as local, which is a source the
// entity declares.
func LocalSourcePinsAt(path string) ([]types.LocalSourceDigest, error) {
	src, err := LocalSourceAt(path)
	if err != nil {
		return nil, err
	}
	return src.Pins, nil
}

// sourceSurface names the declaration a Recipe's or FeatureRecipe's local source
// is carried under, in the document's own field path.
const sourceSurface = "spec.source"

// entityLockSurface names the declaration a lock beside the entity is carried
// under. It is not a field of the document: the lock is written by the author's
// pin step, and it travels because it is what records the digests of the
// entity's other inputs.
const entityLockSurface = recipepin.LockFileName

// DeclaredFilesOf reads the entity document data, found at path, through the same
// per-entity parse a catalog load runs, and returns the files its declarations
// name beside it.
//
// Reading through the parse is what makes the answer trustworthy rather than a
// second opinion: every path it collects is one the parse has already anchored to
// the directory holding the document and held inside it, so no file outside that
// directory can be named, and a document the parse refuses is refused here under
// the same condition. The kind is read from the document itself.
//
// Recipe and FeatureRecipe declare a source, a values mapping, a workload, their
// artifacts, and carry the lock beside them; Component and Feature declare the
// schema files their contract imports, every file of an import chain included.
// Every other kind declares no file beside its document — an Aggregate's paths
// each name another entity, which is resolved and pinned on its own terms — so
// its set is the document alone.
func DeclaredFilesOf(data []byte, path string) (DeclaredFiles, error) {
	out := DeclaredFiles{Document: path}
	dir, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return DeclaredFiles{}, err
	}
	var decls []Declaration
	switch peekEntityKind(data) {
	case "Recipe":
		e, err := resolveRecipeDocument(data, path)
		if err != nil {
			return DeclaredFiles{}, err
		}
		decls, err = sourcedDeclarations(dir, path, e.Spec.Source,
			importedFile{"spec.valuesMapping", e.Spec.ValuesMapping.Import, e.Spec.ValuesMapping},
			importedFile{"spec.workload", e.Spec.Workload.Import, e.Spec.Workload},
			artifactImports(&e.Spec.Artifacts))
		if err != nil {
			return DeclaredFiles{}, err
		}
	case "FeatureRecipe":
		e, err := resolveFeatureRecipeDocument(data, path)
		if err != nil {
			return DeclaredFiles{}, err
		}
		decls, err = sourcedDeclarations(dir, path, e.Spec.Source,
			importedFile{"spec.valuesMapping", e.Spec.ValuesMapping.Import, e.Spec.ValuesMapping},
			importedFile{"spec.workload", e.Spec.Workload.Import, e.Spec.Workload},
			artifactImports(&e.Spec.Artifacts))
		if err != nil {
			return DeclaredFiles{}, err
		}
	case "Component":
		var reads schemaFileReads
		if _, err := resolveComponentDocument(data, path, reads.record); err != nil {
			return DeclaredFiles{}, err
		}
		if decls, err = reads.declarations(dir); err != nil {
			return DeclaredFiles{}, err
		}
	case "Feature":
		var reads schemaFileReads
		if _, err := resolveFeatureDocument(data, path, reads.record); err != nil {
			return DeclaredFiles{}, err
		}
		if decls, err = reads.declarations(dir); err != nil {
			return DeclaredFiles{}, err
		}
	}
	out.Declarations = decls
	return out, nil
}

// importedFile is one import-bearing field of a Recipe or FeatureRecipe, after the
// parse: the surface it sits at, its import target as the parse left it, and the
// field itself, which answers whether that target names a file.
type importedFile struct {
	surface string
	target  string
	ref     importRef
}

// artifactImports lists a Recipe's or FeatureRecipe's artifacts entries as
// imported files, in author order. The two kinds author the block identically,
// which is why one function serves both.
func artifactImports(artifacts *types.OrderedMap[types.ArtifactEntry]) []importedFile {
	keys := artifacts.Keys()
	out := make([]importedFile, 0, len(keys))
	for _, name := range keys {
		entry, _ := artifacts.Get(name)
		out = append(out, importedFile{"spec.artifacts." + name, entry.Import, entry})
	}
	return out
}

// sourcedDeclarations collects the declarations a Recipe or a FeatureRecipe
// makes, in the order the kind declares them: its source, each import-bearing
// field, and the lock beside it.
func sourcedDeclarations(dir, path string, source any, valuesMapping, workload importedFile, artifacts []importedFile) ([]Declaration, error) {
	var decls []Declaration
	if ref, readable := sourceref.Reference(source); readable && sourceref.IsLocalURL(ref) {
		decl := Declaration{Surface: sourceSurface}
		target := ref
		if sourceref.IsTemplated(ref) {
			decl.WholeDirectory = ref
			target = dir
		}
		files, err := filesUnder(dir, target)
		if err != nil {
			return nil, fmt.Errorf("list the files spec.source names: %w", err)
		}
		decl.Files = files
		decls = append(decls, decl)
	}
	for _, imp := range append([]importedFile{valuesMapping, workload}, artifacts...) {
		if !importNamesFile(imp.target, imp.ref) {
			continue
		}
		files, err := filesUnder(dir, imp.target)
		if err != nil {
			return nil, fmt.Errorf("list the file %s imports: %w", imp.surface, err)
		}
		decls = append(decls, Declaration{Surface: imp.surface, Files: files})
	}
	if _, err := os.Stat(recipepin.LockPath(path)); err == nil {
		decls = append(decls, Declaration{Surface: entityLockSurface, Files: []string{recipepin.LockFileName}})
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return decls, nil
}

// schemaFileReads accumulates the files a Component's or Feature's schema
// imports read, grouped by the surface whose import led to each.
type schemaFileReads struct {
	surfaces []string
	files    map[string][]string
}

func (r *schemaFileReads) record(surface, file string) {
	if r.files == nil {
		r.files = map[string][]string{}
	}
	if _, ok := r.files[surface]; !ok {
		r.surfaces = append(r.surfaces, surface)
	}
	r.files[surface] = append(r.files[surface], file)
}

// declarations returns one declaration per surface that imported anything, in the
// order the surfaces were reached, each carrying the files its chain read.
func (r *schemaFileReads) declarations(dir string) ([]Declaration, error) {
	decls := make([]Declaration, 0, len(r.surfaces))
	for _, surface := range r.surfaces {
		var files []string
		for _, abs := range r.files[surface] {
			rel, err := relativeTo(dir, abs)
			if err != nil {
				return nil, err
			}
			files = append(files, rel)
		}
		sort.Strings(files)
		decls = append(decls, Declaration{Surface: surface, Files: files})
	}
	return decls, nil
}

// filesUnder returns the files a declared path contributes, relative to dir: the
// path itself where it is a file, and every file beneath it where it is a
// directory, bar a file whose name starts with a dot. A path that is not on disk
// contributes nothing.
//
// A file named directly is taken whatever it is called, because a declaration
// naming it is the author asking for it. The dot rule applies only to what a walk
// of a declared directory finds — editor leftovers and version-control files an
// author would not mean to publish — and is the one rule about which files a
// declared directory contributes.
func filesUnder(dir, target string) ([]string, error) {
	info, err := os.Stat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		rel, err := relativeTo(dir, target)
		if err != nil {
			return nil, err
		}
		return []string{rel}, nil
	}
	var files []string
	err = filepath.WalkDir(target, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		rel, err := relativeTo(dir, p)
		if err != nil {
			return err
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

// relativeTo spells an absolute path inside dir as the slash-separated path a
// publication carries it under.
func relativeTo(dir, abs string) (string, error) {
	rel, err := filepath.Rel(dir, abs)
	if err != nil {
		return "", err
	}
	if !filepath.IsLocal(rel) {
		return "", fmt.Errorf("%s is not inside %s", abs, dir)
	}
	return filepath.ToSlash(rel), nil
}
