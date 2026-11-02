// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package artifactout puts a rendered artifact set where it belongs, and
// reports where it would go.
//
// A render produces a map of slash-separated logical names to bytes. Two
// surfaces consume that map: one writes it to an output root, the other reports
// what a write would have produced without performing it. Both need the same
// two answers — which host path a name occupies, and in what order the set is
// walked — so both resolve them here. A write and a report alike place every name
// through pathscope.PlaceBeneath, the one way a name somebody else chose reaches
// disk, beneath the output directory made a directory path first, so the path a
// report names for a file is the path a write handed the same directory puts it
// at.
//
// # The primitives are unexported on purpose
//
// The exported functions are the only entry points. The path arithmetic and the
// iteration order behind them are unexported, so a caller cannot reach the raw
// join and skip the judgement every write owes — the bypass does not compile
// rather than being caught in review.
//
// [WriteBuildOutput] is what a surface performing a build calls, and
// [CheckBuildOutput] is its judgement alone, for a surface that reports a build
// instead of writing it; [ReplaceAll] is the write for a set that is not a build's
// output — the per-step intermediate files a traced build collects, which replace
// whatever their own directory held; [ReplaceNamed] is the write for a set that
// shares a directory a caller named, replacing only the files of the set's own
// naming; [WriteAll] is the bare write beneath them;
// [WriteDryRunReport] prints what a write would have produced.
//
// # Every write is held to the workspace first
//
// Every writing entry point takes the workspace root and holds every path it is
// about to write or delete to it, through [writescope], before anything on disk
// moves. An output directory that is a link leading out of the workspace would
// otherwise turn a rebuild into deleting and writing a directory elsewhere on the
// machine. The judgement runs over the whole set first, so a set with one path
// leading out writes none of it.
package artifactout

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/dryrunreport"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// DefaultOutputDir is the name of the directory a build writes its rendered
// artifacts to when it is told nothing else. It is a name and not a location: the
// directory a build writes is this name composed beneath a root by
// [DefaultOutputDirIn], never resolved against whatever folder a process happens
// to be running in.
//
// It lives here rather than beside either caller because both surfaces must
// answer "where does a build put its files" with the same word: a reader
// comparing one surface's output against the other's finds the same directory,
// and a change to the default reaches both at once.
const DefaultOutputDir = "dist"

// DefaultOutputDirIn returns the default output directory beneath root: where a
// build writes when nothing names a destination of its own.
//
// It is the one composition of that directory. The build operation every
// command-line build and every command an assistant's command tool runs goes
// through asks it with the workspace root that build resolved; the language
// server asks it with the workspace root its session opened — or with the folder a
// request names as the output directory's parent — and the assistant's in-process
// build tool, asked to write its artifacts, asks it with the workspace root its
// call resolved. So the surfaces write the same directory for the same workspace
// because they compose it in one place, not because three joins happen to agree.
//
// An empty root yields the bare relative name, which is all a caller holding no
// root has to offer. A root a workspace resolution produced is absolute, so the
// directory composed beneath one is absolute too.
func DefaultOutputDirIn(root string) string {
	return filepath.Join(root, DefaultOutputDir)
}

// boundariesDir is the subtree of an output root that a build regenerates in
// full. It is named here because [WriteBuildOutput] is the only thing that acts
// on it, and naming it at that one site is what keeps the clearing and the write
// from being described in two places.
const boundariesDir = "boundaries"

