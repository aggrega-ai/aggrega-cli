// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package sourcekind

import (
	"slices"
	"testing"
)

func TestOCIArtifactRows(t *testing.T) {
	t.Parallel()
	want := map[string]OCIArtifact{
		BicepModuleOCI: {
			ContentMediaType: "application/vnd.ms.bicep.module.layer.v1+json",
			Layout:           LayoutDocument,
			DocumentName:     "main.json",
		},
		TerraformModuleOCI: {
			ContentMediaType:  "archive/zip",
			Layout:            LayoutZip,
			TakesModuleSubdir: true,
		},
	}
	for kind, row := range want {
		got, ok := OCIArtifactOf(kind)
		if !ok || got != row {
			t.Errorf("OCIArtifactOf(%q) = %+v, %v; want %+v, true", kind, got, ok, row)
		}
	}
	for _, kind := range []string{HelmOCIChart, GitRepository, "nosuch"} {
		if got, ok := OCIArtifactOf(kind); ok {
			t.Errorf("OCIArtifactOf(%q) = %+v, true; want no row", kind, got)
		}
	}
	if LayoutTarGz != 0 {
		t.Errorf("LayoutTarGz = %d; want the zero value", LayoutTarGz)
	}
}

func TestOCIDistributed(t *testing.T) {
	t.Parallel()
	for kind, want := range map[string]bool{
		HelmOCIChart:         true,
		BicepModuleOCI:       true,
		TerraformModuleOCI:   true,
		HelmHTTPSChart:       false,
		ComposeHTTPSFragment: false,
		GitRepository:        false,
		"":                   false,
	} {
		if got := OCIDistributed(kind); got != want {
			t.Errorf("OCIDistributed(%q) = %v; want %v", kind, got, want)
		}
	}
}

func TestOCIModuleKindsIsSortedAndACopy(t *testing.T) {
	t.Parallel()
	got := OCIModuleKinds()
	if want := []string{BicepModuleOCI, TerraformModuleOCI}; !slices.Equal(got, want) {
		t.Fatalf("OCIModuleKinds() = %v; want %v", got, want)
	}
	got[0] = "changed"
	if again := OCIModuleKinds(); again[0] != BicepModuleOCI {
		t.Errorf("mutating the result changed the next answer: %v", again)
	}
}
