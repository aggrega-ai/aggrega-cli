// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package clidocs renders a deterministic, mechanical CLI reference from a
// cobra command tree and splices it into hand-authored documentation pages
// between HTML-comment markers.
//
// The package owns two concerns: rendering (turning a *cobra.Command into a
// stable markdown fragment — synopsis, flags table, subcommand list) and
// splicing (rewriting only the marked region of a page while preserving all
// curated prose outside the markers). A generated region is delimited by:
//
//	<!-- BEGIN GENERATED: <id> -->
//	...generated content...
//	<!-- END GENERATED: <id> -->
//
// Splicing is idempotent: given identical input, a second run produces
// byte-identical output, so a regenerate-and-diff check can detect drift.
// Rendering is deterministic: flags and commands are emitted in sorted
// order and no wall-clock or path-dependent content is written.
package clidocs

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// beginMarker and endMarker are the format strings for the HTML-comment
// delimiters that fence a generated region. The %s is the region id.
const (
	beginMarker = "<!-- BEGIN GENERATED: %s -->"
	endMarker   = "<!-- END GENERATED: %s -->"
)

// Result reports what Generate did to the documentation tree.
type Result struct {
	// Written lists the doc pages whose bytes changed, relative to the
	// docs directory passed to Generate.
	Written []string
	// Unchanged lists the doc pages that were already up to date,
	// relative to the docs directory.
	Unchanged []string
	// SkippedCommands lists top-level command names that had no
	// corresponding page and for which no page was created because the
	// command is hidden.
	SkippedCommands []string
}

// Generate renders the mechanical reference for root's command tree into the
// documentation pages under docsDir. For every visible top-level command it
// targets language-reference/cli/aggrega-<name>.md, creating the page from a
// minimal stub when absent and refreshing the generated region otherwise. It
// also refreshes the command summary in language-reference/07-cli.md and the
// global-flags table in language-reference/cli/global-flags.md. Curated prose
// outside the markers is preserved byte-for-byte.
func Generate(root *cobra.Command, docsDir string) (Result, error) {
	var res Result
	cliDir := filepath.Join(docsDir, "language-reference", "cli")

	cmds := visibleCommands(root)

	for _, cmd := range cmds {
		name := cmd.Name()
		page := filepath.Join(cliDir, "aggrega-"+name+".md")
		id := "cli-" + name
		inner := renderCommand(cmd)

		existing, err := os.ReadFile(page)
		switch {
		case err == nil:
			// refresh in place
		case os.IsNotExist(err):
			existing = []byte(stubPage(cmd))
		default:
			return res, fmt.Errorf("read %s: %w", page, err)
		}

		changed, werr := writeSpliced(page, string(existing), id, inner)
		if werr != nil {
			return res, werr
		}
		rel := filepath.Join("language-reference", "cli", "aggrega-"+name+".md")
		if changed {
			res.Written = append(res.Written, rel)
		} else {
			res.Unchanged = append(res.Unchanged, rel)
		}
	}

	// Command summary in the overview chapter.
	overview := filepath.Join(docsDir, "language-reference", "07-cli.md")
	if body, err := os.ReadFile(overview); err == nil {
		changed, werr := writeSpliced(overview, string(body), "command-list", renderCommandList(cmds))
		if werr != nil {
			return res, werr
		}
		rel := filepath.Join("language-reference", "07-cli.md")
		if changed {
			res.Written = append(res.Written, rel)
		} else {
			res.Unchanged = append(res.Unchanged, rel)
		}
	} else if !os.IsNotExist(err) {
		return res, fmt.Errorf("read %s: %w", overview, err)
	}

	// Global (root persistent) flags table.
	globals := filepath.Join(cliDir, "global-flags.md")
	if body, err := os.ReadFile(globals); err == nil {
		changed, werr := writeSpliced(globals, string(body), "global-flags", renderFlagSet(root.PersistentFlags()))
		if werr != nil {
			return res, werr
		}
		rel := filepath.Join("language-reference", "cli", "global-flags.md")
		if changed {
			res.Written = append(res.Written, rel)
		} else {
			res.Unchanged = append(res.Unchanged, rel)
		}
	} else if !os.IsNotExist(err) {
		return res, fmt.Errorf("read %s: %w", globals, err)
	}

	return res, nil
}

