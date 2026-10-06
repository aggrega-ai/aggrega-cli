// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/entitypin"
)

// pinParams is what aggrega/pin takes: the entity document whose inputs are
// pinned, relative to the workspace root under the contract every path-naming
// parameter here carries, and whether to report what the step would write rather
// than writing it.
//
// No Release is named, and its absence is the contract rather than an omission:
// the step pins what an entity's author already knows, which needs nothing a
// Release supplies.
type pinParams struct {
	Entity string `json:"entity"`
	DryRun bool   `json:"dryRun,omitempty"`
}

// pinResult is the aggrega/pin response: what the step did to the lock beside
// the entity, as the facts rather than as the line a terminal prints.
//
// Pinnable is false for a kind that reads nothing a lock records, and every other
// field is then empty: nothing beside the entity was touched. Changed separates a
// write from a lock that already recorded every pin, which was left
// byte-identical. Content is the lock a dry run would have written, and is absent
// on every other answer.
type pinResult struct {
	Entity   string          `json:"entity"`
	Kind     string          `json:"kind"`
	Name     string          `json:"name"`
	Pinnable bool            `json:"pinnable"`
	Lock     string          `json:"lock,omitempty"`
	Changed  bool            `json:"changed"`
	Removed  bool            `json:"removed,omitempty"`
	DryRun   bool            `json:"dryRun,omitempty"`
	Pins     []pinClassCount `json:"pins,omitempty"`
	Content  string          `json:"content,omitempty"`
}

// pinClassCount is how many pins of one class the lock holds after the step.
type pinClassCount struct {
	Class string `json:"class"`
	Pins  int    `json:"pins"`
}

// pinReportOf projects one run of the pin step onto the wire. It is the one
// projection here, so the pin method and a publication that pinned report the
// step in one shape.
func pinReportOf(res entitypin.Result, workspaceRoot string, dryRun bool) pinResult {
	out := pinResult{
		Entity:   workspaceRelative(workspaceRoot, res.EntityPath),
		Kind:     res.Kind,
		Name:     res.Name,
		Pinnable: res.Pinnable,
		DryRun:   dryRun,
	}
	if !res.Pinnable {
		return out
	}
	out.Lock = workspaceRelative(workspaceRoot, res.Outcome.LockPath)
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

// dispatchPin runs the entity author's pin step over the entity the request
// names.
//
// 🔴 It WRITES, and what it writes lands in the workspace: the lock beside the
// entity, and the sources it fetched in the workspace's content cache. It is
// admitted on the rule every writing method here runs under — a caller that sent
// it has asked for the pin, exactly as a caller sending a dependency resolution
// has asked for the lockfile — and it reaches the remote hosts the entity's
// sources name, which is the same reach that resolution has.
//
// The entity path is confined to the workspace this session opened, because the
// write lands beside it. Nothing is prompted for.
func (s *Server) dispatchPin(ctx context.Context, req *jsonRPCRequest) error {
	var params pinParams
	if len(req.Params) > 0 {
		if err := decodeParams(req, &params); err != nil {
			return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "invalid params: ", err)
		}
	}
	if params.Entity == "" {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"pin: entity is required (the Recipe or FeatureRecipe document whose inputs are pinned)")
	}
	entity, err := s.workspacePath("pin: entity", params.Entity)
	if err != nil {
		return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "", err)
	}
	if s.resolvedWorkspace == nil {
		return s.writeError(ctx, req.ID, jsonRPCInternalError, "the daemon holds no resolved workspace to pin in")
	}
	progress := s.progressFor(ctx)
	defer progress.done()
	progress.begin("Pinning an entity's inputs")

	res, err := entitypin.Pin(ctx, entitypin.Options{
		Workspace:  s.resolvedWorkspace,
		EntityPath: entity,
		DryRun:     params.DryRun,
	})
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return s.writeErrorEnvelope(ctx, req.ID, &jsonRPCError{
			Code:    errEntityNotResolvable,
			Message: err.Error(),
			cause:   err,
		})
	}
	// The report carries the line a terminal prints for the same run, so a client
	// watching the step reads the step's own account rather than one composed here.
	fmt.Fprintln(progress.writer(), res.Summary(params.DryRun))
	return s.writeResult(ctx, req.ID, pinReportOf(res, s.workspaceRoot, params.DryRun))
}
