// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

// The cloudformation importer reads templates at the size CloudFormation
// accepts from a file: up to a megabyte, and as many as five hundred resources.
// The case here runs the real Import over such a template, as the core importer
// the binary carries, under the limits the importer's Plugin entity records.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/embeddedentities"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

const (
	cloudFormationImporter = "importer/cloudformation"

	// cloudFormationResources and minimumCloudFormationTemplateBytes are the
	// size of the template the case imports.
	cloudFormationResources            = 500
	minimumCloudFormationTemplateBytes = 900 << 10

	// The floors of the importer's limits, and the margins its overlay keeps
	// over what the case's template measured: the pages are the smallest
	// memory limit under which the template imports, found by lowering the
	// limit until the guest trapped, and the time is the slowest of the
	// runs, in milliseconds. The runtime reports no peak, so the measured
	// values are recorded here and the limits are held to the margins over them.
	cloudFormationMinimumPages, cloudFormationMinimumTimeoutMs = 2048, 30000
	cloudFormationMeasuredPeakPages                            = 5888
	cloudFormationMeasuredGuestMs                              = 25086
)

// largeCloudFormationTemplate writes a template of cloudFormationResources
// resources and at least minimumCloudFormationTemplateBytes, in YAML with short
// forms: queues, buckets and topics, each carrying many tags and referring to
// the resource before it, so the import resolves a reference per resource.
func largeCloudFormationTemplate() []byte {
	var b strings.Builder
	b.WriteString("AWSTemplateFormatVersion: 2010-09-09\nDescription: A large stack.\nParameters:\n  Env:\n    Type: String\n    Default: prod\nResources:\n")
	kinds := []string{"AWS::SQS::Queue", "AWS::S3::Bucket", "AWS::SNS::Topic"}
	for i := range cloudFormationResources {
		fmt.Fprintf(&b, "  Resource%03d:\n    Type: %s\n", i, kinds[i%len(kinds)])
		if i > 0 {
			fmt.Fprintf(&b, "    DependsOn: Resource%03d\n", i-1)
		}
		b.WriteString("    Properties:\n")
		switch kinds[i%len(kinds)] {
		case "AWS::SQS::Queue":
			fmt.Fprintf(&b, "      QueueName: !Sub \"${Env}-queue-%03d\"\n", i)
		case "AWS::S3::Bucket":
			fmt.Fprintf(&b, "      BucketName: !Sub \"${Env}-bucket-%03d\"\n", i)
		default:
			fmt.Fprintf(&b, "      TopicName: !Sub \"${Env}-topic-%03d\"\n", i)
		}
		b.WriteString("      Tags:\n")
		for k := 0; b.Len() < minimumCloudFormationTemplateBytes*(i+1)/cloudFormationResources || k < 3; k++ {
			prev := max(i-1, 0)
			fmt.Fprintf(&b, "        - Key: tag-%03d-%03d\n          Value: !Sub \"${Env}-%03d-${Resource%03d}-padding-padding-padding\"\n", i, k, k, prev)
		}
	}
	return []byte(b.String())
}

// TestCloudFormationImportLargeTemplateWithinLimits imports a template of 500
// resources and at least 900 kilobytes, each referring to the one before, through the real
// Import as the cloudformation core importer, naming no format, so the template is read
// by the host's recognition and again by the importer. It halts nowhere and
// drafts an Aggregate, under the limits the carried entity records, which are
// the ones the hub's overlay for the importer states.
//
// The carried set is process-wide, so the case is not parallel.
func TestCloudFormationImportLargeTemplateWithinLimits(t *testing.T) {
	embeddedentities.Require(t)
	importopstest.CarryImporters(t, cloudFormationImporter)

	var carried []byte
	for _, p := range embedded.PublishedPlugins() {
		if p.URI == cloudFormationImporter {
			carried = p.Entity
		}
	}
	var entity types.Plugin
	if err := yaml.Unmarshal(carried, &entity); err != nil || entity.Spec.Runtime == nil {
		t.Fatalf("the entity %s carries records no runtime limits (%v)", cloudFormationImporter, err)
	}
	var overlay types.Plugin
	raw, err := os.ReadFile(wasmtest.SiblingPath(t, "aggrega-hub", "plugin-overlays", "importer", "cloudformation.yaml"))
	if err != nil {
		t.Fatalf("read the overlay: %v", err)
	}
	if err := yaml.Unmarshal(raw, &overlay); err != nil || overlay.Spec.Runtime == nil {
		t.Fatalf("the overlay records no runtime limits (%v)", err)
	}
	if got, want := *entity.Spec.Runtime, *overlay.Spec.Runtime; got.MaxMemoryPages != want.MaxMemoryPages ||
		got.TimeoutMs != want.TimeoutMs {
		t.Fatalf("the carried entity records %d pages and %d ms; the overlay states %d pages and %d ms",
			got.MaxMemoryPages, got.TimeoutMs, want.MaxMemoryPages, want.TimeoutMs)
	}

	limits := *overlay.Spec.Runtime
	if limits.MaxMemoryPages < cloudFormationMinimumPages || limits.TimeoutMs < cloudFormationMinimumTimeoutMs {
		t.Errorf("the overlay states %d pages and %d ms; the floors are %d pages and %d ms",
			limits.MaxMemoryPages, limits.TimeoutMs, cloudFormationMinimumPages, cloudFormationMinimumTimeoutMs)
	}
	if float64(limits.MaxMemoryPages) < 1.25*cloudFormationMeasuredPeakPages {
		t.Errorf("the overlay states %d pages, under 1.25 times the %d the template measured",
			limits.MaxMemoryPages, cloudFormationMeasuredPeakPages)
	}
	if limits.TimeoutMs < 4*cloudFormationMeasuredGuestMs {
		t.Errorf("the overlay states %d ms, under 4 times the %d ms the template measured",
			limits.TimeoutMs, cloudFormationMeasuredGuestMs)
	}

	template := largeCloudFormationTemplate()
	if len(template) < minimumCloudFormationTemplateBytes {
		t.Fatalf("the template is %d bytes, below the %d the case imports", len(template), minimumCloudFormationTemplateBytes)
	}

	wsDir := aWorkspaceDir(t)
	if err := os.MkdirAll(filepath.Join(wsDir, "catalog"), 0o755); err != nil {
		t.Fatalf("create the catalog folder: %v", err)
	}
	importopstest.WriteOfflineConfiguration(t, wsDir)
	rw := resolveWorkspaceAt(t, wsDir)
	src := writeFormatSource(t, "template.yaml", string(template))

	started := time.Now()
	res, err := Import(context.Background(), rw, Options{
		SourcePath: src.path, SourceName: src.name, OutputDir: filepath.Join(t.TempDir(), "drafts"),
		ResolvePlugin: (&recordingCascade{}).resolve,
	})
	if took := time.Since(started); took.Milliseconds() >= int64(limits.TimeoutMs) {
		t.Errorf("the import took %v, not within the importer's %d ms", took, limits.TimeoutMs)
	}
	if err != nil {
		t.Fatalf("the import was refused: %v", err)
	}
	if res.Halted() {
		t.Fatalf("the importer halted: %+v", res.Diagnostics)
	}
	if res.Format != "cloudformation" {
		t.Errorf("the template was read as %q, want cloudformation", res.Format)
	}
	if !slices.ContainsFunc(res.Files, func(f abi.ScaffoldFile) bool { return filepath.Base(f.Path) == "aggregate.yaml" }) {
		t.Errorf("the import drafted no Aggregate; its files are %d, its diagnostics %+v", len(res.Files), res.Diagnostics)
	}
}
