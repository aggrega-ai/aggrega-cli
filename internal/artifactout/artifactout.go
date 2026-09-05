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
// [WriteAll] and [WriteDryRunReport] are the only entry points. The path
// arithmetic and the iteration order behind them are unexported, so a caller
// cannot reach the raw join and skip the containment check [WriteAll] owes —
// the bypass does not compile rather than being caught in review.
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
