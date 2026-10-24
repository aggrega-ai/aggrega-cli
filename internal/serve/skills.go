// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"

	"github.com/aggrega-ai/aggrega-cli/internal/skills"
	"github.com/aggrega-ai/aggrega-cli/pkg/machinedirs"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// skillsInstallParams is what aggrega/skillsInstall takes. The binary ships one
// skill, so no field names it, exactly as the command line takes no argument
// naming it.
//
// The spellings follow the command line's flags. agents is a list because the
// flag it mirrors may be repeated, and the plural is what a caller sending one
// list reads it as. global selects each assistant's per-user skill folder; absent,
// the skill lands in each assistant's project folder inside the workspace the
// request names.
type skillsInstallParams struct {
	Agents []string `json:"agents"`
	Global bool     `json:"global"`
}

// skillsInstalledFolder is one skill folder an installation wrote.
//
// Agents carries the identifiers of every named assistant that reads the folder,
// in the order the request named them: several assistants read one shared folder,
// and the skill is written there once for all of them, so the answer names the
// folder once with each of them rather than once per assistant.
type skillsInstalledFolder struct {
	Agents   []string `json:"agents"`
	Folder   string   `json:"folder"`
	Replaced bool     `json:"replaced"`
}

// skillFolderScope states how far a skill installation reached.
//
// A request names one workspace, so an answer that said nothing would be read as
// being about that workspace — and for an installation into the per-user folders
// that reading is wrong, because the assistant reads those in every project on
// the machine. MachineWide is therefore derived from which folders were written
// rather than left for a caller to infer from the field it sent.
type skillFolderScope struct {
	// MachineWide says the folders written are the per-user ones under the home
	// directory, which the assistant reads in every project.
	MachineWide bool `json:"machineWide"`
	// WorkspaceRoot is the workspace the request named.
	WorkspaceRoot string `json:"workspaceRoot"`
}

// skillsInstallResult is the aggrega/skillsInstall response.
//
// Installed lists one entry per folder written, in the order the request named
// the assistants, each saying whether a copy carrying the banner every written
// copy carries was there and has been replaced.
type skillsInstallResult struct {
	Skill     string                  `json:"skill"`
	Installed []skillsInstalledFolder `json:"installed"`
	Scope     skillFolderScope        `json:"scope"`
}

// dispatchSkillsInstall writes the assistant skill built into this binary into
// each named assistant's skill folder.
//
// 🔴 It WRITES, and it is admitted on the rule every writing method here runs
// under: a caller that sent it has asked for the installation. What stays out of
// bounds is a write performed while validating or inspecting, where nobody asked,
// and this is not that.
//
// A project installation lands in each assistant's project folder inside the
// workspace the request names — the root the editor opened — and never in the
// directory the daemon happens to run in, which is a property of how it was
// started rather than of the project the caller is working in. Each path it would
// write is held to that workspace before anything is written, so a folder a link
// leads out of the workspace is refused with nothing written. With global the
// skill lands in each assistant's per-user folder under the home directory,
// which is read through the same environment every workspace this daemon resolves
// reads, and the answer's scope says the change reaches every project.
//
// The installation itself is the one the command line runs: the same folder
// table, the same refusals — an assistant no folder is known for, a file at the
// target that does not carry the banner — and the same rule that every
// target is checked before anything is written. Nothing is prompted for.
func (s *Server) dispatchSkillsInstall(ctx context.Context, req *jsonRPCRequest) error {
	var params skillsInstallParams
	if err := decodeParams(req, &params); err != nil {
		return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "invalid params: ", err)
	}
	if len(params.Agents) == 0 {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"skills install: agents is required (one or more identifiers of the assistants to install "+
				"the skill for; aggrega/skillsList reports every identifier)")
	}
	if s.workspaceRoot == "" {
		return s.writeError(ctx, req.ID, jsonRPCInternalError,
			"the daemon holds no workspace to install the skill into")
	}

	install := skills.InstallRequest{
		Skill:  skills.InstalledSkill,
		Agents: params.Agents,
		Global: params.Global,
	}
	if params.Global {
		home, err := skills.UserHome(machinedirs.Host(), s.flags.Lookup(nil))
		if err != nil {
			return s.writeRefusal(ctx, req.ID, jsonRPCInternalError, "skills install: ", err)
		}
		install.Base = home
	} else {
		root := s.workspaceRoot
		install.Base = root
		install.Guard = func(path string) error { return writescope.CheckWrite(root, path) }
	}

	installed, err := skills.Install(install)
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "skills install: ", err)
	}

	folders := make([]skillsInstalledFolder, 0, len(installed))
	for _, in := range installed {
		ids := make([]string, 0, len(in.Agents))
		for _, a := range in.Agents {
			ids = append(ids, a.ID)
		}
		folders = append(folders, skillsInstalledFolder{Agents: ids, Folder: in.Dir, Replaced: in.Replaced})
	}
	return s.writeResult(ctx, req.ID, skillsInstallResult{
		Skill:     skills.InstalledSkill,
		Installed: folders,
		Scope: skillFolderScope{
			MachineWide:   params.Global,
			WorkspaceRoot: s.workspaceRoot,
		},
	})
}

// skillsListedSkill is one skill this binary ships.
type skillsListedSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// skillsListedAgent is one assistant aggrega/skillsInstall can write a skill for.
//
// Both folders are slash-separated and relative: ProjectFolder to a project's
// root, which on this surface is the workspace a request names, and UserFolder to
// the home directory. They are the folders the assistant's own documentation
// names, and the ones an installation writes the skill's folder inside.
type skillsListedAgent struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ProjectFolder string `json:"projectFolder"`
	UserFolder    string `json:"userFolder"`
}

// skillsListResult is the aggrega/skillsList response.
type skillsListResult struct {
	Skills []skillsListedSkill `json:"skills"`
	Agents []skillsListedAgent `json:"agents"`
}

// dispatchSkillsList reports the skills this binary ships and every assistant an
// installation can write them for, with the two folders each one reads.
//
// It writes nothing, reaches nothing and reads nothing from the workspace the
// request names, which it takes on the terms every request here does: both
// lists come from what the binary carries, in the fixed order the command line
// prints them, and they are the same table the installation reads, so the folders
// an editor shows before installing are the folders the installation writes.
func (s *Server) dispatchSkillsList(ctx context.Context, req *jsonRPCRequest) error {
	if err := decodeParams(req, &noParams{}); err != nil {
		return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "invalid params: ", err)
	}

	names := skills.Names()
	listed := make([]skillsListedSkill, 0, len(names))
	for _, name := range names {
		meta, err := skills.Describe(name)
		if err != nil {
			return s.writeRefusal(ctx, req.ID, jsonRPCInternalError, "skills list: ", err)
		}
		listed = append(listed, skillsListedSkill{Name: name, Description: meta.Description})
	}
	known := skills.Agents()
	agents := make([]skillsListedAgent, 0, len(known))
	for _, a := range known {
		agents = append(agents, skillsListedAgent{
			ID:            a.ID,
			Name:          a.Name,
			ProjectFolder: a.ProjectDir,
			UserFolder:    a.UserDir,
		})
	}
	return s.writeResult(ctx, req.ID, skillsListResult{Skills: listed, Agents: agents})
}