// WriteBuildOutput puts a build's rendered artifact set under outRoot: the
// boundaries subtree a previous build left is removed first, then the set is
// written by [WriteAll].
//
// The clearing is part of writing a build's output rather than a step beside it,
// which is why it is here and not at the call sites. A build regenerates its
// boundaries in full, so a file a previous build of a different variant emitted
// would otherwise survive into a tree that no longer produces it — and a
// consumer reading that tree would apply an artifact the compiler did not just
// render. Every surface that performs a build therefore clears through this one
// entry point; a surface clearing on its own would agree with this one the day
// it was written and drift into leaving one output stale on one surface.
//
// It does not clear the whole output root. Anything a caller puts beside the
// boundaries subtree is theirs, and a write that emptied the root would destroy
// content this package never produced.
//
// # What it reports instead of removing
//
// Once the set is written, it returns every file this write did not produce that
// sits directly in a directory the write put a file in, or passed through to put
// one deeper: the output root itself, and each directory between it and a file
// written. Each is named by its slash-separated path relative to the output root,
// in sorted order, so the same tree is reported identically on every run and every
// platform. Nothing is removed — a file the build cannot prove is its own may be
// one the author keeps there — but a directory mixing this build's files with
// others is no longer silent: [UnwrittenAdvisory] is the one wording every surface
// performing a build tells it in.
//
// The directories looked into are only those the write reached. A subdirectory
// the write put nothing in, and nothing beneath, is not opened, so a tree the
// author keeps beside the build's output is not listed file by file merely for
// sitting there; the boundaries subtree is cleared before the write and holds only
// this build's files after it.
//
// root is the workspace the build runs in. The subtree to clear and every file to
// write are held to it before the clear, so an output directory leading out of
// the workspace through a link halts the build with nothing removed and nothing
// written; see the package documentation.
//
// Errors from either half, and from reading a directory back, are returned
// wrapped and name the path they concern.
func WriteBuildOutput(root, outRoot string, files map[string][]byte) ([]string, error) {
	if err := CheckBuildOutput(root, outRoot, files); err != nil {
		return nil, err
	}
	outRoot, err := pathscope.DirectoryPath(outRoot)
	if err != nil {
		return nil, err
	}
	boundaries := filepath.Join(outRoot, boundariesDir)
	if err := os.RemoveAll(boundaries); err != nil {
		return nil, fmt.Errorf("clean %q: %w", boundaries, err)
	}
	if err := writeChecked(outRoot, files); err != nil {
		return nil, err
	}
	return unwrittenBeside(outRoot, files)
}

// unwrittenBeside lists, relative to outRoot, every file directly in a directory
// the write of files reached that is not one of files. A directory is reached
// when a file was written in it or beneath it, so the output root is reached by
// any write of one file or more — and by none of an empty set, which wrote into
// no directory and may have left the output root uncreated.
//
// Every name is placed by [place] again, so the paths compared are the paths
// writeChecked wrote — a name spelled with a redundant segment is matched as the
// file it produced, not as its spelling.
func unwrittenBeside(outRoot string, files map[string][]byte) ([]string, error) {
	written := make(map[string]bool, len(files))
	reached := map[string]bool{}
	var dirs []string
	for _, name := range sortedNames(files) {
		full, err := place(outRoot, name)
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(outRoot, full)
		if err != nil {
			return nil, fmt.Errorf("relate %q to %q: %w", full, outRoot, err)
		}
		rel = filepath.ToSlash(rel)
		written[rel] = true
		for dir := path.Dir(rel); !reached[dir]; dir = path.Dir(dir) {
			reached[dir] = true
			dirs = append(dirs, dir)
		}
	}
	var unwritten []string
	for _, dir := range dirs {
		host := filepath.Join(outRoot, filepath.FromSlash(dir))
		entries, err := os.ReadDir(host)
		if err != nil {
			return nil, fmt.Errorf("read %q: %w", host, err)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			rel := path.Join(dir, entry.Name())
			if !written[rel] {
				unwritten = append(unwritten, rel)
			}
		}
	}
	sort.Strings(unwritten)
	return unwritten, nil
}

// UnwrittenAdvisory is the one sentence a surface performing a build tells a
// reader the files [WriteBuildOutput] reported in: how many, and each by its
// quoted path relative to the output directory, in the order given. It is empty
// for none, which is the case where a surface tells the reader nothing.
//
// It names no host path, so the same sentence is true on whichever surface relays
// it, and it is the same sentence on all of them: a surface wording its own would
// agree with this one the day it was written and drift into telling a reader on
// one surface something another does not.
func UnwrittenAdvisory(unwritten []string) string {
	if len(unwritten) == 0 {
		return ""
	}
	quoted := make([]string, len(unwritten))
	for i, name := range unwritten {
		quoted[i] = strconv.Quote(name)
	}
	return fmt.Sprintf("the output directory holds %d file(s) this build did not write, in directories it wrote into; "+
		"nothing was removed, so they sit beside this build's output: %s", len(unwritten), strings.Join(quoted, ", "))
}

