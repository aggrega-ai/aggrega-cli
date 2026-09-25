// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package publishops

import (
	"context"
	"fmt"
	"io"

	"github.com/aggrega-ai/aggrega-cli/internal/dryrunreport"
	"github.com/aggrega-ai/aggrega-cli/internal/entitypin"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// PinSourcesSupply names how one particular caller asks a publication to run the
// entity author's pin step on this one invocation, in that caller's own
// vocabulary: a flag at a terminal, a request field elsewhere.
//
// It exists for the reason DestinationSupply does. The condition a publication
// refuses on — the entity reads an input its lock records no pin for — is one
// condition with one home, the halt a dependency resolution raises over the same
// lock, while what a caller can do about it on this invocation differs by
// surface. So the phrasing travels in as a parameter and the condition is not
// re-decided per surface.
//
// The zero value names no per-run request. The refusal then names the author's
// own step and the workspace setting, which are true on every surface.
type PinSourcesSupply string

// pinBeforePublishing runs the entity author's pin step on the entity a
// publication is about to bundle, and returns what the step did. Push calls it
// where this invocation — or, where it says nothing, the workspace's
// pin_sources_at_push — asks for the step.
//
// It is the step [entitypin.Pin] runs for the author's own command, reached
// rather than restated, so the lock a publication writes is byte for byte the
// lock `aggrega pin` writes for the same entity.
//
// A dry run pins as a dry run: the step resolves what it would record and writes
// nothing, and the lock it would write is reported on the publication's own
// status output beside the bundle facts. The bundle a dry run reports is then the
// directory as it stands, which is said beside it, so a reader comparing the two
// is not left to work out why a real publication's size differs.
func pinBeforePublishing(ctx context.Context, out io.Writer, cfg PushConfig, rw *workspaceconfig.ResolvedWorkspace) (*entitypin.Result, error) {
	res, err := entitypin.Pin(ctx, entitypin.Options{
		Workspace:  rw,
		EntityPath: cfg.EntityPath,
		DryRun:     cfg.DryRun,
	})
	if err != nil {
		return nil, fmt.Errorf("pin %s before publishing: %w", cfg.EntityPath, err)
	}
	if cfg.DryRun && res.Report != nil {
		dryrunreport.Write(out, []dryrunreport.File{*res.Report})
	}
	fmt.Fprintln(out, res.Summary(cfg.DryRun))
	if cfg.DryRun && res.Pinnable && res.Outcome.Changed {
		fmt.Fprintf(out, "The bundle below is the directory as it stands, without the %s the step would write\n",
			recipepin.LockFileName)
	}
	return &res, nil
}

// holdBeforeBundling refuses to bundle an entity whose lock does not record, at
// the value the pin step would record it, a pin for every input the entity reads
// — every class of pin the lock holds, not remote sources alone. Push calls it
// where this invocation does not run the step, which is the ordinary path and the
// one a pipeline takes.
//
// It is the one halt a dependency resolution raises over the same lock,
// [recipepin.ErrEntityNotPinned], reached through [entitypin.Hold] rather than
// composed again, so a publication and a pull refuse the same entity for the same
// inputs in the same words — and its subject is exactly what a run of the step
// would have written, so an entity the step pinned is one it accepts. What a
// publication adds is only its own remedy: the author's step, this caller's way
// of asking for the step on this run, and the workspace setting that asks for it
// on every run.
//
// It refuses rather than repairing, which is the discipline a Plugin publication
// follows over its own machine-owned records: the author runs the step and
// commits what it wrote, and publication verifies it, so a published entity
// carries a lock somebody reviewed rather than one publication rewrote unseen.
//
// A dry run holds exactly as a publication does, so a dry run that reports a
// bundle is one the publication would not refuse here. The hold acquires what
// the step would acquire — a remote source whose identity the entity writes out
// in full — because a stale pin is only visible against the bytes the source
// serves now. An entity that reads nothing a lock records — a kind with no pin
// class, a Recipe with no remote source, no imported file and no local source, or
// one whose only source's identity is filled in per Release and so pinned in each
// Release's lockfile — is accepted, and a hold over it acquires nothing.
func holdBeforeBundling(ctx context.Context, cfg PushConfig, rw *workspaceconfig.ResolvedWorkspace) error {
	return entitypin.Hold(ctx, entitypin.Options{Workspace: rw, EntityPath: cfg.EntityPath}, recipepin.Remedy{
		Operation:  "publication",
		Setting:    workspaceconfig.PinSourcesAtPushKey,
		Invocation: string(cfg.PinSourcesSupply),
	})
}
