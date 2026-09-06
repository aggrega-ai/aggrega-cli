// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"errors"
	"fmt"
	"io"

	schemas "github.com/aggrega-ai/aggrega-schemas"
	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/scaffoldops"
)

// newGetSchemaCmd builds the get-schema command, which prints the canonical
// JSON Schema for an embedded entity or contract kind. The schema bytes come
// straight from the binary's embedded copy, so the output is byte-identical
// to the distributed schema file and stable across runs — suitable for
// piping to a validator or wiring into an editor's schema association.
func newGetSchemaCmd() *cobra.Command {
	var listFlag bool
	c := &cobra.Command{
		Use:   "get-schema [kind]",
		Short: "Print the JSON Schema for an entity or contract kind",
		Long: "Print the canonical JSON Schema for an embedded kind.\n\n" +
			"The kind is the lowercase-hyphenated schema name — for example " +
			"component, aggregate, ir, or template-context. Entity kinds, " +
			"the intermediate-representation and wire-format contracts, and the " +
			"wasm-* plugin socket contracts are all addressable. The emitted " +
			"bytes are identical to the distributed schema file.\n\n" +
			"Use --list to enumerate every available kind, one per line.",
		Args: func(cmd *cobra.Command, args []string) error {
			if listFlag {
				return cobra.NoArgs(cmd, args)
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if listFlag {
				return listSchemaKinds(out)
			}
			return emitSchema(out, args[0])
		},
	}
	c.Flags().BoolVar(&listFlag, "list", false, "List every schema kind, one per line.")
	return c
}

// listSchemaKinds writes each available kind token on its own line, in the
// deterministic order Kinds reports.
func listSchemaKinds(out io.Writer) error {
	for _, kind := range schemas.Kinds() {
		if _, err := fmt.Fprintln(out, kind); err != nil {
			return err
		}
	}
	return nil
}

// emitSchema writes the schema for kind verbatim. An unknown kind is an
// actionable error that names every valid kind, so the caller can correct a
// typo without a second lookup.
//
// The corrective text comes from the shared drafting layer, which is where every
// surface resolving a kind token against this set phrases its rejection. Spelling
// it here as well would leave one rejection with two wordings, and a caller
// meeting them on two surfaces reading them as two different faults.
func emitSchema(out io.Writer, kind string) error {
	schema, ok := schemas.SchemaByKind(kind)
	if !ok {
		return errors.New(scaffoldops.UnknownKindGuidance(kind))
	}
	_, err := io.WriteString(out, schema)
	return err
}
