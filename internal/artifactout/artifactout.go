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
// [WriteBuildOutput], [WriteAll] and [WriteDryRunReport] are the only entry
// points. The path arithmetic and the iteration order behind them are
// unexported, so a caller cannot reach the raw join and skip the containment
// check [WriteAll] owes — the bypass does not compile rather than being caught
// in review.
//
// [WriteBuildOutput] is what a surface performing a build calls;
// [WriteAll] is the bare write, for a set that is not a build's output — the
// per-step intermediate files a traced build collects, whose own directory is
// cleared by whoever asked for it.
package artifactout

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
)

// DefaultOutputDir is the directory a build writes its rendered artifacts to
// when it is told nothing else. It is relative, so it resolves against whatever
// the caller treats as the build's working location — the invocation directory
// on the command line, the anchored workspace root in the daemon.
//
// It lives here rather than beside either caller because both surfaces must
// answer "where does a build put its files" with the same word: a reader
// comparing one surface's output against the other's finds the same directory,
// and a change to the default reaches both at once.
const DefaultOutputDir = "dist"

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
// Errors from either half are returned wrapped and name the path they concern.
func WriteBuildOutput(outRoot string, files map[string][]byte) error {
	boundaries := filepath.Join(filepath.Clean(outRoot), boundariesDir)
	if err := os.RemoveAll(boundaries); err != nil {
		return fmt.Errorf("clean %q: %w", boundaries, err)
	}
	return WriteAll(outRoot, files)
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
func WriteAll(outRoot string, files map[string][]byte) error {
	outRoot = filepath.Clean(outRoot)
	for _, name := range sortedNames(files) {
		if name == "" {
			return fmt.Errorf("empty artifact path")
		}
		if !pathscope.IsLocalSlash(name) {
			return fmt.Errorf("invalid artifact path %q (path escapes the output root)", name)
		}
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
// followed by its bytes. The header is the familiar multi-file form, so a reader
// can tell a header apart from content that happens to open with a comment
// marker.
//
// The path reported is computed by the same function that computes the path a
// real build writes to, so what a dry run says a file would be called and what a
// build calls it cannot come apart. The order is the same one a build writes in,
// which makes the report byte-identical across runs of the same input rather
// than reshuffled by map iteration.
//
// A trailing newline is supplied where the artifact's own bytes lack one, so the
// next header starts at a column rather than butting onto the last line of
// content; the blank line after each artifact is the separator between them.
func WriteDryRunReport(out io.Writer, outDir string, files map[string][]byte) {
	for _, name := range sortedNames(files) {
		body := files[name]
		fmt.Fprintf(out, "==> %s <==\n", hostPath(outDir, name))
		_, _ = out.Write(body)
		if len(body) > 0 && body[len(body)-1] != '\n' {
			fmt.Fprintln(out)
		}
		fmt.Fprintln(out)
	}
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
