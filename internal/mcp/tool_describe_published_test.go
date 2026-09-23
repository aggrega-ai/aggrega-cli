// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDescribeReadsARegistryReferenceOnlyForALocalClient pins, per transport,
// where the describe tool's registry-reference form is offered — and neither row
// can reach a network.
//
// Over a listener the reference is refused by name before anything is resolved,
// which is why that row needs no workspace at all: a refusal that ran after the
// workspace resolved, or after the egress gate, would be a read the listener had
// started. Over standard input and output the same call is routed to the
// published-entity reading; the workspace it names is offline, so the reading is
// refused at the egress gate, and that refusal — reachable only through the
// reading — is the evidence the call went there rather than to the catalog.
func TestDescribeReadsARegistryReferenceOnlyForALocalClient(t *testing.T) {
	t.Parallel()
	const reference = "oci://registry.example.com/component/core/api:1.0.0"

	textOf := func(t *testing.T, res *callToolResult, rpcErr *rpcError) string {
		t.Helper()
		if rpcErr != nil {
			t.Fatalf("describe answered with a protocol error rather than a tool result: %+v", rpcErr)
		}
		if res == nil || !res.IsError || len(res.Content) == 0 {
			t.Fatalf("describe did not answer with a tool failure: %+v", res)
		}
		return res.Content[0].Text
	}

	t.Run("listener", func(t *testing.T) {
		t.Parallel()
		s := &server{transport: transportHTTP}
		args, _ := json.Marshal(map[string]any{"name": reference})
		res, rpcErr := describeInvoke(s, context.Background(), args)
		text := textOf(t, res, rpcErr)
		if !strings.Contains(text, "only for a client that launched this server over its standard input and output") {
			t.Errorf("the refusal does not say which caller the read is for: %q", text)
		}
		if strings.Contains(text, "E0125") || strings.Contains(text, "Could not resolve the workspace") {
			t.Errorf("the listener refused only after starting the read: %q", text)
		}
	})

	t.Run("standard input and output", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, ".aggrega-config.yaml"), []byte("workspace: {}\noffline: true\n"), 0o644); err != nil {
			t.Fatalf("write workspace config: %v", err)
		}
		s := &server{transport: transportStdio}
		args, _ := json.Marshal(map[string]any{"name": reference, "workspace_root": root})
		res, rpcErr := describeInvoke(s, context.Background(), args)
		text := textOf(t, res, rpcErr)
		if !strings.Contains(text, "E0125") || !strings.Contains(text, "offline-egress") {
			t.Errorf("the read did not reach the egress gate the published-entity reading passes through: %q", text)
		}
		if strings.Contains(text, "only for a client that launched") || strings.Contains(text, "No catalog entity named") {
			t.Errorf("a client on standard input and output was refused, or sent to the catalog: %q", text)
		}
	})
}
