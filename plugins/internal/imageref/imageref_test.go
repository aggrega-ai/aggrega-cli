// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package imageref

import "testing"

// TestRepositoryReadsAReferenceAsDockerDoes reads the repository a reference
// names, its tag and its digest left out and its registry left out where it is
// Docker Hub's, and refuses text that is no reference.
func TestRepositoryReadsAReferenceAsDockerDoes(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		ref, want string
		ok        bool
	}{
		{"postgres", "postgres", true},
		{"postgres:16", "postgres", true},
		{"docker.io/library/postgres:16", "postgres", true},
		{"library/redis", "redis", true},
		{"index.docker.io/grafana/loki:3.6.7", "grafana/loki", true},
		{"ghcr.io/example/shop-api:1.4.2", "ghcr.io/example/shop-api", true},
		{"localhost:5000/app:dev", "localhost:5000/app", true},
		{"cr.agentgateway.dev/agentgateway:v1.6.0@sha256:9d3e", "cr.agentgateway.dev/agentgateway", true},
		{"redis:", "redis", true},
		{"", "", false},
		{"${IMAGE:-postgres:16}", "", false},
		{"Postgres", "", false},
		{"nginx-", "", false},
		{"/postgres", "", false},
	} {
		got, ok := Repository(row.ref)
		if got != row.want || ok != row.ok {
			t.Errorf("Repository(%q) = %q, %v; want %q, %v", row.ref, got, ok, row.want, row.ok)
		}
	}
}

// TestReferenceReadsTheTagAReferenceNames reads, beside the repository
// Repository reads, the tag a reference names: none where it names none or its
// tag is empty, and none where a digest pins it, whatever its tag says; a port
// of a registry is no tag.
func TestReferenceReadsTheTagAReferenceNames(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		ref, repository, tag string
	}{
		{"postgres:16", "postgres", "16"},
		{"redis:7.4-alpine", "redis", "7.4-alpine"},
		{"postgres", "postgres", ""},
		{"redis:", "redis", ""},
		{"localhost:5000/app", "localhost:5000/app", ""},
		{"localhost:5000/app:dev", "localhost:5000/app", "dev"},
		{"cr.agentgateway.dev/agentgateway:v1.6.0@sha256:9d3e", "cr.agentgateway.dev/agentgateway", ""},
	} {
		repository, tag, ok := Reference(row.ref)
		if !ok || repository != row.repository || tag != row.tag {
			t.Errorf("Reference(%q) = %q, %q, %v; want %q, %q", row.ref, repository, tag, ok, row.repository, row.tag)
		}
	}
}
