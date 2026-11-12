// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package exampleops

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/docsbundle"
	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// ExamplesDir is the directory of the documentation bundle the runnable examples
// sit in, one directory each, named after the example.
const ExamplesDir = "language-reference/examples"

// IndexPage is the page of the documentation bundle listing every example, each
// with its level and the first sentence of its own page. It is generated from the
// examples' own pages, so the list read from it is the one the documentation
// publishes.
const IndexPage = ExamplesDir + "/by-product-and-capability.md"

// Example is one runnable example, as the documentation's list of examples
// describes it.
type Example struct {
	// Name is the example's directory name, which is what a reader passes to have
	// it written out.
	Name string
	// Level is the level the example's page declares for it.
	Level string
	// Summary is the first sentence of the example's page.
	Summary string
}

// Examples is the documentation bundle the examples are read from, opened for a
// reader whose signature check admitted it.
type Examples struct {
	// Ref is the bundle's reference.
	Ref string
	// Version is the version of the bundle read: the one asked for where it is
	// published, otherwise the newest published at or below it.
	Version string
	// Requested is the version asked for.
	Requested string

	bundle *docscache.Bundle
}

// Open acquires the documentation bundle for version on the default registry of
// the workspace configured as cfg, and opens it under that workspace's signature
// check.
//
// The acquisition is the one every documentation surface shares: a copy already
// in the machine-wide cache that the check admits is read without reaching a
// registry, and otherwise the bundle is fetched through the workspace's egress
// gate and verified before it is kept. Where the registry publishes no bundle at
// version, the newest at or below it is read instead, and Version says which —
// the one choice every documentation surface shares
// (docsbundle.EnsureCachedAtOrBelow). Warnings the acquisition raises go to warn.
//
// Every failure names the bundle and the version it looked for, and carries what
// stood in the way — an offline workspace's refusal, a signature the workspace's
// check does not accept, a registry that publishes nothing at or below version —
// unchanged.
func Open(ctx context.Context, cfg workspaceconfig.Config, version string, warn func(string)) (*Examples, error) {
	ref := docsbundle.Ref(cfg.DefaultRegistryLocation().Prefix())
	cacheRoot, err := docsbundle.CacheRoot(ctx)
	if err != nil {
		return nil, unavailable(ref, version, err)
	}
	held, _, err := docsbundle.EnsureCachedAtOrBelow(ctx, cfg, cacheRoot, ref, version, nil, warn)
	if err != nil {
		return nil, unavailable(ref, version, err)
	}
	// The acquisition above already reported what the check had to say about the
	// copy it left in place, so the read asks it again without repeating that.
	bundle, err := docscache.Open(cacheRoot, ref, held, docsbundle.Admission(ctx, cfg, ref, nil))
	if err != nil {
		return nil, unavailable(ref, held, err)
	}
	return &Examples{Ref: ref, Version: held, Requested: version, bundle: bundle}, nil
}

// unavailable states that the examples cannot be read because the documentation
// bundle for (ref, version) could not be, and why.
//
// Where no bundle is published for version and the earlier version's one could
// not be acquired either (docsbundle.NoBundleForVersionError), the answer is that
// one sentence, which already names the bundle and both versions, and what it
// means for the examples.
func unavailable(ref, version string, cause error) error {
	var none *docsbundle.NoBundleForVersionError
	if errors.As(cause, &none) {
		return fmt.Errorf("%w, so the examples cannot be read", none)
	}
	return fmt.Errorf("the examples are read from the documentation bundle %s@%s, which is not available: %w",
		ref, version, cause)
}

// List returns every example the bundle's list of examples names, each once, in
// the order that list gives them.
func (e *Examples) List() ([]Example, error) {
	page, _, err := e.bundle.Page(IndexPage)
	if err != nil {
		return nil, err
	}
	return ParseIndex(page), nil
}

// indexEntry is one entry of the list of examples: a link to the example's page,
// named after the example, its level as a code span, and the first sentence of its
// page after a dash.
var indexEntry = regexp.MustCompile("^- \\[([^\\]]+)\\]\\(([^)]+)/README\\.md\\) \\(`([^`]+)`\\) — (.*)$")

// ParseIndex returns the examples the list-of-examples page names, each once, in
// the order the page first names them.
//
// The page opens with every example in the order the documentation teaches them,
// then lists an example again under every product it deploys and every capability
// it shows, so one example appears on several lines; they carry the same level and
// sentence, and the first is kept, at the place it first appears. The order is the
// page's own and is not rearranged, so the list reads in the order the
// documentation publishes. A line that is not an entry, and an entry whose link
// does not lead to the page of the example it names, is not an example.
func ParseIndex(page []byte) []Example {
	var out []Example
	seen := map[string]bool{}
	for _, line := range strings.Split(string(page), "\n") {
		m := indexEntry.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil || m[1] != m[2] || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		out = append(out, Example{Name: m[1], Level: m[3], Summary: m[4]})
	}
	return out
}

