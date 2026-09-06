// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/scaffoldops"
)

// newScaffoldCmd builds the scaffold command, which prints a starter document
// for an entity kind derived from that kind's embedded JSON Schema.
//
// The command decodes its arguments and writes the result; the document itself
// comes from the shared drafting layer every front end drives, so the bytes a
// person gets at a terminal are the bytes an assistant gets from the tool
// protocol and an editor gets from the daemon.
//
// It writes no file. The document goes to the command's own output stream, so a
// redirect decides where it lands and an existing file is only ever replaced by
// a shell the caller drove themselves.
func newScaffoldCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "scaffold <kind> [name]",
		Short: "Draft a starter document for an entity kind from its schema",
		Long: "Draft a starter document for an entity kind from its schema.\n\n" +
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
			"payload; this command is the terminal route to the same drafting.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			var name string
			if len(args) > 1 {
				name = args[1]
			}
			return emitSkeleton(cmd.OutOrStdout(), args[0], name)
		},
	}
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
