// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package wasm

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

// TestDescriptorBytesProfileIndependent proves the load profile does not alter
// the describe() descriptor: a non-CLI guest loaded under the describe-only
// profile (LoadForDescribe) and under the build profile (LoadAndDescribe) yields
// a byte-identical marshalled descriptor. The two profiles differ only in the
// load half — the import allowlist and whether host functions are real or inert
// stubs — and share the same post-load describe/validate primitive, so the
// self-report they surface must be the same bytes. This is the load-bearing
// no-behaviour-drift proof for routing the describe-only introspection surfaces
// through LoadForDescribe.
func TestDescriptorBytesProfileIndependent(t *testing.T) {
	ctx := context.Background()
	// A guest that imports only aggrega_emit_warning (in the shared allowlist),
	// so both the build profile and the describe-only profile admit it — the
	// comparison is a true A/B over the same guest across both profiles rather
	// than over two guests the profiles happen to admit separately.
	path := buildGuestFromDir(t, filepath.Join("testdata", "describe-emits-warning"))

	loader := &Loader{}

	buildPlugin, buildDesc, err := loader.LoadAndDescribe(ctx, PluginSpec{Name: "describe-emits-warning", Path: path})
	if err != nil {
		t.Fatalf("LoadAndDescribe (build profile) failed on the non-CLI guest: %v", err)
	}
	defer func() { _ = buildPlugin.Close(ctx) }()

	describePlugin, describeDesc, err := loader.LoadForDescribe(ctx, PluginSpec{Name: "describe-emits-warning", Path: path})
	if err != nil {
		t.Fatalf("LoadForDescribe failed on the non-CLI guest: %v", err)
	}
	defer func() { _ = describePlugin.Close(ctx) }()

	buildJSON, err := json.Marshal(buildDesc)
	if err != nil {
		t.Fatalf("marshal build-profile descriptor: %v", err)
	}
	describeJSON, err := json.Marshal(describeDesc)
	if err != nil {
		t.Fatalf("marshal describe-only descriptor: %v", err)
	}

	if !bytes.Equal(buildJSON, describeJSON) {
		t.Fatalf("descriptor bytes differ across load profiles; the profile must not change the descriptor:\n build:    %s\n describe: %s", buildJSON, describeJSON)
	}
}
