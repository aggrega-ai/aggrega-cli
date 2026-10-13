// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importops

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// SourceReadError reports a source an import could not read: a file that could
// not be opened, a directory that could not be listed, or a file of a directory
// that one of the importers' file patterns selects and that could not be opened.
//
// It is a distinct type so a surface names the place it could not read in its own
// words without parsing the message, as it names the format refusals. Its message
// is the command line's.
type SourceReadError struct {
	// Path is the place the import could not read, spelled as the import was handed
	// the source: the source itself, or a file beneath a directory source.
	Path string
	// Err is what reading it failed with.
	Err error
}

// Error names the place that could not be read and why.
func (e *SourceReadError) Error() string { return fmt.Sprintf("read source %q: %v", e.Path, e.Err) }

// Unwrap returns what reading the source failed with.
func (e *SourceReadError) Unwrap() error { return e.Err }

// DirectoryLinkError reports a link inside a directory source that leads out of
// the directory. It carries the occurrence of the read-leads-out-of-workspace
// condition the judgement raised, so errors.Is against
// [readscope.ErrReadLeadsOutOfWorkspace] and the short-code read both reach it.
//
// It is a distinct type so a surface wording the refusal for a caller who may not
// be told where the link leads names the link by its place in the directory
// without reading the message, which names where it leads.
type DirectoryLinkError struct {
	// Directory is the directory source, spelled as the import was handed it.
	Directory string
	// Link is the link's slash-separated name inside Directory.
	Link string

	err error
}

// Error renders the condition's occurrence: the link, the directory it must stay
// inside, where it leads, and the remedy.
func (e *DirectoryLinkError) Error() string { return e.err.Error() }

// Unwrap returns the occurrence of the registered condition.
func (e *DirectoryLinkError) Unwrap() error { return e.err }

// Source is the source one import reads, as [Import] found it at the path it was
// handed: one file, read whole, or a directory, listed.
//
// A directory's listing names every file it holds, at any depth, by its
// slash-separated path relative to the directory, sorted by that name byte by byte
// — never by the order a walk meets them, and never carrying the directory's own
// name or a host separator — so the same directory yields the same names in the
// same order on every platform. Listing opens no file: a file's bytes are read the
// first time a recognition or the importer's selection needs them, and at most
// once.
//
// What a listing holds is what the import may take something from: regular files,
// and links to regular files, each named by the link's own place. A link to a
// directory is listed so it can be judged, and is never entered. A link leading
// nowhere or round a loop has nothing behind it and is passed over, as are pipes,
// sockets and devices, which are never opened.
type Source struct {
	// name is the source as the caller received it, for a refusal to name.
	name string
	// path is the place the source is read from on this host.
	path string
	// directory is set for a directory source.
	directory bool
	// entries is the one file of a file source, or the directory's listing.
	entries []*sourceFile
}

// sourceFile is one entry of a [Source].
type sourceFile struct {
	// name is the slash-separated name the entry is matched and handed on under:
	// its path relative to the directory, or a single file's name as received.
	name string
	// path is where the entry is read from on this host.
	path string
	// link is set for a link; dir is set beside it for a link to a directory.
	link, dir bool
	// judged is set once the link has been held to the directory.
	judged bool
	// content holds the bytes once read is set.
	content []byte
	read    bool
}

// bytes returns the entry's bytes, reading them the first time they are asked
// for.
func (f *sourceFile) bytes() ([]byte, error) {
	if !f.read {
		content, err := os.ReadFile(f.path)
		if err != nil {
			return nil, &SourceReadError{Path: f.path, Err: err}
		}
		f.content, f.read = content, true
	}
	return f.content, nil
}

// readSource finds the source at path, which the caller received as name: a
// directory is listed, and anything else is read whole exactly as a file is read,
// so a single file's bytes are the bytes os.ReadFile returns and a path naming
// nothing is refused with the error that read gives. A path that is a link is
// used where it leads, as every place a person names is.
func readSource(path, name string) (*Source, error) {
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		entries, err := listDirectory(path)
		if err != nil {
			return nil, &SourceReadError{Path: path, Err: err}
		}
		return &Source{name: name, path: path, directory: true, entries: entries}, nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, &SourceReadError{Path: path, Err: err}
	}
	return &Source{name: name, path: path, entries: []*sourceFile{{
		name: filepath.ToSlash(name), path: path, content: content, read: true,
	}}}, nil
}

// listDirectory lists what a directory source holds, as [Source] describes, sorted
// by name. A directory beneath it that cannot be listed fails the listing.
func listDirectory(dir string) ([]*sourceFile, error) {
	var entries []*sourceFile
	err := pathscope.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == dir || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		entry := &sourceFile{name: filepath.ToSlash(rel), path: p}
		switch {
		case d.Type().IsRegular():
		case d.Type()&fs.ModeSymlink != 0:
			target, serr := os.Stat(p)
			if serr != nil {
				return nil
			}
			switch {
			case target.Mode().IsRegular():
				entry.link = true
			case target.IsDir():
				entry.link, entry.dir = true, true
			default:
				return nil
			}
		default:
			return nil
		}
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(entries, func(a, b *sourceFile) int { return strings.Compare(a.name, b.name) })
	return entries, nil
}

