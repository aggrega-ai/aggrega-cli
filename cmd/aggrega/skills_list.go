// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/skills"
)

// newSkillsListCmd builds the `skills list` command: it reports the skills this
// binary ships and the assistants `skills install` knows, with the folders each
// one reads.
func newSkillsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the skills this binary ships and the assistants it can install them for",
		Long: `List the skills built into this binary, and the assistants "aggrega skills
install" can write them for, with the project folder and the per-user folder
each assistant reads skills from. It writes nothing.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return writeSkillsList(cmd.OutOrStdout())
		},
	}
}

// writeSkillsList renders the listing: the shipped skills, then the assistants,
// each in the fixed order the skills package declares them.
func writeSkillsList(out io.Writer) error {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SKILL\tDESCRIPTION")
	for _, name := range skills.Names() {
		meta, err := skills.Describe(name)
		if err != nil {
			return fmt.Errorf("aggrega skills list: %w", err)
		}
		fmt.Fprintf(w, "%s\t%s\n", name, meta.Description)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Fprintln(out)
	w = tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "AGENT\tASSISTANT\tPROJECT FOLDER\tPER-USER FOLDER")
	for _, a := range skills.Agents() {
		fmt.Fprintf(w, "%s\t%s\t%s\t~/%s\n", a.ID, a.Name, a.ProjectDir, a.UserDir)
	}
	return w.Flush()
}
