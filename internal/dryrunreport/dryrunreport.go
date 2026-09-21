// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package dryrunreport carries the one shape a command prints when it is asked
// to show what it would write instead of writing it.
//
// The shape is a user-visible output contract — a reader learns to tell a header
// from content that happens to open with a comment marker — and more than one
// command produces it, from products that have nothing else in common: a
// rendered artifact set, and the files a dependency resolution would put on
// disk. Two renderings of one contract would agree on the day they were written
// and diverge the first time either gained a field, so the rendering lives here
// and each command supplies its own files.
//
// It is deliberately NOT the delivery that hands authored content back to a
// caller to place. That one answers "who writes this file", and it applies where
// the product is content an author will own and go on editing. This one answers
// "what would this command do", and it applies where the product is the
// command's own output: a reader inspects it and does not amend it.
package dryrunreport

import (
	"fmt"
	"io"
)

// File is one file a run would have written: the path it would occupy and the
// bytes it would hold.
//
// The path travels WITH its content rather than in a separate channel, so a
// reader of a several-file report never has to correlate two lists — and a
// command producing several files gives them nothing to correlate on.
type File struct {
	Path    string
	Content []byte
}

// Write prints files as the report: one header per file naming the path it would
// occupy, followed by its bytes.
//
// The order is the caller's own, because what a deterministic order IS belongs
// to the command: one walks a rendered set by logical name, another sorts by
// host path. Taking an ordered slice rather than a map is what keeps that
// decision with the caller instead of imposing one order on every producer.
//
// A trailing newline is supplied where a file's own bytes lack one, so the next
// header starts at a column rather than butting onto the last line of content;
// the blank line after each file is the separator between them.
func Write(out io.Writer, files []File) {
	for _, f := range files {
		fmt.Fprintf(out, "==> %s <==\n", f.Path)
		_, _ = out.Write(f.Content)
		if len(f.Content) > 0 && f.Content[len(f.Content)-1] != '\n' {
			fmt.Fprintln(out)
		}
		fmt.Fprintln(out)
	}
}
