// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/exampleops"
	"github.com/aggrega-ai/aggrega-cli/internal/scaffoldops"
	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
)

// newScaffoldCmd builds the scaffold command, which prints a starter document
// for an entity kind derived from that kind's embedded JSON Schema, or — with
// --example — writes one of the documentation's runnable examples into a new
// directory, or lists them.
//
// The command decodes its arguments and writes the result; the document itself
// comes from the shared drafting layer every front end drives, so the bytes a
// person gets at a terminal are the bytes an assistant gets from the tool
// protocol and an editor gets from the daemon. The examples likewise come from
// the shared example layer, read from the signed documentation bundle.
//
// Drafting writes no file. The document goes to the command's own output stream,
// so a redirect decides where it lands and an existing file is only ever replaced
// by a shell the caller drove themselves. Writing an example writes the new
// directory it names and nothing else, and never into a directory that holds
// anything.
func newScaffoldCmd() *cobra.Command {
	var example, list bool
	cmd := &cobra.Command{
		Use:   "scaffold <kind> [name] | --example <name> [dir] | --example --list",
		Short: "Draft a starter document for an entity kind, or write a runnable example into a new directory",
		Long: "Draft a starter document for an entity kind from its schema, or write one of the " +
			"documentation's runnable examples into a new directory.\n\n" +
			"Drafting a starter document\n\n" +
			"The kind is the lowercase-hyphenated schema token — for example component, " +
			"aggregate, release, recipe, manifest, or feature. Run aggrega get-schema --list " +
			"to print the whole set; a token no schema answers to is refused with that set " +
			"in the message, so a typo is corrected without a second lookup.\n\n" +
			"The document is derived from that kind's own contract and from nothing else. It " +
			"carries exactly the fields that kind's schema requires, with the fixed apiVersion " +
			"and kind filled in and a placeholder everywhere a value is needed. It is not a " +
			"worked example: there is no namespace, no category, no label set and no commented " +
			"suggestion of what people usually write, because a catalog decides those for " +
			"itself and a starter document that guessed at them would teach a requirement that " +
			"does not exist.\n\n" +
			"The optional name seeds metadata.name for a kind whose schema requires one. Every " +
			"other required value is left as a placeholder to fill in.\n\n" +
			"Nothing is written to disk. The document goes to standard output, so a redirect " +
			"decides where it lands:\n\n" +
			"  aggrega scaffold component my-service > my-service.yaml\n\n" +
			"The same starter document is served to an assistant by the scaffold tool that " +
			"aggrega mcp exposes, which returns these bytes alongside a machine-readable " +
			"payload; this command is the terminal route to the same drafting.\n\n" +
			"Writing a runnable example\n\n" +
			"With --example, the argument is the name of one of the runnable examples the " +
			"documentation publishes, and the command writes that example's files into a new " +
			"directory — the second argument, or a directory named after the example in the " +
			"current directory when there is none — then prints the commands the example's own " +
			"page gives for building it:\n\n" +
			"  aggrega scaffold --example redis-cache\n\n" +
			"The directory must not exist yet, or must be empty; a directory holding anything " +
			"is refused and left as it is. With --list, the command prints every example there " +
			"is, each with its level and the first sentence of its page, and writes nothing:\n\n" +
			"  aggrega scaffold --example --list\n\n" +
			"The examples are read from the documentation bundle published for this version of " +
			"aggrega on the default registry — or, where none is published for it, the newest " +
			"one published for an earlier version. It is kept in the cache every project on this " +
			"machine shares, so a second example needs no second download. It is fetched through the same network settings as every other " +
			"registry read, and verified against this workspace's signature policy before it is " +
			"kept or read; a bundle that does not verify is refused, never used. Offline, an " +
			"example is written only from a copy already cached on this machine.\n\n" +
			"An example's files are written only as files and directories inside the directory " +
			"named: a bundle carrying an entry that would land outside it, or a link, is refused " +
			"whole and nothing is written.\n\n" +
			"The scaffold tool that aggrega mcp exposes, and the editor's language server that " +
			"aggrega serve runs, write and list the examples on the same terms, with the directory " +
			"named relative to the workspace root, and answer with the files written and these same " +
			"commands.",
		Args: func(cmd *cobra.Command, args []string) error {
			switch {
			case list && !example:
				return errors.New("--list prints the documentation's runnable examples and is given together " +
					"with --example: aggrega scaffold --example --list")
			case example && list:
				if len(args) != 0 {
					return fmt.Errorf("--example --list takes no argument, and received %d", len(args))
				}
				return nil
			case example:
				if len(args) < 1 || len(args) > 2 {
					return fmt.Errorf("--example takes the example's name and, optionally, the directory to "+
						"write it into, and received %d arguments; aggrega scaffold --example --list prints "+
						"the names", len(args))
				}
				return nil
			}
			return cobra.RangeArgs(1, 2)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if example {
				ctx, out, notes := cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr()
				if list {
					return listExamples(ctx, out, notes, provenanceCompilerVersion())
				}
				var dir string
				if len(args) > 1 {
					dir = args[1]
				}
				return writeExample(ctx, out, notes, provenanceCompilerVersion(), args[0], dir)
			}
			var name string
			if len(args) > 1 {
				name = args[1]
			}
			return emitSkeleton(cmd.OutOrStdout(), args[0], name)
		},
	}
	cmd.Flags().BoolVar(&example, "example", false,
		"write the runnable example the argument names into a new directory, read from the signed documentation bundle")
	cmd.Flags().BoolVar(&list, "list", false,
		"with --example, print every runnable example the documentation publishes, and write nothing")
	return cmd
}

