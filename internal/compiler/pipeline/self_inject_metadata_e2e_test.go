// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pipeline_test

import (
	"strings"
	"testing"
)

// TestASelfInjectValueTemplateReadsTheReleaseMetadata builds a component whose
// own injection block reads each .metadata field, through the real pipeline, and
// asserts every delivered value is the Release's: its name, version and
// environment, and the Aggregate's name.
func TestASelfInjectValueTemplateReadsTheReleaseMetadata(t *testing.T) {
	t.Parallel()

	artifacts, err := buildDeliveryOrigin(t, "self-inject-metadata")
	if err != nil {
		t.Fatalf("a self-inject template reading the release metadata must build: %v", err)
	}
	var all strings.Builder
	for _, body := range artifacts {
		all.Write(body)
	}
	out := all.String()
	for _, want := range []string{
		"AGGREGATE_NAME=aggregate-self-inject-metadata;",
		"ENVIRONMENT=staging;",
		"RELEASE_NAME=self-inject-metadata-release;",
		"RELEASE_VERSION=3.1.4;",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the output does not deliver %q:\n%s", want, out)
		}
	}
}
