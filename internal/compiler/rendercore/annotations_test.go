// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package rendercore

import "testing"

func TestImageRegistryFromAnnotations(t *testing.T) {
	t.Parallel()
	if got := ImageRegistryFromAnnotations(nil); got != "" {
		t.Errorf("nil map: want %q, got %q", "", got)
	}
	if got := ImageRegistryFromAnnotations(map[string]string{}); got != "" {
		t.Errorf("empty map: want %q, got %q", "", got)
	}
	if got := ImageRegistryFromAnnotations(map[string]string{AnnotationImageRegistry: "reg.io"}); got != "reg.io" {
		t.Errorf("present: want %q, got %q", "reg.io", got)
	}
}

func TestImagePullSecretFromAnnotations(t *testing.T) {
	t.Parallel()
	if got := ImagePullSecretFromAnnotations(nil); got != "" {
		t.Errorf("nil map: want %q, got %q", "", got)
	}
	if got := ImagePullSecretFromAnnotations(map[string]string{}); got != "" {
		t.Errorf("empty map: want %q, got %q", "", got)
	}
	if got := ImagePullSecretFromAnnotations(map[string]string{AnnotationImagePullSecret: "op-x"}); got != "op-x" {
		t.Errorf("present: want %q, got %q", "op-x", got)
	}
}

func TestApplyImageRegistry(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		ref    string
		prefix string
		want   string
	}{
		{"empty prefix leaves ref unchanged", "postgres:16", "", "postgres:16"},
		{"empty ref unchanged", "", "reg.io", ""},
		{"bare name with tag", "postgres:16", "reg.io", "reg.io/postgres:16"},
		{"bare name no tag", "postgres", "reg.io", "reg.io/postgres"},
		{"namespaced repo, no host", "library/postgres:16", "reg.io", "reg.io/library/postgres:16"},
		{"fully-qualified host stripped", "docker.io/library/postgres:16", "reg.io", "reg.io/library/postgres:16"},
		{"host with port stripped", "localhost:5000/app:1", "reg.io", "reg.io/app:1"},
		{"localhost host stripped", "localhost/app:1", "reg.io", "reg.io/app:1"},
		{"deep repo path under host", "registry.io/a/b/c:1", "reg.io", "reg.io/a/b/c:1"},
		{"digest-pinned bare ref preserved", "postgres@sha256:abc123", "reg.io", "reg.io/postgres@sha256:abc123"},
		{"digest-pinned qualified ref", "docker.io/library/postgres@sha256:abc", "reg.io", "reg.io/library/postgres@sha256:abc"},
		{"prefix trailing slash trimmed", "postgres:16", "reg.io/", "reg.io/postgres:16"},
		{"prefix with path", "postgres:16", "reg.io/mirror", "reg.io/mirror/postgres:16"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ApplyImageRegistry(tc.ref, tc.prefix); got != tc.want {
				t.Errorf("ApplyImageRegistry(%q, %q) = %q, want %q", tc.ref, tc.prefix, got, tc.want)
			}
		})
	}
}
