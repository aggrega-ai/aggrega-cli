// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

// The tool surface's leg of the withdrawal proof.
//
// A withheld reply looks identical whether the work stopped or ran to completion:
// both hand something back, and neither says whether a registry was reached or a
// file was written. So the assertion that carries this file is on an OBSERVABLE
// EFFECT — the lockfile on disk — and the reply is only ever a secondary check,
// deliberately raised with t.Error rather than t.Fatal so the on-disk assertion is
// always reached.
//
// The invocation is driven at the tool's own invoke rather than through tools/call,
// and the reason is a measurement rather than convenience: this server's tool
// dispatch hands every invoke a context nothing cancels, so a withdrawal cannot be
// expressed through that path at all. What is under test is whether the invoke
// carries the context it is handed into the resolution, and calling it with one is
// exactly the question.
//
// No assertion here carries a timing bound. The context is withdrawn BEFORE the
// call, so where the stop lands is fixed by the code rather than by how fast this
// machine resolves.

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// invokePullWithContext drives the pull tool's own invoke with a caller-supplied
// context, which is the only way to hand this surface a withdrawal.
func invokePullWithContext(t *testing.T, s *server, ctx context.Context, a pullArgs) *callToolResult {
	t.Helper()
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal pull args: %v", err)
	}
	res, rpcErr := pullInvoke(s, ctx, raw)
	if rpcErr != nil {
		t.Fatalf("the pull invoke returned a protocol error: %+v", rpcErr)
	}
	if res == nil {
		t.Fatal("the pull invoke returned neither a result nor a protocol error")
	}
	return res
}

// TestAWithdrawnPullToolInvocationWritesNoLockfile asserts that a caller which
// withdraws stops the resolution rather than only the answer to it.
//
// The fixture carries no lockfile, so the file's presence is the whole
// discriminator: absent means nothing was pinned, and present means the run
// carried on past the point the caller gave up. The control below is what makes
// the absence a measurement rather than a property of a fixture that could never
// have produced one.
func TestAWithdrawnPullToolInvocationWritesNoLockfile(t *testing.T) {
	t.Run("a withdrawn invocation leaves the workspace with no lockfile", func(t *testing.T) {
		s, root, dirs, releasePath := pullWorkspace(t)
		lockPath := lockfile.PathFor(releasePath)
		if _, err := os.Stat(lockPath); err == nil {
			t.Fatalf("precondition: the fixture already carries a lockfile at %s, so the assertion below "+
				"would read the same whether the resolution ran or not", lockPath)
		}

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		res := invokePullWithContext(t, s, ctx, pullArgs{
			Release: releasePath, WorkspaceRoot: root, CatalogDirs: dirs,
		})

		// Non-fatal on purpose: a t.Fatal here would short-circuit the on-disk
		// assertion below, which is the load-bearing half of this proof.
		//
		// The refusal is attributed to the WITHDRAWAL rather than accepted as any
		// error, because an absent lockfile is also what an invocation refused on
		// its arguments produces — and this proof would then read green on a
		// refusal that never reached the resolution. A tool result carries text
		// rather than an error value, so the attribution is the sentence the
		// resolution's own stop writes; reword that sentence and re-point this
		// assertion rather than weakening it.
		if !res.IsError {
			t.Error("a withdrawn invocation answered as a completed resolution, so nothing distinguishes " +
				"it from one that ran")
		} else if !strings.Contains(res.Content[0].Text, "stopped before the lockfile was written") {
			t.Errorf("the withdrawn invocation refused for some reason other than the caller giving up, so "+
				"the absent lockfile below says nothing about the withdrawal: %s", res.Content[0].Text)
		}
		if _, err := os.Stat(lockPath); err == nil {
			t.Errorf("the withdrawn invocation wrote the lockfile anyway: %s\n"+
				"The context this invoke was handed did not reach the resolution, so pins landed on disk "+
				"after the caller gave up.", lockPath)
		}
	})

	t.Run("positive control: an invocation nobody withdrew writes it", func(t *testing.T) {
		s, root, dirs, releasePath := pullWorkspace(t)
		lockPath := lockfile.PathFor(releasePath)

		res := invokePullWithContext(t, s, t.Context(), pullArgs{
			Release: releasePath, WorkspaceRoot: root, CatalogDirs: dirs,
		})
		if res.IsError {
			t.Fatalf("the control invocation refused, so the absence asserted above proves nothing: %s",
				res.Content[0].Text)
		}
		if _, err := os.Stat(lockPath); err != nil {
			t.Fatalf("the control invocation wrote no lockfile, so the absence asserted above is a "+
				"property of the fixture rather than of the withdrawal: %v", err)
		}
	})
}
