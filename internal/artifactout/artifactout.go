// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package artifactout puts a rendered artifact set where it belongs, and
// reports where it would go.
//
// A render produces a map of slash-separated logical names to bytes. Two
// surfaces consume that map: one writes it to an output root, the other reports
// what a write would have produced without performing it. Both need the same
// two answers — which host path a name occupies, and in what order the set is
// walked — so both resolve them here, and a reported path is the path that
// would be written.
//
// # The primitives are unexported on purpose
//
// The exported functions are the only entry points. The path arithmetic and the
// iteration order behind them are unexported, so a caller cannot reach the raw
// join and skip the containment check every write owes — the bypass does not
// compile rather than being caught in review.
//
// [WriteBuildOutput] is what a surface performing a build calls, and
// [CheckBuildOutput] is its judgement alone, for a surface that reports a build
// instead of writing it; [ReplaceAll] is the write for a set that is not a build's
// output — the per-step intermediate files a traced build collects, which replace
// whatever their own directory held; [WriteAll] is the bare write beneath them;
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
// root is the workspace the build runs in. The subtree to clear and every file to
// write are held to it before the clear, so an output directory leading out of
// the workspace through a link halts the build with nothing removed and nothing
// written; see the package documentation.
//
// Errors from either half are returned wrapped and name the path they concern.
func WriteBuildOutput(root, outRoot string, files map[string][]byte) error {
	if err := CheckBuildOutput(root, outRoot, files); err != nil {
		return err
	}
	boundaries := filepath.Join(filepath.Clean(outRoot), boundariesDir)
	if err := os.RemoveAll(boundaries); err != nil {
		return fmt.Errorf("clean %q: %w", boundaries, err)
	}
	return writeChecked(filepath.Clean(outRoot), files)
}

// CheckBuildOutput is the judgement [WriteBuildOutput] makes before it removes or
// writes anything, and nothing else: the boundaries subtree it would clear and
// every file it would write, held to the workspace at root, and every name held
// to the output root. A surface that reports a build instead of writing it asks
// it, so a report says a build would halt where the build would.
func CheckBuildOutput(root, outRoot string, files map[string][]byte) error {
	boundaries := filepath.Join(filepath.Clean(outRoot), boundariesDir)
	if err := writescope.CheckDelete(root, boundaries); err != nil {
		return err
	}
	return checkAll(root, filepath.Clean(outRoot), files)
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
//   - A file whose place leads out of the workspace at root through a
//     link is refused, before any file is written; see the package
//     documentation.
//
// An artifact name is a slash-separated logical name rather than a
// path in the host's own spelling, so containment is judged in slash
// arithmetic — identically on every platform — and the name is
// converted to a host path only to write it. Judging it as a host
// path instead would make a backslash a separator on one platform and
// an ordinary character on another, so the same rendered output would
// be accepted on one machine and refused on the next.
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
	outRoot = filepath.Clean(outRoot)
	if err := checkAll(root, outRoot, files); err != nil {
		return err
	}
	return writeChecked(outRoot, files)
}

// ReplaceAll makes dir hold exactly files: whatever dir held is removed, then the
// set is written by the rules [WriteAll] states.
//
// dir and every file are held to the workspace at root before dir is removed, so a
// directory leading out of the workspace through a link halts the write with
// nothing removed and nothing written.
func ReplaceAll(root, dir string, files map[string][]byte) error {
	dir = filepath.Clean(dir)
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

// checkAll judges every entry of files before any is written: its name must be a
// local logical name, and the host path it occupies under outRoot must stay in
// the workspace at root once the links on the way to it are resolved.
func checkAll(root, outRoot string, files map[string][]byte) error {
	for _, name := range sortedNames(files) {
		if name == "" {
			return fmt.Errorf("empty artifact path")
		}
		if !pathscope.IsLocalSlash(name) {
			return fmt.Errorf("invalid artifact path %q (path escapes the output root)", name)
		}
		if err := writescope.CheckWrite(root, hostPath(outRoot, name)); err != nil {
			return err
		}
	}
	return nil
}

// writeChecked writes files under outRoot in sorted name order. Every entry has
// already passed checkAll.
func writeChecked(outRoot string, files map[string][]byte) error {
	for _, name := range sortedNames(files) {
		full := hostPath(outRoot, name)
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
// The path reported is computed by the same function that computes the path a
// real build writes to, so what a dry run says a file would be called and what a
// build calls it cannot come apart. The order is the same one a build writes in,
// which makes the report byte-identical across runs of the same input rather
// than reshuffled by map iteration.
//
// What it does NOT own is the report's shape. That shape is a contract a reader
// learns, and a command whose product is not a rendered artifact set prints the
// same one — so the rendering lives in [dryrunreport], and this function's whole
// job is to answer the two questions that are this package's: which host path
// each name occupies, and in what order the set is walked.
func WriteDryRunReport(out io.Writer, outDir string, files map[string][]byte) {
	names := sortedNames(files)
	report := make([]dryrunreport.File, 0, len(names))
	for _, name := range names {
		report = append(report, dryrunreport.File{Path: hostPath(outDir, name), Content: files[name]})
	}
	dryrunreport.Write(out, report)
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

// hostPath returns the host path an artifact name occupies under outRoot.
// An artifact name is a slash-separated logical name rather than a path in the
// host's own spelling, so it is converted rather than concatenated. Both the
// surface that writes a file and the surface that reports where one would go
// resolve it here, so a reported path is the path that would be written.
//
// It does not judge containment: the write path checks the name first (see
// [WriteAll]), and the report path has nothing to contain.
func hostPath(outRoot, name string) string {
	return filepath.Join(outRoot, filepath.FromSlash(path.Clean(name)))
}
