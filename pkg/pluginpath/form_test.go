// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginpath

import (
	"errors"
	"strings"
	"testing"
)

// TestClassifyReference proves the structural classifier partitions every
// reference shape into the expected form. The provider/name:version case guards
// against the trap of treating a legitimate entity URI (a slash and a colon) as
// an artifact reference.
func TestClassifyReference(t *testing.T) {
	cases := []struct {
		uri  string
		want ReferenceForm
	}{
		{"./plugins/foo.wasm", FormLocalPath},
		{"../foo.wasm", FormLocalPath},
		{"/abs/plugins/foo.wasm", FormLocalPath},
		{"oci://hub.aggrega.ai/plugin/frp/patch:1.0.0", FormArtifact},
		{"foo.wasm", FormArtifact},
		{"frp-step-patch-rust.wasm", FormArtifact},
		{"core/frp/patch@sha256:0000000000000000000000000000000000000000000000000000000000000000", FormArtifact},
		{"helm-renderer:0.4.1", FormEntityURI},
		{"core/helm-renderer:0.4.1", FormEntityURI},
		{"core/frp/patch:1.0.0", FormEntityURI},
	}
	for _, tc := range cases {
		if got := ClassifyReference(tc.uri); got != tc.want {
			t.Errorf("ClassifyReference(%q) = %d; want %d", tc.uri, got, tc.want)
		}
	}
}

// TestAcceptReferenceForm_EntityOnly proves the entity-only policy rejects the
// two non-entity forms — naming the offending reference and wrapping
// ErrPluginEntityNotFound — while accepting a bare entity URI, including the
// provider/name:version shape.
func TestAcceptReferenceForm_EntityOnly(t *testing.T) {
	rejected := []struct {
		uri     string
		wantSub string
	}{
		{"./plugins/foo.wasm", "filesystem path"},
		{"/abs/foo.wasm", "filesystem path"},
		{"oci://hub.aggrega.ai/plugin/frp/patch:1.0.0", "artifact reference"},
		{"foo.wasm", "artifact reference"},
		{"core/frp/patch@sha256:0000000000000000000000000000000000000000000000000000000000000000", "artifact reference"},
	}
	for _, tc := range rejected {
		err := AcceptReferenceForm(tc.uri, SurfaceEntityOnly)
		if err == nil {
			t.Errorf("AcceptReferenceForm(%q, entity-only) = nil; want a rejection", tc.uri)
			continue
		}
		if !errors.Is(err, ErrPluginEntityNotFound()) {
			t.Errorf("AcceptReferenceForm(%q, entity-only) must wrap ErrPluginEntityNotFound; got %v", tc.uri, err)
		}
		if !strings.Contains(err.Error(), tc.uri) {
			t.Errorf("AcceptReferenceForm(%q, entity-only) must name the offending reference; got %v", tc.uri, err)
		}
		if !strings.Contains(err.Error(), tc.wantSub) {
			t.Errorf("AcceptReferenceForm(%q, entity-only) message must classify the form (%q); got %v", tc.uri, tc.wantSub, err)
		}
	}

	for _, uri := range []string{"helm-renderer:0.4.1", "core/helm-renderer:0.4.1", "core/frp/patch:1.0.0"} {
		if err := AcceptReferenceForm(uri, SurfaceEntityOnly); err != nil {
			t.Errorf("AcceptReferenceForm(%q, entity-only) = %v; a bare entity URI must be accepted", uri, err)
		}
	}
}

// TestAcceptReferenceForm_Permissive proves the permissive policy accepts every
// form — the cross-cutting sockets attach a plugin by path, artifact, or entity
// URI alike.
func TestAcceptReferenceForm_Permissive(t *testing.T) {
	for _, uri := range []string{
		"./plugins/foo.wasm",
		"/abs/foo.wasm",
		"oci://hub.aggrega.ai/plugin/frp/patch:1.0.0",
		"foo.wasm",
		"core/frp/patch@sha256:0000000000000000000000000000000000000000000000000000000000000000",
		"core/helm-renderer:0.4.1",
	} {
		if err := AcceptReferenceForm(uri, SurfacePermissive); err != nil {
			t.Errorf("AcceptReferenceForm(%q, permissive) = %v; every form must be accepted", uri, err)
		}
	}
}
