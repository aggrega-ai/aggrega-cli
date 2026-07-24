// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lockfile_test

import (
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// TestPinsByURI pins that PinsByURI projects every closure-walked entity
// kind (entities, schema + mapping + workload libraries, wasm plugins) into
// a URI->digest map and excludes source artifacts, which are descended on
// their own path and carry their own read-time digest gate.
func TestPinsByURI(t *testing.T) {
	t.Parallel()
	lf := &lockfile.Lockfile{
		Entities: []lockfile.Entity{
			{URI: "core/mariadb:1.0.0", Digest: "sha256:aaa"},
			{URI: "core/redis:2.0.0", Digest: "sha256:bbb"},
		},
		SchemaLibraries: []lockfile.SchemaLibrary{
			{URI: "core/DbSizing:1.0.0", Digest: "sha256:ccc"},
		},
		MappingLibraries: []lockfile.MappingLibrary{
			{URI: "core/BaseMappings:1.0.0", Digest: "sha256:ddd"},
		},
		WorkloadLibraries: []lockfile.WorkloadLibrary{
			{URI: "core/BaseWorkloads:1.0.0", Digest: "sha256:eee"},
		},
		Plugins: []lockfile.Plugin{
			{URI: "core/policy:1.0.0", Digest: "sha256:fff"},
		},
		SourceArtifacts: []lockfile.SourceArtifact{
			{Kind: "helm-oci-chart", URL: "oci://example/chart", Digest: "sha256:ggg"},
		},
	}

	got := lf.PinsByURI()
	want := map[string]string{
		"core/mariadb:1.0.0":       "sha256:aaa",
		"core/redis:2.0.0":         "sha256:bbb",
		"core/DbSizing:1.0.0":      "sha256:ccc",
		"core/BaseMappings:1.0.0":  "sha256:ddd",
		"core/BaseWorkloads:1.0.0": "sha256:eee",
		"core/policy:1.0.0":        "sha256:fff",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PinsByURI() = %v; want %v (source artifacts excluded)", got, want)
	}
}
