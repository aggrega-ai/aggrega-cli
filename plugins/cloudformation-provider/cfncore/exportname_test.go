// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cfncore

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestExportName asserts the deterministic export-name scheme for representative
// inputs: the aggrega- prefix, the four ordered segments, and per-segment
// sanitisation of characters a CloudFormation export name does not admit.
func TestExportName(t *testing.T) {
	cases := []struct {
		name                                      string
		release, boundary, component, providesKey string
		want                                      string
	}{
		{
			name:        "plain segments",
			release:     "prod",
			boundary:    "api",
			component:   "orders-db",
			providesKey: "host",
			want:        "aggrega-prod-api-orders-db-host",
		},
		{
			name:        "boundary path slash sanitised to hyphen",
			release:     "prod",
			boundary:    "prod/api",
			component:   "orders-db",
			providesKey: "host",
			want:        "aggrega-prod-prod-api-orders-db-host",
		},
		{
			name:        "mixed invalid characters mapped to hyphen",
			release:     "staging",
			boundary:    "eu_west/1",
			component:   "cache.primary",
			providesKey: "port",
			want:        "aggrega-staging-eu-west-1-cache-primary-port",
		},
		{
			name:        "empty release rendered as placeholder",
			release:     "",
			boundary:    "net",
			component:   "db",
			providesKey: "endpoint",
			want:        "aggrega-none-net-db-endpoint",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExportName(tc.release, tc.boundary, tc.component, tc.providesKey)
			t.Logf("ExportName(%q, %q, %q, %q) = %q", tc.release, tc.boundary, tc.component, tc.providesKey, got)
			if got != tc.want {
				t.Fatalf("ExportName = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestExportNameDeterministic asserts the export name is byte-stable across
// repeated calls with identical inputs (Deterministic Output invariant).
func TestExportNameDeterministic(t *testing.T) {
	first := ExportName("prod", "prod/api", "orders-db", "host")
	for i := 0; i < 8; i++ {
		if again := ExportName("prod", "prod/api", "orders-db", "host"); again != first {
			t.Fatalf("ExportName not stable on run %d: %q != %q", i, again, first)
		}
	}
}

// TestReadCloudTargetAnnotationsPresent asserts the reader extracts each of the
// three cloud-target annotations from the node-scoped Release bucket as opaque
// strings.
func TestReadCloudTargetAnnotationsPresent(t *testing.T) {
	var rel types.OrderedMap[string]
	rel.Set("aggrega.ai/aws-account", "123456789012")
	rel.Set("aggrega.ai/aws-region", "eu-west-1")
	rel.Set("aggrega.ai/aws-assume-role-arn", "arn:aws:iam::123456789012:role/cross-stack-reader")

	ctx := renderctx.RenderContext{
		Annotations: renderctx.RenderAnnotations{Release: rel},
	}

	got := ReadCloudTargetAnnotations(ctx)
	if got.AWSAccount != "123456789012" {
		t.Fatalf("AWSAccount = %q, want 123456789012", got.AWSAccount)
	}
	if got.AWSRegion != "eu-west-1" {
		t.Fatalf("AWSRegion = %q, want eu-west-1", got.AWSRegion)
	}
	if got.AWSAssumeRoleARN != "arn:aws:iam::123456789012:role/cross-stack-reader" {
		t.Fatalf("AWSAssumeRoleARN = %q, want the role arn", got.AWSAssumeRoleARN)
	}
}

// TestReadCloudTargetAnnotationsAbsent asserts an absent annotation yields the
// empty string on every field.
func TestReadCloudTargetAnnotationsAbsent(t *testing.T) {
	got := ReadCloudTargetAnnotations(renderctx.RenderContext{})
	if got.AWSAccount != "" || got.AWSRegion != "" || got.AWSAssumeRoleARN != "" {
		t.Fatalf("expected empty strings for an empty render context, got %+v", got)
	}
}

// TestReadCloudTargetAnnotationsComponentOverBoundary asserts the node-scoped
// Release bucket wins over the boundary bucket for the same key, and a key
// present only on the boundary is read from there.
func TestReadCloudTargetAnnotationsComponentOverBoundary(t *testing.T) {
	var rel types.OrderedMap[string]
	rel.Set("aggrega.ai/aws-region", "us-west-2")

	var boundary types.OrderedMap[string]
	boundary.Set("aggrega.ai/aws-region", "us-east-1")
	boundary.Set("aggrega.ai/aws-account", "999988887777")

	ctx := renderctx.RenderContext{
		Annotations: renderctx.RenderAnnotations{Release: rel},
		Boundary:    renderctx.BoundaryContext{Annotations: boundary},
	}

	got := ReadCloudTargetAnnotations(ctx)
	if got.AWSRegion != "us-west-2" {
		t.Fatalf("AWSRegion = %q, want the node-scoped value us-west-2 (component over boundary)", got.AWSRegion)
	}
	if got.AWSAccount != "999988887777" {
		t.Fatalf("AWSAccount = %q, want the boundary-only value 999988887777", got.AWSAccount)
	}
}