// CheckBuildOutput is the judgement [WriteBuildOutput] makes before it removes or
// writes anything, and nothing else: the boundaries subtree it would clear and
// every file it would write, held to the workspace at root, and every name placed
// beneath the output root. A surface that reports a build instead of writing it
// asks it, so a report says a build would halt where the build would.
func CheckBuildOutput(root, outRoot string, files map[string][]byte) error {
	outRoot, err := pathscope.DirectoryPath(outRoot)
	if err != nil {
		return err
	}
	boundaries := filepath.Join(outRoot, boundariesDir)
	if err := writescope.CheckDelete(root, boundaries); err != nil {
		return err
	}
	return checkAll(root, outRoot, files)
}

// WriteAll writes the rendered file map to disk under outRoot,
// preserving relative path structure and creating intermediate
// directories as needed. Each entry's name is validated:
//
//   - Empty names are rejected (defensive guard against renderer
//     producing a zero-key entry).
//   - Names that escape outRoot are rejected. The check defends
//     against a malicious or buggy renderer engine emitting a name
//     that lands outside the output root.
//   - Where the process runs on Windows, a name holding a segment
//     Windows cannot hold as written — a name it reserves for a device,
//     or one ending in a full stop or a space — is refused with the halt
//     pathscope.ErrNameWindowsCannotHold names. On every other platform
//     such a name is written as it is.
//   - A file whose place leads out of the workspace at root through a
//     link is refused, before any file is written; see the package
//     documentation.
//
// outRoot is made a directory path ([pathscope.DirectoryPath]) before
// any name is placed beneath it, so an output root named relative to
// the working directory, or as a bare Windows drive, still holds every
// file written beneath it.
//
// An artifact name is a slash-separated logical name rather than a
// path in the host's own spelling, so it is placed by
// [pathscope.PlaceBeneath] — whether it escapes is judged in slash
// arithmetic, identically on every platform — and the name is
// converted to a host path only to write it. Judging it as a host
// path instead would make a backslash a separator on one platform and
// an ordinary character on another, so whether the same rendered output
// escapes would be answered one way on one machine and another way on
// the next.
//
// Entries are written in sorted name order. Go randomises map
// iteration, so writing in range order would emit the same build's
// files in a different sequence on every run: the bytes on disk match,
// but the order of the mkdir and write calls that produced them does
// not, and a consumer watching the tree as it fills — or a filesystem
// recording creation order — sees a different build each time.
//
// On any per-file error (mkdir, write), the function returns the
// wrapped error and stops — partial writes to disk persist; the
// caller is responsible for any cleanup in the partial-write case.
// Sorting also makes that partial prefix reproducible: the same
// failing input stops after the same set of files.
func WriteAll(root, outRoot string, files map[string][]byte) error {
	outRoot, err := pathscope.DirectoryPath(outRoot)
	if err != nil {
		return err
	}
	if err := checkAll(root, outRoot, files); err != nil {
		return err
	}
	return writeChecked(outRoot, files)
}

// ReplaceAll makes dir hold exactly files: whatever dir held is removed, then the
// set is written by the rules [WriteAll] states.
//
// dir is made a directory path first, as [WriteAll]'s output root is, and dir and
// every file are held to the workspace at root before dir is removed, so a
// directory leading out of the workspace through a link halts the write with
// nothing removed and nothing written.
func ReplaceAll(root, dir string, files map[string][]byte) error {
	dir, err := pathscope.DirectoryPath(dir)
	if err != nil {
		return err
	}
	if err := writescope.CheckDelete(root, dir); err != nil {
		return err
	}
	if err := checkAll(root, dir, files); err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("clean %s: %w", dir, err)
	}
	return writeChecked(dir, files)
}

