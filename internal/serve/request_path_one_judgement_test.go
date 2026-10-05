// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"strings"
	"testing"
)

// TestAPathARequestReadsIsJudgedByTheDeepestPartOfItThatExists drives the requests
// that READ a path they name — a lint of an entity, a graph of a Release, and an
// insertion point in a document — over one real root, naming each path in the
// shapes the one judgement of inside tells apart, and reads back whether the
// request was refused for where the path leads.
//
// A read and a write are held by one judgement: the deepest part of the path that
// exists, with its links resolved, against the root resolved the same way. So a
// path a request reads that is not there yet, beneath a directory linked out of
// the workspace, is refused for the link as a destination there would be — rather
// than being looked for outside and reported missing. Each row carries one path at
// fault, or none; the first row of each request is its control, a path inside the
// root, which is answered (or refused for something other than a link). The
// directory outside the root is listed before and after every row and must not
// have changed.
func TestAPathARequestReadsIsJudgedByTheDeepestPartOfItThatExists(t *testing.T) {
	t.Parallel()

	const linkRefusal = "leads outside the workspace root through a link"
	rows := []struct {
		name   string
		method string
		params map[string]any
		// refusal is the fragment a refused row's answer carries; empty for a row
		// whose path is held inside the root.
		refusal string
	}{
		{
			name:   "control: a lint of an entity inside the root is not refused for its path",
			method: "aggrega/lint",
			params: map[string]any{"entity": "entity.yaml"},
		},
		{
			name:    "a lint of an entity reached through a link leading out is refused",
			method:  "aggrega/lint",
			params:  map[string]any{"entity": "linked-entity.yaml"},
			refusal: `"linked-entity.yaml" ` + linkRefusal,
		},
		{
			name:    "a lint of an entity not there yet beneath a directory linked out is refused for the link",
			method:  "aggrega/lint",
			params:  map[string]any{"entity": "shared/missing.yaml"},
			refusal: `"shared/missing.yaml" ` + linkRefusal,
		},
		{
			name:   "control: a graph of a Release inside the root is not refused for its path",
			method: "aggrega/irGraph",
			params: map[string]any{"release": "plain-release.yaml"},
		},
		{
			name:    "a graph of a Release not there yet beneath a directory linked out is refused for the link",
			method:  "aggrega/irGraph",
			params:  map[string]any{"release": "shared/missing-release.yaml"},
			refusal: `"shared/missing-release.yaml" ` + linkRefusal,
		},
		{
			name:   "control: an insertion point in a document inside the root is answered",
			method: "aggrega/insertionPoint",
			params: map[string]any{"file": "entity.yaml", "container": []string{"metadata"}},
		},
		{
			name:    "an insertion point in a document not there yet beneath a directory linked out is refused for the link",
			method:  "aggrega/insertionPoint",
			params:  map[string]any{"file": "shared/missing.yaml", "container": []string{"metadata"}},
			refusal: errInsertionFileLeadsOut.Error(),
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			l := layDestinationLinks(t)
			srv := linkedFilesServer(t, l.root, "workspace: {}\n")
			before := treeListing(t, l.outside)

			resp := callPastPushes(t, srv, row.method, row.params)

			if after := treeListing(t, l.outside); strings.Join(after, "\n") != strings.Join(before, "\n") {
				t.Errorf("the directory outside the root changed:\nbefore:\n  %s\nafter:\n  %s",
					strings.Join(before, "\n  "), strings.Join(after, "\n  "))
			}
			if row.refusal == "" {
				if resp.Error != nil && resp.Error.Code == jsonRPCInvalidParams && strings.Contains(resp.Error.Message, "link") {
					t.Fatalf("a path inside the root was refused for a link: %q", resp.Error.Message)
				}
				if row.method == "aggrega/insertionPoint" {
					var got InsertionPointResult
					decodeDaemonResult(t, resp, &got)
					if got.File != "entity.yaml" {
						t.Errorf("a position was reported for %q; want entity.yaml", got.File)
					}
				}
				return
			}
			if resp.Error == nil {
				t.Fatalf("want the request refused with %q; got result %s", row.refusal, resp.Result)
			}
			if resp.Error.Code != jsonRPCInvalidParams {
				t.Errorf("Code = %d; want %d (invalid params) — a path a request may not name is the request's fault",
					resp.Error.Code, jsonRPCInvalidParams)
			}
			if !strings.Contains(resp.Error.Message, row.refusal) {
				t.Errorf("the refusal reads %q; want it to carry %q", resp.Error.Message, row.refusal)
			}
			if strings.Contains(resp.Error.Message, l.outside) {
				t.Errorf("the refusal names %s, the place a link leads to: %q", l.outside, resp.Error.Message)
			}
		})
	}
}
