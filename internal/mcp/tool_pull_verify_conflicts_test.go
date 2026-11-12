// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/pullops"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// TestPullToolRefusesAVerifyBesideAnotherRequestBeforeItResolves drives each pair
// the verify refuses through the real tools/call dispatch, over a workspace the
// verify can answer.
//
// The resolution is reached through the tool's seam, wrapped so each call that
// reaches it is counted and then run for real. A refused pair must be answered as
// a failed result naming both arguments, before the resolution is reached, with
// the lockfile as it was. The first row is the positive control: a verify on its
// own reaches the resolution and answers, which shows the counter is live and the
// workspace verifiable, so a refusal below is the pair's.
//
// The pin_sources rows fail where the tool accepted the pair and ran the verify
// with the argument doing nothing. The upgrade_targets row is a non-regression
// row: the tool refused that pair before the rule moved into the shared
// resolution. The tool has no argument for a dry run, and none for an upgrade
// apart from the references it names, so neither has a row here.
func TestPullToolRefusesAVerifyBesideAnotherRequestBeforeItResolves(t *testing.T) {
	s, root, dirs, releasePath := pullWorkspace(t)
	written := callPull(t, s, pullArgs{Release: releasePath, WorkspaceRoot: root, CatalogDirs: dirs})
	if written.IsError {
		t.Fatalf("the preparing pull refused: %s", written.Content[0].Text)
	}
	lockPath := lockfile.PathFor(releasePath)
	before, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read the prepared lockfile: %v", err)
	}

	reached := 0
	s.resolveDependencies = func(ctx context.Context, cfg pullops.Config) (pullops.Result, error) {
		reached++
		return pullops.Run(ctx, cfg)
	}

	yes, no := true, false
	cases := []struct {
		name  string
		args  pullArgs
		names []string // empty for the control, which is answered
	}{
		{name: "verify on its own: the tool answers", args: pullArgs{Verify: true}},
		{name: "verify beside pin_sources true", args: pullArgs{Verify: true, PinSources: &yes}, names: []string{"verify", "pin_sources"}},
		{name: "verify beside pin_sources false", args: pullArgs{Verify: true, PinSources: &no}, names: []string{"verify", "pin_sources"}},
		{
			name:  "verify beside upgrade_targets",
			args:  pullArgs{Verify: true, UpgradeTargets: []string{"core/database"}},
			names: []string{"verify", "upgrade_targets"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reached = 0
			args := tc.args
			args.Release, args.WorkspaceRoot, args.CatalogDirs = releasePath, root, dirs
			res := callPull(t, s, args)

			if len(tc.names) == 0 {
				if res.IsError {
					t.Fatalf("verifying the lockfile the preparing pull wrote was refused: %s", res.Content[0].Text)
				}
				if !unmarshalPullPayload(t, res).Verified {
					t.Error("the control did not report itself as a comparison")
				}
				if reached != 1 {
					t.Errorf("the control reached the resolution %d times; want 1, or the counter below measures nothing", reached)
				}
			} else {
				if !res.IsError {
					t.Fatalf("the pair was accepted: %s", res.Content[0].Text)
				}
				if reached != 0 {
					t.Errorf("the refused pair reached the resolution %d time(s); it must be refused before any work", reached)
				}
				for _, owed := range tc.names {
					if !strings.Contains(res.Content[0].Text, "'"+owed+"'") {
						t.Errorf("the refusal does not name %q, so the caller is not told which pair it may not send: %q",
							owed, res.Content[0].Text)
					}
				}
			}

			after, err := os.ReadFile(lockPath)
			if err != nil {
				t.Fatalf("read the lockfile after the call: %v", err)
			}
			if !bytes.Equal(before, after) {
				t.Error("the call wrote to the lockfile")
			}
		})
	}
}
