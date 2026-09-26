// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// TestResolveDependenciesRefusesAVerifyBesideAnotherRequestBeforeItBegins drives
// each pair a verify refuses through the daemon's dispatch loop, over real framed
// streams, on a workspace the verify can answer.
//
// Every request carries a progress token on a session that negotiated progress, so
// "refused before any work" is observable rather than assumed: a request refused on
// its params reports nothing, while one that began reports a begin and an end. The
// first row is the positive control — a verify on its own answers AND reports
// progress — which shows the token is live and the workspace verifiable, so an
// absence of progress below is the refusal's and a refusal is the pair's.
//
// Every row here fails where the daemon accepted the pair and ran the verify, with
// the field doing nothing or, for an upgrade, comparing against a resolution no
// reconcile performs. The daemon has no field for a dry run — its params decode
// refuses one as undeclared — so that pair has no row here.
func TestResolveDependenciesRefusesAVerifyBesideAnotherRequestBeforeItBegins(t *testing.T) {
	cases := []struct {
		name  string
		extra map[string]any
		field string // empty for the control, which is answered
	}{
		{name: "verify on its own: the daemon answers"},
		{name: "verify beside pinSources true", extra: map[string]any{"pinSources": true}, field: "pinSources"},
		{name: "verify beside pinSources false", extra: map[string]any{"pinSources": false}, field: "pinSources"},
		{name: "verify beside a bare upgrade", extra: map[string]any{"upgrade": true}, field: "upgrade"},
		{name: "verify beside targets", extra: map[string]any{"targets": []string{"demo/svc"}}, field: "targets"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws, releaseRel := autoPullWorkspace(t)
			t.Chdir(ws)
			srv := newFillCapableServer(t, ws)
			if _, errEnvelope := srv.handleResolveDependencies(context.Background(),
				resolveDependenciesParams{Release: releaseRel}, srv.progressFor(context.Background())); errEnvelope != nil {
				t.Fatalf("the preparing resolution failed: %s", errEnvelope.Message)
			}
			lockPath := lockfile.PathFor(filepath.Join(ws, releaseRel))
			before, err := os.ReadFile(lockPath)
			if err != nil {
				t.Fatalf("read the prepared lockfile: %v", err)
			}

			params := map[string]any{"release": releaseRel, "verify": true, "workDoneToken": "verify-conflict"}
			for k, v := range tc.extra {
				params[k] = v
			}
			frames := driveSession(t, srv,
				send(1, framedRequestIn(t, srv, 1, "aggrega/initialize", map[string]any{
					"capabilities": map[string]any{"window": map[string]any{"workDoneProgress": true}},
				})),
				send(2, framedRequestIn(t, srv, 2, "aggrega/resolveDependencies", params)),
			)
			resp := responseTo(t, frames, "2")
			progress := progressPayloads(t, frames)

			if tc.field == "" {
				if resp.Error != nil {
					t.Fatalf("verifying the lockfile the preparing resolution wrote was refused: code %d: %s",
						resp.Error.Code, resp.Error.Message)
				}
				var result struct {
					Verified bool `json:"verified"`
				}
				if err := json.Unmarshal(resp.Result, &result); err != nil || !result.Verified {
					t.Errorf("the control did not answer as a comparison (%v): %s", err, resp.Result)
				}
				if len(progress) == 0 {
					t.Error("the control reported no progress, so the absence asserted below would measure nothing")
				}
			} else {
				if resp.Error == nil {
					t.Fatalf("the pair was accepted: %s", resp.Result)
				}
				if resp.Error.Code != jsonRPCInvalidParams {
					t.Errorf("the refusal carries code %d, not invalid-params %d: %s",
						resp.Error.Code, jsonRPCInvalidParams, resp.Error.Message)
				}
				for _, owed := range []string{"verify", tc.field} {
					if !strings.Contains(resp.Error.Message, owed) {
						t.Errorf("the refusal does not name %q, so a caller cannot tell which pair it may not send: %s",
							owed, resp.Error.Message)
					}
				}
				// It is worded for the reader of this surface: a client author never
				// typed a command-line flag.
				for _, absent := range []string{"--verify", "--pin-sources", "--upgrade"} {
					if strings.Contains(resp.Error.Message, absent) {
						t.Errorf("the refusal names %q, which belongs to the command line: %s", absent, resp.Error.Message)
					}
				}
				if len(progress) != 0 {
					t.Errorf("a request refused on its params reported %d progress frame(s); nothing began: %+v",
						len(progress), progress)
				}
			}

			after, err := os.ReadFile(lockPath)
			if err != nil {
				t.Fatalf("read the lockfile after the request: %v", err)
			}
			if !bytes.Equal(before, after) {
				t.Error("the request wrote to the lockfile")
			}
		})
	}
}
