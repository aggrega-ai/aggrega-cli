// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/skills"
)

// attachSkillsCommands builds the `skills` command family under parent: the
// grouping command, then each subcommand named by the operation it carries.
//
// The group is attached before its children so that each child's invocation
// path, which the registrar checks against the operation table, is the one a
// caller types.
func attachSkillsCommands(parent *cobra.Command) {
	c := &cobra.Command{
		Use:   "skills",
		Short: "Install the Aggrega skill into an AI assistant",
		Long: `Install the skill that teaches an AI assistant to install and use Aggrega.

A skill is a folder holding a SKILL.md file in the open Agent Skills format,
which many assistants read from a skill folder of their own. The Aggrega skill
tells an assistant how to install Aggrega, how to connect it to the aggrega MCP
server, how to start a workspace, and how to run the authoring loop — scaffold,
validate, build. It carries no copy of any schema: it sends the assistant to
this binary for every structural question and to the published documentation
for every conceptual one.

The skill is built into this binary, so the copy it installs matches the
version you have.`,
		Example: `  # Install the skill for Claude Code in this project
  aggrega skills install --agent claude-code

  # Install it for Codex and Cursor in every project of yours
  aggrega skills install --agent codex --agent cursor --global`,
	}
	mustAttachGroup(parent, c)
	mustAttach(c, newSkillsInstallCmd(), "skills-install")
	mustAttach(c, newSkillsListCmd(), "skills-list")
}

// skillToInstall is the skill `aggrega skills install` writes. The binary ships
// one, so the command takes no argument naming it.
const skillToInstall = "aggrega"

// newSkillsInstallCmd builds the `skills install` command: it writes the skill
// built into this binary into each named assistant's skill folder.
func newSkillsInstallCmd() *cobra.Command {
	var agentIDs []string
	var global bool

	c := &cobra.Command{
		Use:   "install",
		Short: "Write the Aggrega skill into an AI assistant's skill folder",
		Long: `Write the Aggrega skill into the skill folder of each assistant named with
--agent, so the assistant knows how to install and use Aggrega.

By default the skill goes into the assistant's project skill folder inside the
directory you run the command in, which is where a project's own skills are
committed. With --global it goes into the assistant's per-user skill folder
in your home directory instead, where the assistant reads it in every project.

--agent may be given more than once. Where several of the named assistants read
the same folder, the skill is written there once.

The skill is written as a folder named aggrega holding SKILL.md, and every file
written carries a comment line saying where it comes from. Running the command
again replaces a copy carrying that line, which is how a newer aggrega brings
the skill up to its own version. A file at the target that does not carry it
was not written by this command, so the command refuses, names the file and
writes nothing; move it aside or remove it, then run the command again. Every
target is checked before anything is written, so a refusal leaves every folder
as it was.

The assistants it installs for, and the folders each one reads:

` + agentTable() + `
An assistant that is not listed is refused by name. It can still use Aggrega
through its MCP server: connect it to "aggrega mcp" as
` + skills.SetupPage + ` describes.`,
		Example: `  # This project, for Claude Code
  aggrega skills install --agent claude-code

  # Every project, for Gemini CLI and Cursor
  aggrega skills install --agent gemini-cli --agent cursor --global`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			base, err := skillsInstallBase(cmd, global)
			if err != nil {
				return fmt.Errorf("aggrega skills install: %w", err)
			}
			installed, err := skills.Install(skills.InstallRequest{
				Skill:  skillToInstall,
				Agents: agentIDs,
				Base:   base,
				Global: global,
			})
			if err != nil {
				return fmt.Errorf("aggrega skills install: %w", err)
			}
			writeSkillsInstalled(cmd.OutOrStdout(), installed)
			return nil
		},
	}
	c.Flags().StringArrayVar(&agentIDs, "agent", nil,
		"Assistant to install the skill for, by identifier (for example claude-code, codex, cursor or gemini-cli). Repeatable; the help lists every identifier and the folder each one writes.")
	c.Flags().BoolVar(&global, "global", false,
		"Install into each assistant's per-user skill folder in your home directory, which it reads in every project, instead of the project skill folder in the directory you run the command in.")
	_ = c.MarkFlagRequired("agent")
	return c
}

// skillsInstallBase returns the folder the assistants' skill folders are taken
// relative to: the home directory under --global, read through the environment
// the invocation runs against, and the working directory otherwise.
func skillsInstallBase(cmd *cobra.Command, global bool) (string, error) {
	if !global {
		return os.Getwd()
	}
	platform, lookup := machineFromContext(cmd.Context())
	if lookup == nil {
		lookup = os.Getenv
	}
	return skills.UserHome(platform, lookup)
}

// writeSkillsInstalled reports each folder an installation wrote, one line per
// folder, in the order the assistants were named.
func writeSkillsInstalled(out io.Writer, installed []skills.Installed) {
	for _, in := range installed {
		verb := "Installed"
		if in.Replaced {
			verb = "Replaced"
		}
		fmt.Fprintf(out, "%s the %s skill for %s at %s\n", verb, skillToInstall, in.AgentNames(), in.Dir)
	}
}

// agentTable renders the assistants the install command knows as an indented
// table for its help, from the one table the installation reads.
func agentTable() string {
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  --agent\tassistant\tproject folder\tper-user folder")
	for _, a := range skills.Agents() {
		fmt.Fprintf(w, "  %s\t%s\t%s\t~/%s\n", a.ID, a.Name, a.ProjectDir, a.UserDir)
	}
	_ = w.Flush()
	return b.String()
}