// writeSpliced computes the spliced content and writes it only when it
// differs from what is on disk, so unchanged pages keep their modification
// time. It reports whether the file changed.
func writeSpliced(path, content, id, inner string) (bool, error) {
	next, err := splice(content, id, inner)
	if err != nil {
		return false, fmt.Errorf("splice %s (%s): %w", path, id, err)
	}
	if next == content {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}

// splice replaces the region delimited by the id's markers with inner,
// preserving everything outside the markers verbatim. When the markers are
// absent the block is appended after the existing content. The canonical
// block form is the begin marker, the inner content, and the end marker each
// on their own line, which makes a second call with identical inner a no-op.
func splice(content, id, inner string) (string, error) {
	begin := fmt.Sprintf(beginMarker, id)
	end := fmt.Sprintf(endMarker, id)
	block := begin + "\n" + inner + "\n" + end

	bi := strings.Index(content, begin)
	ei := strings.Index(content, end)

	switch {
	case bi == -1 && ei == -1:
		if strings.TrimSpace(content) == "" {
			return block + "\n", nil
		}
		return strings.TrimRight(content, "\n") + "\n\n" + block + "\n", nil
	case bi == -1 || ei == -1:
		return "", fmt.Errorf("unbalanced markers for id %q", id)
	case ei < bi:
		return "", fmt.Errorf("end marker precedes begin marker for id %q", id)
	default:
		after := ei + len(end)
		return content[:bi] + block + content[after:], nil
	}
}

// visibleCommands returns root's immediate subcommands that should appear in
// the reference, excluding hidden commands and cobra's auto-generated help
// and completion commands, sorted by name for determinism.
func visibleCommands(root *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	for _, c := range root.Commands() {
		if c.Hidden || c.Name() == "help" || c.Name() == "completion" {
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// renderCommand produces the mechanical reference fragment for one command:
// its synopsis, its own flags, and its subcommands. Descriptive prose is left
// to the curated portion of the page.
func renderCommand(cmd *cobra.Command) string {
	var b strings.Builder
	b.WriteString("**Synopsis**\n\n")
	b.WriteString("```\n")
	b.WriteString(cmd.UseLine())
	b.WriteString("\n```")

	if flags := renderFlagSet(cmd.LocalFlags()); flags != "" {
		b.WriteString("\n\n**Flags**\n\n")
		b.WriteString(flags)
	}

	if subs := renderSubcommands(cmd); subs != "" {
		b.WriteString("\n\n**Subcommands**\n\n")
		b.WriteString(subs)
	}

	return b.String()
}

// renderFlagSet renders a flag set as a markdown table sorted by flag name.
// Hidden flags and the auto-added help flag are omitted. It returns the empty
// string when no flag qualifies, so callers can suppress an empty section.
func renderFlagSet(fs *pflag.FlagSet) string {
	var flags []*pflag.Flag
	fs.VisitAll(func(f *pflag.Flag) {
		if f.Hidden || f.Name == "help" {
			return
		}
		flags = append(flags, f)
	})
	if len(flags) == 0 {
		return ""
	}
	sort.Slice(flags, func(i, j int) bool { return flags[i].Name < flags[j].Name })

	var b strings.Builder
	b.WriteString("| Flag | Default | Description |\n")
	b.WriteString("| --- | --- | --- |\n")
	for _, f := range flags {
		name := "`--" + f.Name + "`"
		if f.Shorthand != "" {
			name += ", `-" + f.Shorthand + "`"
		}
		def := f.DefValue
		if def == "" {
			def = "—"
		} else {
			def = "`" + def + "`"
		}
		b.WriteString("| " + name + " | " + def + " | " + escapeCell(f.Usage) + " |\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderSubcommands renders a command's visible subcommands as a table of
// command path and short description, sorted by name. It returns the empty
// string when the command is a leaf.
func renderSubcommands(cmd *cobra.Command) string {
	subs := visibleCommands(cmd)
	if len(subs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("| Command | Description |\n")
	b.WriteString("| --- | --- |\n")
	for _, c := range subs {
		b.WriteString("| `" + c.CommandPath() + "` | " + escapeCell(c.Short) + " |\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderCommandList renders the top-level command summary table used in the
// CLI overview chapter.
func renderCommandList(cmds []*cobra.Command) string {
	var b strings.Builder
	b.WriteString("| Command | Description |\n")
	b.WriteString("| --- | --- |\n")
	for _, c := range cmds {
		b.WriteString("| `" + c.CommandPath() + "` | " + escapeCell(c.Short) + " |\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// stubPage returns the minimal page body used when a command has no curated
// page yet: a title and the command's short description. The generated region
// is appended by the splice step.
func stubPage(cmd *cobra.Command) string {
	short := cmd.Short
	if short == "" {
		short = cmd.CommandPath() + " command."
	}
	return "# " + cmd.CommandPath() + "\n\n" + short + "\n"
}

// escapeCell makes a value safe to place inside a markdown table cell by
// escaping the pipe character and flattening newlines.
func escapeCell(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "|", "\\|")
	return strings.TrimSpace(s)
}
