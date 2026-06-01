// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package parse_test

import (
	"encoding/json"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parse"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestNodePayloadMap is the single shared extraction the lock-time source
// descent and the build-time source-eval both use to turn an IR values/infra
// mapping entry into the template pillar map — so the two passes cannot drift on
// the conversion. Pins: present node → flattened map; absent node → empty;
// malformed payload → empty (no panic).
func TestNodePayloadMap(t *testing.T) {
	t.Parallel()
	mapping := types.OrderedMap[json.RawMessage]{}
	mapping.Set("api", json.RawMessage(`{"source_version":"25.0.8","replicas":3}`))
	mapping.Set("bad", json.RawMessage(`{not json`))

	got := parse.NodePayloadMap(mapping, "api")
	if got["source_version"] != "25.0.8" {
		t.Errorf("source_version = %v; want 25.0.8", got["source_version"])
	}
	if len(got) != 2 {
		t.Errorf("len = %d; want 2 (source_version, replicas)", len(got))
	}

	if absent := parse.NodePayloadMap(mapping, "missing"); len(absent) != 0 {
		t.Errorf("absent node should yield empty map; got %v", absent)
	}
	if bad := parse.NodePayloadMap(mapping, "bad"); len(bad) != 0 {
		t.Errorf("malformed payload should yield empty map (no panic); got %v", bad)
	}
}