// ReplaceNamed makes dir hold files in place of an earlier set of the same
// naming: every file directly in dir whose name owned reports as one of the
// set's is removed, then files is written by the rules [WriteAll] states.
// Anything else dir holds — another name, a subdirectory — is left as it is,
// because the directory is one a caller named and may hold content this package
// never produced. A set whose names vary from one run to the next therefore
// leaves no file of an earlier run behind to be read as part of this one.
//
// dir is made a directory path first, and every file to write and every file to
// remove is held to the workspace at root before anything is removed, so a path
// leading out of the workspace through a link halts the write with nothing
// removed and nothing written. A dir that does not exist yet is created.
func ReplaceNamed(root, dir string, files map[string][]byte, owned func(name string) bool) error {
	dir, err := pathscope.DirectoryPath(dir)
	if err != nil {
		return err
	}
	if err := writescope.CheckWrite(root, dir); err != nil {
		return err
	}
	if err := checkAll(root, dir, files); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", dir, err)
	}
	var stale []string
	for _, entry := range entries {
		if entry.IsDir() || !owned(entry.Name()) {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if err := writescope.CheckDelete(root, path); err != nil {
			return err
		}
		stale = append(stale, path)
	}
	for _, path := range stale {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove %s: %w", path, err)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %q: %w", dir, err)
	}
	return writeChecked(dir, files)
}

// checkAll judges every entry of files before any is written: its name must be
// one [place] places beneath outRoot, and the host path it occupies there must
// stay in the workspace at root once the links on the way to it are resolved.
func checkAll(root, outRoot string, files map[string][]byte) error {
	for _, name := range sortedNames(files) {
		full, err := place(outRoot, name)
		if err != nil {
			return err
		}
		if err := writescope.CheckWrite(root, full); err != nil {
			return err
		}
	}
	return nil
}

// place returns the host path the artifact name occupies beneath outRoot, by the
// one way a name somebody else chose is placed on disk
// ([pathscope.PlaceBeneath]). An empty name and one escaping the output root are
// refused in this package's words, and on Windows a name Windows cannot hold as
// written is refused with the halt pathscope.ErrNameWindowsCannotHold names.
func place(outRoot, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("empty artifact path")
	}
	full, inside, err := pathscope.PlaceBeneath(outRoot, name)
	if err != nil {
		return "", err
	}
	if !inside {
		return "", fmt.Errorf("invalid artifact path %q (path escapes the output root)", name)
	}
	return full, nil
}

// writeChecked writes files under outRoot in sorted name order. Every entry has
// already passed checkAll, and each is placed by [place] again, so the path written
// is the path checkAll judged.
func writeChecked(outRoot string, files map[string][]byte) error {
	for _, name := range sortedNames(files) {
		full, err := place(outRoot, name)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return fmt.Errorf("mkdir %q: %w", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, files[name], 0o644); err != nil {
			return fmt.Errorf("write %q: %w", full, err)
		}
	}
	return nil
}

// WriteDryRunReport prints what the render produced instead of persisting it:
// one header per artifact naming the path that file would have occupied,
// followed by its bytes.
//
// outDir is made a directory path ([pathscope.DirectoryPath]) and each name is
// placed beneath it by [place], exactly as a write does both, so the path a dry
// run says a file would be called is the path a build handed the same directory
// writes it to — a directory named relative to the working directory included.
// The order is the same one a build writes in, which makes the report
// byte-identical across runs of the same input rather than reshuffled by map
// iteration.
//
// An error is the one a write would meet in making outDir a directory path or in
// placing a name: an empty name, a name escaping the output directory, and on
// Windows a name Windows cannot hold as written. Every name is placed before
// anything is printed, so a set holding one refused name prints nothing.
//
// What it does NOT own is the report's shape. That shape is a contract a reader
// learns, and a command whose product is not a rendered artifact set prints the
// same one — so the rendering lives in [dryrunreport], and this function's whole
// job is to answer the two questions that are this package's: which host path
// each name occupies, and in what order the set is walked.
func WriteDryRunReport(out io.Writer, outDir string, files map[string][]byte) error {
	outDir, err := pathscope.DirectoryPath(outDir)
	if err != nil {
		return err
	}
	names := sortedNames(files)
	report := make([]dryrunreport.File, 0, len(names))
	for _, name := range names {
		full, err := place(outDir, name)
		if err != nil {
			return err
		}
		report = append(report, dryrunreport.File{Path: full, Content: files[name]})
	}
	dryrunreport.Write(out, report)
	return nil
}

// sortedNames returns a rendered file map's names in sorted order — the
// one deterministic iteration order every surface that walks such a map uses.
// Go randomises map iteration, so a surface rolling its own order would report
// or emit the same build differently on every run.
func sortedNames(files map[string][]byte) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