// Written is one example written out.
type Written struct {
	// Name is the example written.
	Name string
	// Dir is the directory it was written into, as it was named.
	Dir string
	// Files are the files written, relative to Dir and slash-separated, in the
	// order the bundle carries them.
	Files []string
	// Next are the lines of the shell block the example's page gives under its
	// Build heading, as the page writes them; empty where the page gives none.
	Next []string
}

// Write writes the example name into the directory dir — a directory named
// after the example, relative to the working directory, where dir is empty — and
// returns what it wrote and the commands the example's page gives for building it.
//
// dir must not exist yet or must be an empty directory, and a member of the
// example that is a link, or whose name would land outside dir, refuses the whole
// example; either way nothing is written. A name that is not one directory name,
// and one the bundle carries no example under, is refused as a page the bundle
// does not carry (docscache.ErrPageNotFound).
func (e *Examples) Write(name, dir string) (Written, error) {
	if err := CheckName(name); err != nil {
		return Written{}, err
	}
	if dir == "" {
		dir = name
	}
	files, err := e.bundle.ExtractDirectory(ExamplesDir+"/"+name, dir)
	if err != nil {
		if errors.Is(err, docscache.ErrPageNotFound()) {
			return Written{}, fmt.Errorf("the documentation bundle %s@%s carries no example named %q: %w",
				e.Ref, e.Version, name, err)
		}
		return Written{}, err
	}
	var next []string
	if readme, _, err := e.bundle.Page(ExamplesDir + "/" + name + "/README.md"); err == nil {
		next = NextCommands(readme)
	}
	return Written{Name: name, Dir: dir, Files: files, Next: next}, nil
}

// NextCommands returns the lines of the first fenced block marked sh, bash or
// shell beneath the page's level-two Build heading, before the next heading of
// level one or two, as the page writes them, without the blank lines that open or
// close the block. It returns none where the page has no such block.
//
// It reads what the page already says and decides nothing about it: the lines are
// the example's own instructions, comments included.
func NextCommands(page []byte) []string {
	inBuild := false
	fence := ""
	capturing := false
	var block []string
	for _, line := range strings.Split(strings.ReplaceAll(string(page), "\r\n", "\n"), "\n") {
		if fence != "" {
			if closesFence(line, fence) {
				if capturing {
					return trimBlank(block)
				}
				fence = ""
				continue
			}
			if capturing {
				block = append(block, line)
			}
			continue
		}
		if opened, info, ok := opensFence(line); ok {
			fence = opened
			capturing = inBuild && isShellBlock(info)
			continue
		}
		if level, text := heading(line); level > 0 && level <= 2 {
			inBuild = level == 2 && text == "Build"
		}
	}
	if capturing {
		return trimBlank(block)
	}
	return nil
}

// atxHeading is a Markdown heading: up to three spaces, one to six hashes, and
// its text, with any closing hashes dropped.
var atxHeading = regexp.MustCompile(`^ {0,3}(#{1,6})(?:[ \t]+(.*?))?(?:[ \t]+#+)?[ \t]*$`)

// heading returns the level and text of a heading line, and level 0 for any
// other line.
func heading(line string) (int, string) {
	m := atxHeading.FindStringSubmatch(line)
	if m == nil {
		return 0, ""
	}
	return len(m[1]), strings.TrimSpace(m[2])
}

// opensFence reports whether line opens a fenced block, the run of backticks or
// tildes that opened it, and the first word of its info string.
func opensFence(line string) (fence, info string, ok bool) {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 {
		return "", "", false
	}
	for _, mark := range []string{"`", "~"} {
		run := len(trimmed) - len(strings.TrimLeft(trimmed, mark))
		if run < 3 {
			continue
		}
		rest := strings.TrimSpace(trimmed[run:])
		if mark == "`" && strings.Contains(rest, "`") {
			return "", "", false
		}
		if fields := strings.Fields(rest); len(fields) > 0 {
			info = fields[0]
		}
		return trimmed[:run], info, true
	}
	return "", "", false
}

// closesFence reports whether line closes a block fence opened.
func closesFence(line, fence string) bool {
	trimmed := strings.TrimSpace(line)
	mark := fence[:1]
	return len(trimmed) >= len(fence) && strings.Trim(trimmed, mark) == ""
}

// isShellBlock reports whether a fenced block's info word marks shell commands.
func isShellBlock(info string) bool {
	switch info {
	case "sh", "bash", "shell":
		return true
	}
	return false
}

// trimBlank returns lines without the blank lines that open or close it.
func trimBlank(lines []string) []string {
	start, end := 0, len(lines)
	for start < end && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	if start == end {
		return nil
	}
	return lines[start:end]
}