// emitSkeleton writes the starter document for kind, seeding metadata.name with
// name when one was given.
//
// A kind no schema answers to surfaces as the drafting layer's own refusal,
// which names every valid kind, so the correction a caller reads here is the
// correction every other surface gives for the same mistake.
func emitSkeleton(out io.Writer, kind, name string) error {
	skeleton, err := scaffoldops.Skeleton(kind, name)
	if err != nil {
		return err
	}
	_, err = io.WriteString(out, skeleton)
	return err
}

// openExamples opens the documentation bundle the examples are read from for the
// workspace this invocation resolved, reporting acquisition warnings on notes and,
// where the bundle read is not the one published for version, which one it is.
func openExamples(ctx context.Context, notes io.Writer, version string) (*exampleops.Examples, error) {
	ws, err := workspaceFromContext(ctx)
	if err != nil {
		return nil, err
	}
	warn := func(notice string) { fmt.Fprintf(notes, "warning: %s\n", notice) }
	examples, err := exampleops.Open(ctx, ws.Config(), version, warn)
	if err != nil {
		return nil, err
	}
	if note := examples.Note(); note != "" {
		fmt.Fprintf(notes, "note: %s\n", note)
	}
	return examples, nil
}

// listExamples prints every runnable example the documentation publishes for
// version, one per line with its level and the first sentence of its page.
func listExamples(ctx context.Context, out, notes io.Writer, version string) error {
	examples, err := openExamples(ctx, notes, version)
	if err != nil {
		return err
	}
	list, err := examples.List()
	if err != nil {
		return fmt.Errorf("the documentation bundle %s@%s carries no readable list of examples: %w",
			examples.Ref, examples.Version, err)
	}
	_, err = io.WriteString(out, exampleops.ListReport(list))
	return err
}

// writeExample writes the example name into dir — a directory named after it in
// the working directory, where dir is empty — and prints where it went and the
// commands its page gives for building it.
func writeExample(ctx context.Context, out, notes io.Writer, version, name, dir string) error {
	examples, err := openExamples(ctx, notes, version)
	if err != nil {
		return err
	}
	written, err := examples.Write(name, dir)
	if err != nil {
		if errors.Is(err, docscache.ErrPageNotFound()) {
			return fmt.Errorf("%w\n\naggrega scaffold --example --list prints the examples there are", err)
		}
		return err
	}
	_, err = io.WriteString(out, written.Report())
	return err
}
