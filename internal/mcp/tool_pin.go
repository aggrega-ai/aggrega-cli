// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/entitypin"
	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
)

// pinArgs is the pin tool's argument shape: the entity document whose inputs
// are pinned, whether to report instead of writing, and the workspace the call
// means.
//
// No Release is named, and its absence is the contract rather than an omission:
// the step pins what an entity's author already knows, which needs nothing a
// Release supplies.
type pinArgs struct {
	Entity        string `json:"entity"`
	DryRun        bool   `json:"dry_run"`
	WorkspaceRoot string `json:"workspace_root"`
}

// pinClassCount is how many pins of one class the lock holds after the step.
type pinClassCount struct {
	Class string `json:"class"`
	Pins  int    `json:"pins"`
}

// pinPayload is the pin tool's structured result: what the step did to the lock
// beside the entity.
//
// pinnable is false for a kind that reads nothing a lock records, and every
// other field is then empty: nothing beside the entity was touched. changed
// separates a write from a lock that already recorded every pin. content is the
// lock a dry run would have written, and is absent on every other answer.
type pinPayload struct {
	Entity   string          `json:"entity"`
	Kind     string          `json:"kind"`
	Name     string          `json:"name"`
	Pinnable bool            `json:"pinnable"`
	Lock     string          `json:"lock,omitempty"`
	Changed  bool            `json:"changed"`
	Removed  bool            `json:"removed,omitempty"`
	DryRun   bool            `json:"dry_run,omitempty"`
	Pins     []pinClassCount `json:"pins,omitempty"`
	Content  string          `json:"content,omitempty"`
}

// pinPayloadOf projects one run of the pin step onto this surface's result
// shape. It is the one projection here, so the pin tool and a publication that
// pinned report the step in one shape.
func pinPayloadOf(res entitypin.Result, dryRun bool) pinPayload {
	out := pinPayload{
		Entity:   res.EntityPath,
		Kind:     res.Kind,
		Name:     res.Name,
		Pinnable: res.Pinnable,
		DryRun:   dryRun,
	}
	if !res.Pinnable {
		return out
	}
	out.Lock = res.Outcome.LockPath
	out.Changed = res.Outcome.Changed
	out.Removed = res.Outcome.Removed
	for _, c := range res.Outcome.Pins {
		out.Pins = append(out.Pins, pinClassCount{Class: c.Class, Pins: c.Pins})
	}
	if res.Report != nil {
		out.Content = string(res.Report.Content)
	}
	return out
}

var pinToolSpec = toolSpec{
	kinds:   []string{"featurerecipe", "recipe"},
	summary: "Records the content pins for an entity's external inputs in the entity.lock beside it.",
	description: "Write, into the lock file beside a Recipe or FeatureRecipe, the content pin for every input the " +
		"entity reads from outside its own document — it writes inside your workspace and reaches the hosts the " +
		"entity's sources name. That lock file is the entity.lock in the entity's own directory. Pass the entity " +
		"document as `entity`, absolute or relative to the workspace root. " +
		"It pins a remote source whose coordinates are written out in full, a file an artifacts entry imports, and " +
		"each file of a local source, by its path beside the entity; " +
		"a remote source whose coordinates come from a template is pinned per Release by the pull tool instead, so no " +
		"Release is needed here. Pinning is the entity author's step: call it before an entity is consumed or " +
		"published, and after changing one of its inputs, because a pull over an entity whose lock does not " +
		"record its pins, or whose local source has had a file added, changed or removed since it was pinned, " +
		"refuses and names this step. The entity file itself is never written, " +
		"and re-running over unchanged inputs leaves the lock byte-identical. Set `dry_run` to get the lock it would write " +
		"without writing it. The pull tool's `pin_sources` and the publish-entity tool's `pin_sources` run this " +
		"same step and write the same bytes.",
	workspaceSelectionNote: "Optional `workspace_root` overrides discovery.",
	inputSchema: `{
  "type": "object",
  "properties": {
    "entity": {
      "type": "string",
      "minLength": 1,
      "description": "Path to the Recipe or FeatureRecipe document to pin, absolute or relative to the workspace root."
    },
    "dry_run": {
      "type": "boolean",
      "description": "Resolve the entity's inputs and return the lock the step would write, writing nothing. The content cache still fills."
    },
    "workspace_root": {
      "type": "string",
      "description": "Optional workspace root override. Defaults to discovery from the server's working directory."
    }
  },
  "required": ["entity"]
}`,
	invoke: pinInvoke,
}

// pinInvoke runs the entity author's pin step over the entity the call names.
//
// It writes and reaches the network, and it runs on explicit invocation and never
// as a side effect of validating or inspecting — the rule every writing tool here
// runs under. Nothing is prompted for, and the step's own account of itself is
// the structured result rather than lines written to a process stream, which on
// this server carries framed protocol messages only.
func pinInvoke(s *server, ctx context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	var a pinArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return invalidToolArguments("pin", err)
		}
	}
	entity := strings.TrimSpace(a.Entity)
	if entity == "" {
		return errorResult("pin requires an 'entity' path: the Recipe or FeatureRecipe document whose inputs to pin.")
	}
	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, nil)
	if err != nil {
		return errorResult(explainToolError("Could not resolve the workspace to pin in.", err, ""))
	}
	entityPath, refusal := tw.workspacePath("pin", "entity", entity)
	if refusal != "" {
		return errorResult(refusal)
	}
	res, err := entitypin.Pin(ctx, entitypin.Options{Workspace: tw.rw, EntityPath: entityPath, DryRun: a.DryRun})
	if err != nil {
		return errorResult(explainToolError("The pin step did not complete, so the lock beside the entity is unchanged.", err, ""))
	}
	payload := pinPayloadOf(res, a.DryRun)
	return textResult(res.Summary(a.DryRun)+".", payload)
}

// publishReach reports how far a publication would reach: the registry, which
// is what the operation declares, or also the workspace where this call would
// run the pin step first — the step writes the lock beside the entity and fills
// the workspace's content cache, so it needs what the pull tool needs.
//
// It reads the decision from the one composition every surface carrying a
// publication reads — the call's own argument and the workspace's own
// pin_sources_at_push setting — rather than branching on the argument, for the
// reason the build's widening does: a second derivation of the decision would
// agree with the publication until one of them learned about an input the other
// did not. The declared class is still asked for first, so a call reaching both
// is admitted only by a credential carrying both scopes.
func publishReach(s *server, args json.RawMessage) opsurface.Reach {
	var a publishEntityArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return opsurface.ReachRegistry
	}
	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, nil)
	if err != nil {
		return opsurface.ReachRegistry
	}
	if tw.cfg.PushPinsSources(a.PinSources) {
		return opsurface.ReachWorkspace
	}
	return opsurface.ReachRegistry
}

// renderPinOutcome renders a publication's pin outcome as the sentence its text
// result carries, and the empty string where the publication did not pin.
func renderPinOutcome(p *pinPayload) string {
	if p == nil {
		return ""
	}
	if !p.Pinnable {
		return fmt.Sprintf(" Nothing to pin: a %s reads no input a lock records.", p.Kind)
	}
	switch {
	case !p.Changed:
		return fmt.Sprintf(" The pin step found %s already current.", p.Lock)
	case p.DryRun:
		return fmt.Sprintf(" The pin step would write %s; nothing was written.", p.Lock)
	default:
		return fmt.Sprintf(" The pin step wrote %s before the bundle was built.", p.Lock)
	}
}
