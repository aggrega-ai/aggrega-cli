// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package skills

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/machinedirs"
)

// Agent is one AI assistant this package knows where to install a skill for.
type Agent struct {
	// ID is the identifier a caller names the assistant by. It is the
	// identifier the open Agent Skills command-line tool uses for the same
	// assistant, so one name works with either tool.
	ID string

	// Name is the assistant's own name, for messages and listings.
	Name string

	// ProjectDir is the folder, relative to a project's root, that the
	// assistant reads a project's skills from, slash-separated.
	ProjectDir string

	// UserDir is the folder, relative to the user's home directory, that the
	// assistant reads the user's own skills from in every project,
	// slash-separated.
	UserDir string

	// ConfigDir is the folder, relative to the user's home directory and
	// slash-separated, that the assistant keeps its own configuration in. Its
	// presence is one of the two signs [Detect] reads the assistant as installed
	// by. It is empty where no such folder belongs to this assistant alone.
	ConfigDir string

	// Command is the name of the program the assistant installs on PATH. Its
	// presence there is the other sign [Detect] reads. It is empty where the
	// assistant installs no command of its own.
	Command string
}

// agents are the assistants this package installs skills for, ordered by ID.
//
// Each skill folder is the one the assistant's own documentation names. Where
// an assistant documents a folder of its own beside the shared .agents/skills,
// the row takes the assistant's own, so a skill installed for one assistant is
// not also picked up by every other assistant reading the shared folder; a row
// takes the shared folder where it is the only one the assistant documents for
// that scope, or the one it recommends. An assistant whose documentation names
// no skill folder has no row: its users connect it to the aggrega MCP server
// instead, which every listed assistant can reach as well.
//
// The two detection signs follow one rule: a sign is recorded only where it
// belongs to that assistant alone, so finding it cannot be read as a different
// assistant being installed. A skill folder several assistants share is
// therefore never a sign — .agents/skills is read by Codex, Goose and Zed alike —
// and neither is .gemini, which both Gemini CLI and Antigravity keep files in:
// Gemini CLI is found by its command alone, and each Antigravity product by a
// folder beneath .gemini that only it writes.
var agents = []Agent{
	{ID: "amp", Name: "Amp", ProjectDir: ".agents/skills", UserDir: ".config/amp/skills",
		ConfigDir: ".config/amp", Command: "amp"},
	{ID: "antigravity", Name: "Antigravity", ProjectDir: ".agents/skills", UserDir: ".gemini/config/skills",
		ConfigDir: ".gemini/antigravity", Command: "antigravity"},
	{ID: "antigravity-cli", Name: "Antigravity CLI", ProjectDir: ".agents/skills", UserDir: ".gemini/antigravity-cli/skills",
		ConfigDir: ".gemini/antigravity-cli"},
	{ID: "claude-code", Name: "Claude Code", ProjectDir: ".claude/skills", UserDir: ".claude/skills",
		ConfigDir: ".claude", Command: "claude"},
	{ID: "cline", Name: "Cline", ProjectDir: ".cline/skills", UserDir: ".cline/skills",
		ConfigDir: ".cline", Command: "cline"},
	{ID: "codex", Name: "Codex", ProjectDir: ".agents/skills", UserDir: ".agents/skills",
		ConfigDir: ".codex", Command: "codex"},
	{ID: "cursor", Name: "Cursor", ProjectDir: ".cursor/skills", UserDir: ".cursor/skills",
		ConfigDir: ".cursor", Command: "cursor"},
	{ID: "gemini-cli", Name: "Gemini CLI", ProjectDir: ".gemini/skills", UserDir: ".gemini/skills",
		Command: "gemini"},
	{ID: "github-copilot", Name: "GitHub Copilot", ProjectDir: ".github/skills", UserDir: ".copilot/skills",
		ConfigDir: ".copilot", Command: "copilot"},
	{ID: "goose", Name: "Goose", ProjectDir: ".agents/skills", UserDir: ".agents/skills",
		ConfigDir: ".config/goose", Command: "goose"},
	{ID: "opencode", Name: "OpenCode", ProjectDir: ".opencode/skills", UserDir: ".config/opencode/skills",
		ConfigDir: ".config/opencode", Command: "opencode"},
	{ID: "windsurf", Name: "Windsurf", ProjectDir: ".devin/skills", UserDir: ".codeium/windsurf/skills",
		ConfigDir: ".codeium/windsurf", Command: "windsurf"},
	{ID: "zed", Name: "Zed", ProjectDir: ".agents/skills", UserDir: ".agents/skills",
		ConfigDir: ".config/zed", Command: "zed"},
}

// Agents returns the assistants this package installs skills for, ordered by ID.
func Agents() []Agent {
	return append([]Agent(nil), agents...)
}

// LookupAgent returns the assistant with the given identifier.
func LookupAgent(id string) (Agent, bool) {
	for _, a := range agents {
		if a.ID == id {
			return a, true
		}
	}
	return Agent{}, false
}

// AgentIDs returns every known assistant's identifier, ordered.
func AgentIDs() []string {
	out := make([]string, 0, len(agents))
	for _, a := range agents {
		out = append(out, a.ID)
	}
	return out
}

// UserHome returns the home directory on p, read through lookup: the
// USERPROFILE variable on Windows and HOME everywhere else, which is the rule
// the Go standard library applies to the running process, stated over a
// platform and an environment the caller supplies.
//
// It refuses rather than answering with an empty or relative path, so a write
// meant for the home directory never lands in the folder the process runs in.
func UserHome(p machinedirs.Platform, lookup func(string) string) (string, error) {
	variable := "HOME"
	if p == machinedirs.Windows {
		variable = "USERPROFILE"
	}
	home := lookup(variable)
	if home == "" {
		return "", errors.New("the home directory is not known: " + variable + " is not set")
	}
	// Whether a path is absolute is answered by this host's rules, so it is asked
	// only of a platform that is this host.
	if p == machinedirs.Host() && !filepath.IsAbs(home) {
		return "", errors.New("the home directory is not known: " + variable + " is not an absolute path")
	}
	return home, nil
}

// fromSlash joins a slash-separated relative folder onto base in the platform's
// own separator.
func fromSlash(base, rel string) string {
	return filepath.Join(append([]string{base}, strings.Split(rel, "/")...)...)
}
