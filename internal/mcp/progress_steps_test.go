// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/opprogress"
)

// TestTheStepsAnOperationWaitsOnRaiseTheRequestsProgress drives the steps an
// operation reports waiting on under the context the route builds for a call, and
// reads what reached the transport's sink: one rising notification on the
// caller's token per step where the call minted a token, and nothing where it
// minted none.
func TestTheStepsAnOperationWaitsOnRaiseTheRequestsProgress(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		params string
		want   []uint64
	}{
		{name: "a call that minted a token is told of each step", params: `{"_meta":{"progressToken":"tok"}}`, want: []uint64{1, 2}},
		{name: "a call that minted none is told nothing", params: `{}`, want: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var sent []progressNotification
			ctx := withProgressSink(t.Context(), func(note progressNotification) { sent = append(sent, note) })
			ctx, rpcErr := withProgressReporter(ctx, &rpcRequest{Params: json.RawMessage(tc.params)})
			if rpcErr != nil {
				t.Fatalf("withProgressReporter: %v", rpcErr)
			}
			waitOnTwoSteps(ctx)
			if len(sent) != len(tc.want) {
				t.Fatalf("got %d notifications, want %d", len(sent), len(tc.want))
			}
			for i, note := range sent {
				if note.Progress != tc.want[i] || string(note.ProgressToken) != `"tok"` {
					t.Errorf("notification %d: progress %d on token %s; want %d on \"tok\"", i, note.Progress, note.ProgressToken, tc.want[i])
				}
			}
		})
	}
}

// waitOnTwoSteps is an operation that waits on two steps.
func waitOnTwoSteps(ctx context.Context) {
	opprogress.Begin(ctx, "reading the trust policy hub.example advertises")(nil)
	opprogress.Begin(ctx, "fetching hub.example/plugin/cli/moo:1.0.0")(nil)
}