// applies reports whether a signature declaring patterns applies to the entry
// named name. For a single file a signature declaring no pattern applies whatever
// the file is called; for a directory it applies to none of its files, so a
// directory is never read by a signature that names no file.
func (s *Source) applies(patterns []string, name string) bool {
	if s.directory && len(patterns) == 0 {
		return false
	}
	return filesMatch(patterns, name)
}

// judgeLinks holds to the directory, before anything is read through it, every
// link the read would take something from: every link to a directory, which is
// never entered, and every link to a file selects admits that is not judged yet,
// in name order. The first leading out of the directory refuses, naming it.
//
// The directory is the root of its own reads wherever it lies, inside the
// workspace as much as outside it, and the judgement is the workspace's one
// definition of inside, through [readscope.CheckImportDirectoryRead]. A link to a
// file selects does not admit is neither judged nor read. A file source holds no
// link to judge.
func (s *Source) judgeLinks(selects func(name string) bool) error {
	for _, e := range s.entries {
		if !e.link || e.judged || (!e.dir && !selects(e.name)) {
			continue
		}
		if err := readscope.CheckImportDirectoryRead(s.path, e.path); err != nil {
			return &DirectoryLinkError{Directory: s.path, Link: e.name, err: err}
		}
		e.judged = true
	}
	return nil
}

// inputsFor returns what the importer serving format is handed from this source:
// the primary bytes, and the named sources read from it.
//
// A file source is handed whole as the primary bytes, with no named source. A
// directory source is handed as named sources and empty primary bytes, one source
// per file the importer's declared file patterns select, named by its path inside
// the directory, in name order — after every link the selection would read
// through has been judged, so a link leading out refuses before any file is
// opened. An importer declaring no file pattern is refused before anything is
// read, and so is a directory holding no file its patterns select.
func (s *Source) inputsFor(importer types.Plugin, format string) ([]byte, []abi.ImportSource, error) {
	if !s.directory {
		return s.entries[0].content, nil, nil
	}
	if !declaresFilePatterns(importer) {
		return nil, nil, cerrors.Raise(errImporterDeclaresNoFilePatterns, "", cerrors.SourceLocation{},
			cerrors.ComposeMessage(errImporterDeclaresNoFilePatterns,
				[]string{"format " + strconv.Quote(format), "importer " + strconv.Quote(importer.Coordinate()),
					"directory " + strconv.Quote(s.name)},
				"Import one of the directory's files on its own instead; the importer reads a directory once it "+
					"declares the file patterns its files are named by"), nil)
	}
	selects := func(name string) bool { return selectsFile(importer, name) }
	if err := s.judgeLinks(selects); err != nil {
		return nil, nil, err
	}
	var sources []abi.ImportSource
	for _, f := range s.entries {
		if f.dir || !selects(f.name) {
			continue
		}
		content, err := f.bytes()
		if err != nil {
			return nil, nil, err
		}
		sources = append(sources, abi.ImportSource{Name: f.name, Bytes: content})
	}
	if len(sources) == 0 {
		return nil, nil, cerrors.Raise(errImportDirectoryMatchedNothing, "", cerrors.SourceLocation{},
			cerrors.ComposeMessage(errImportDirectoryMatchedNothing,
				[]string{"format " + strconv.Quote(format), "importer " + strconv.Quote(importer.Coordinate()),
					"directory " + strconv.Quote(s.name), "file patterns " + quotedList(filePatternsOf(importer))},
				"Name a directory holding a file one of those patterns matches, or the format of an importer whose "+
					"patterns match the files this directory holds"), nil)
	}
	return nil, sources, nil
}

// mergeSources returns the named sources the importer is handed: those read from a
// directory source and those the attachment configuring it names in its
// with.sources, in one list sorted by name.
//
// The attachment's sources keep the base names they are read under. A name both
// carry is refused rather than handed on twice, naming the name and the
// with.sources file, because a name is how an importer tells one source from
// another. With nothing read from a directory, the attachment's sources are handed
// on exactly as they were read.
func mergeSources(read []abi.ImportSource, attached []attachedSource) ([]abi.ImportSource, error) {
	if len(read) == 0 {
		if len(attached) == 0 {
			return nil, nil
		}
		out := make([]abi.ImportSource, 0, len(attached))
		for _, a := range attached {
			out = append(out, a.source)
		}
		return out, nil
	}
	out := slices.Clone(read)
	for _, a := range attached {
		if _, found := slices.BinarySearchFunc(read, a.source.Name, func(s abi.ImportSource, name string) int {
			return strings.Compare(s.Name, name)
		}); found {
			return nil, cerrors.Raise(errImportSourceNameCollision, "", cerrors.SourceLocation{},
				cerrors.ComposeMessage(errImportSourceNameCollision,
					[]string{"name " + strconv.Quote(a.source.Name), "the with.sources file " + strconv.Quote(a.path)},
					"Each source reaches the importer under one name, so rename one of the two files, or leave the "+
						"file out of the importer's with.sources"), nil)
		}
		out = append(out, a.source)
	}
	slices.SortStableFunc(out, func(a, b abi.ImportSource) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// quotedList renders values quoted and joined by commas.
func quotedList(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, v := range values {
		quoted = append(quoted, strconv.Quote(v))
	}
	return strings.Join(quoted, ", ")
}
