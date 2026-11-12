// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package skills

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/haltref"
)

// setupPagePath is the page that says how to install aggrega and connect an
// assistant to its MCP server, as a path in the documentation tree.
const setupPagePath = "install.md"

// SetupPage returns the address of the published page that says how to install
// aggrega and connect an assistant to its MCP server, which is the route for an
// assistant no skill folder is known for. The address is composed where every
// published page's is, from setupPagePath.
func SetupPage() string { return haltref.PublishedURL(setupPagePath) }

// InstallRequest is what one installation writes.
type InstallRequest struct {
	// Skill names the skill to install.
	Skill string

	// Agents names the assistants to install it for, by identifier, in the
	// order the caller gave them. A name given twice is installed once.
	Agents []string

	// Base is the folder the assistants' skill folders are taken relative to:
	// a project's root, or the user's home directory when Global is set.
	Base string

	// Global selects each assistant's per-user skill folder, beneath the home
	// directory Base names, in place of its project skill folder.
	Global bool

	// Guard, when set, is asked about every path the installation would write —
	// each skill folder and each file in it — before anything is written. An error
	// it returns refuses the installation with every folder left as it was, so a
	// caller that holds its writes to a tree can hold this one to it too.
	Guard func(path string) error
}

// Installed reports one skill folder an installation wrote.
type Installed struct {
	// Agents are the assistants that read the folder. Several assistants read
	// the same shared folder, and the skill is written there once for all of
	// them.
	Agents []Agent

	// Dir is the skill's folder.
	Dir string

	// Replaced reports whether a copy carrying the banner was already there
	// and has been replaced.
	Replaced bool
}

// AgentNames renders the assistants that read the folder for a message:
// "Codex", "Codex and Zed", "Codex, Goose and Zed".
func (in Installed) AgentNames() string {
	names := make([]string, 0, len(in.Agents))
	for _, a := range in.Agents {
		names = append(names, a.Name)
	}
	if len(names) <= 1 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// Install writes the requested skill into each named assistant's skill folder.
//
// Every target is checked before anything is written, so a refusal leaves every
// folder as it was. It refuses an assistant it knows no skill folder for, naming
// the identifier, the assistants it does know and the MCP server as the route
// that works for any assistant; and it refuses a file at a target that does not
// carry the banner, naming its path, because that file was not written by an
// installation and replacing it would lose whatever it holds. A copy carrying
// the banner is replaced, which is how a later binary refreshes the skill to its
// own version. Files in a skill folder that the skill does not write are left
// alone.
//
// The results are in the order the assistants were named, one per distinct
// folder.
func Install(req InstallRequest) ([]Installed, error) {
	files, err := Files(req.Skill)
	if err != nil {
		return nil, err
	}
	if len(req.Agents) == 0 {
		return nil, errors.New("name at least one assistant to install the skill for")
	}
	if req.Base == "" {
		return nil, errors.New("no folder to install the skill into was given")
	}

	var targets []*Installed
	byDir := map[string]*Installed{}
	seen := map[string]bool{}
	for _, id := range req.Agents {
		if seen[id] {
			continue
		}
		seen[id] = true
		agent, ok := LookupAgent(id)
		if !ok {
			return nil, unknownAgentError(id)
		}
		rel := agent.ProjectDir
		if req.Global {
			rel = agent.UserDir
		}
		dir := filepath.Join(fromSlash(req.Base, rel), req.Skill)
		if t, ok := byDir[dir]; ok {
			t.Agents = append(t.Agents, agent)
			continue
		}
		t := &Installed{Agents: []Agent{agent}, Dir: dir}
		byDir[dir] = t
		targets = append(targets, t)
	}

	for _, t := range targets {
		if req.Guard != nil {
			if err := guardTarget(req.Guard, t.Dir, files); err != nil {
				return nil, err
			}
		}
		replaced, err := checkTarget(t.Dir, files)
		if err != nil {
			return nil, err
		}
		t.Replaced = replaced
	}

	out := make([]Installed, 0, len(targets))
	for _, t := range targets {
		if err := writeFiles(t.Dir, files); err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, nil
}

// unknownAgentError is the refusal for an assistant no skill folder is known
// for.
func unknownAgentError(id string) error {
	return fmt.Errorf("no skill folder is known for the assistant %q; the assistants a skill can be installed "+
		"for are %s. An assistant that is not listed can still use Aggrega through its MCP server: connect it to "+
		"the command %q as %s describes",
		id, strings.Join(AgentIDs(), ", "), mcpServerCommandLine(), SetupPage())
}

// guardTarget asks guard about a skill folder and about every file the skill
// writes in it, in that order.
func guardTarget(guard func(string) error, dir string, files []File) error {
	if err := guard(dir); err != nil {
		return err
	}
	for _, f := range files {
		if err := guard(filepath.Join(dir, filepath.FromSlash(f.Path))); err != nil {
			return err
		}
	}
	return nil
}

// checkTarget reports whether a skill folder already holds a copy the skill may
// replace, and refuses a folder holding anything it may not: a file standing
// where the folder or one of the skill's files belongs, or a file at one of the
// skill's paths that does not carry the banner.
func checkTarget(dir string, files []File) (replaced bool, err error) {
	info, err := os.Stat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, err
	case !info.IsDir():
		return false, fmt.Errorf("%s is a file where the skill's folder belongs, so nothing was written: move it "+
			"aside or remove it, then run the command again", dir)
	}
	for _, f := range files {
		p := filepath.Join(dir, filepath.FromSlash(f.Path))
		info, err := os.Stat(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, err
		}
		if info.IsDir() {
			return false, fmt.Errorf("%s is a folder where the skill's file belongs, so nothing was written: move "+
				"it aside or remove it, then run the command again", p)
		}
		existing, err := os.ReadFile(p)
		if err != nil {
			return false, err
		}
		if !HasBanner(existing) {
			return false, fmt.Errorf("%s is already there and was not written by \"aggrega skills install\", so "+
				"nothing was written: move it aside or remove it, then run the command again", p)
		}
		replaced = true
	}
	return replaced, nil
}

// writeFiles writes every file of a skill beneath dir, each replacing what was
// there in one step: the bytes go to a temporary file beside the target first,
// so a reader never sees half a file.
func writeFiles(dir string, files []File) error {
	for _, f := range files {
		p := filepath.Join(dir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := writeFileAtomic(p, f.Content); err != nil {
			return err
		}
	}
	return nil
}

// writeFileAtomic writes content to path through a temporary file in the same
// folder and a rename.
func writeFileAtomic(path string, content []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}
