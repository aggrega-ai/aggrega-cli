// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package clidocs renders a deterministic, mechanical CLI reference from a
// cobra command tree and splices it into hand-authored documentation pages
// between HTML-comment markers.
//
// The package owns one concern: rendering — turning a command descriptor into a
// stable markdown fragment (synopsis, flags table, subcommand list, and a flags
// table for each subcommand that declares flags). Placing that fragment into a
// page belongs to docsplice, which owns the generated-region convention for
// every generator that writes one, so the marker format is not restated here.
//
// It does not read the command tree itself. Every command fact it renders comes
// from the catalog clisurface.Catalog projects, which is the single place the
// tree is read, so the reference cannot describe a different command surface
// than any other consumer of that catalog sees. What this package adds on top is
// presentation policy: which commands a reference shows (hidden commands and
// cobra's generated help and completion commands are omitted) and in what order
// (by name).
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

	"github.com/aggrega-ai/aggrega-cli/internal/clisurface"
	"github.com/aggrega-ai/aggrega-cli/internal/docsplice"
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

	cat := clisurface.Catalog(root)
	if len(cat) == 0 {
		return res, nil
	}
	rootCmd := cat[0]
	cmds := visibleCommands(cat, rootCmd.Path)

	for _, cmd := range cmds {
		name := cmd.Name
		page := filepath.Join(cliDir, "aggrega-"+name+".md")
		id := "cli-" + name
		inner := renderCommand(cmd, cat)

		existing, err := os.ReadFile(page)
		switch {
		case err == nil:
			// refresh in place
		case os.IsNotExist(err):
			existing = []byte(stubPage(cmd))
		default:
			return res, fmt.Errorf("read %s: %w", page, err)
		}

		changed, werr := docsplice.WriteFile(page, string(existing), id, inner)
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
		changed, werr := docsplice.WriteFile(overview, string(body), "command-list", renderCommandList(cmds))
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
		changed, werr := docsplice.WriteFile(globals, string(body), "global-flags", renderFlagSet(rootCmd.PersistentFlags))
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

// visibleCommands returns the catalog entries whose parent is parentPath and
// that should appear in the reference, excluding hidden commands and cobra's
// generated help and completion commands, sorted by name for determinism. This
// is the reference's presentation policy over the unfiltered catalog; the
// catalog itself carries every command.
func visibleCommands(cat []clisurface.Descriptor, parentPath string) []clisurface.Descriptor {
	var out []clisurface.Descriptor
	for _, c := range clisurface.Children(cat, parentPath) {
		if c.Hidden || c.Name == "help" || c.Name == "completion" {
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// renderCommand produces the mechanical reference fragment for one command:
// its synopsis, its own flags, its subcommands, and one flags table per
// subcommand that declares flags. Descriptive prose is left to the curated
// portion of the page. The flags rendered are the command's local ones — the
// flags it inherits are documented once on the global-flags page instead of
// repeated under every command.
//
// A command and its subcommands share one page, so a reader looking up a
// subcommand's flags finds them beside the parent's rather than on a page of
// their own.
func renderCommand(cmd clisurface.Descriptor, cat []clisurface.Descriptor) string {
	var b strings.Builder
	b.WriteString("**Synopsis**\n\n")
	b.WriteString("```\n")
	b.WriteString(cmd.Synopsis)
	b.WriteString("\n```")

	if flags := renderFlagSet(cmd.LocalFlags); flags != "" {
		b.WriteString("\n\n**Flags**\n\n")
		b.WriteString(flags)
	}

	if subs := renderSubcommands(cmd, cat); subs != "" {
		b.WriteString("\n\n**Subcommands**\n\n")
		b.WriteString(subs)
	}

	if subFlags := renderSubcommandFlags(cmd, cat); subFlags != "" {
		b.WriteString("\n\n")
		b.WriteString(subFlags)
	}

	return b.String()
}

// renderFlagSet renders flags as a markdown table sorted by flag name. Hidden
// flags and the auto-added help flag are omitted. It returns the empty string
// when no flag qualifies, so callers can suppress an empty section.
func renderFlagSet(all []clisurface.Flag) string {
	var flags []clisurface.Flag
	for _, f := range all {
		if f.Hidden || f.Name == "help" {
			continue
		}
		flags = append(flags, f)
	}
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
		def := f.Default
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
func renderSubcommands(cmd clisurface.Descriptor, cat []clisurface.Descriptor) string {
	subs := visibleCommands(cat, cmd.Path)
	if len(subs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("| Command | Description |\n")
	b.WriteString("| --- | --- |\n")
	for _, c := range subs {
		b.WriteString("| `" + c.Path + "` | " + escapeCell(c.Short) + " |\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderSubcommandFlags renders one flags table per subcommand of cmd, each
// under a label naming the subcommand's full invocation path. Subcommands are
// visited in the order renderSubcommands lists them, and one declaring no flags
// of its own contributes nothing, so the reference carries no empty table and a
// wholly flagless family renders exactly as it did before — the same
// empty-string contract renderFlagSet has. It returns the empty string when no
// subcommand declares a flag, so callers can suppress the whole section.
//
// The label is bold rather than a markdown heading. Every part of the generated
// region is deliberately heading-free, so regenerating a page can neither add
// nor move a link target and the page's inbound links keep resolving to its
// curated headings alone.
//
// The subcommands covered are cmd's own, matching the depth of the subcommand
// table above them: a reader sees a flags table for each command that table
// lists, and for no command it does not.
func renderSubcommandFlags(cmd clisurface.Descriptor, cat []clisurface.Descriptor) string {
	var blocks []string
	for _, sub := range visibleCommands(cat, cmd.Path) {
		flags := renderFlagSet(sub.LocalFlags)
		if flags == "" {
			continue
		}
		blocks = append(blocks, "**`"+sub.Path+"` flags**\n\n"+flags)
	}
	return strings.Join(blocks, "\n\n")
}

// renderCommandList renders the top-level command summary table used in the
// CLI overview chapter.
func renderCommandList(cmds []clisurface.Descriptor) string {
	var b strings.Builder
	b.WriteString("| Command | Description |\n")
	b.WriteString("| --- | --- |\n")
	for _, c := range cmds {
		b.WriteString("| `" + c.Path + "` | " + escapeCell(c.Short) + " |\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// stubPage returns the minimal page body used when a command has no curated
// page yet: a title and the command's short description. The generated region
// is appended by the splice step.
func stubPage(cmd clisurface.Descriptor) string {
	short := cmd.Short
	if short == "" {
		short = cmd.Path + " command."
	}
	return "# " + cmd.Path + "\n\n" + short + "\n"
}

// escapeCell makes a value safe to place inside a markdown table cell by
// escaping the pipe character and flattening newlines.
func escapeCell(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "|", "\\|")
	return strings.TrimSpace(s)
}
