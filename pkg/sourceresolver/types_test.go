// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package sourceresolver_test

import (
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver"
)

func TestResolvedSourceYAMLRoundTrip(t *testing.T) {
	t.Parallel()

	// Two sub-cases cover the documented OriginalSource shape union:
	// scalar string source (Recipe authoring with `source: "oci://..."`),
	// and structured-object source (Recipe authoring with `source: { kind: ..., url: ..., ... }`).
	t.Run("string source", func(t *testing.T) {
		t.Parallel()
		want := sourceresolver.ResolvedSource{
			Kind:           "helm-oci-chart",
			URL:            "oci://hub.aggrega.ai/charts/mariadb",
			PinnedVersion:  "25.0.8",
			Digest:         "sha256:abcd",
			CachePath:      "/ws/.aggrega/cache/helm-oci-chart/hub.aggrega.ai/charts/mariadb/25.0.8/abcd/blob.tar.gz",
			ExtractedPath:  "/ws/.aggrega/cache/helm-oci-chart/hub.aggrega.ai/charts/mariadb/25.0.8/abcd/extracted/",
			OriginalSource: "oci://hub.aggrega.ai/charts/mariadb:25.0.8",
		}

		out, err := yaml.Marshal(want)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var got sourceresolver.ResolvedSource
		if err := yaml.Unmarshal(out, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !reflect.DeepEqual(want, got) {
			t.Errorf("round-trip mismatch.\n  got:  %+v\n  want: %+v\n  yaml:\n%s", got, want, string(out))
		}
	})

	t.Run("object source", func(t *testing.T) {
		t.Parallel()
		want := sourceresolver.ResolvedSource{
			Kind:          "helm-oci-chart",
			URL:           "oci://hub.aggrega.ai/charts/mariadb",
			PinnedVersion: "25.0.8",
			Digest:        "sha256:abcd",
			CachePath:     "/ws/.aggrega/cache/helm-oci-chart/hub.aggrega.ai/charts/mariadb/25.0.8/abcd/blob.tar.gz",
			ExtractedPath: "/ws/.aggrega/cache/helm-oci-chart/hub.aggrega.ai/charts/mariadb/25.0.8/abcd/extracted/",
			OriginalSource: map[string]any{
				"kind":    "helm-oci-chart",
				"url":     "oci://hub.aggrega.ai/charts",
				"chart":   "mariadb",
				"version": "25.0.8",
			},
		}

		out, err := yaml.Marshal(want)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var got sourceresolver.ResolvedSource
		if err := yaml.Unmarshal(out, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !reflect.DeepEqual(want, got) {
			t.Errorf("round-trip mismatch.\n  got:  %+v\n  want: %+v\n  yaml:\n%s", got, want, string(out))
		}
	})
}
