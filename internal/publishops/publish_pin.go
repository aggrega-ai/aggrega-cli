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

// pinBeforePublishing runs the entity author's pin step on the entity a
// publication is about to bundle, where this invocation — or, where it says
// nothing, the workspace's pin_sources_at_push — asks for it, and returns what
// the step did. It returns nil, and touches nothing, where pinning was not asked
// for.
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
	if !rw.Config().PushPinsSources(cfg.PinSources) {
		return nil, nil
	}
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
