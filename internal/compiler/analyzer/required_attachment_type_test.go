// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// mandateGraph builds a one-node graph for component "web" whose catalog
// annotations carry the supplied key/value pairs, with the named attachment
// types present on the node's attachment bucket. Every attachment family —
// operational, credential, identity — shares that one bucket, so a fixture
// needs no family discriminator.
func mandateGraph(attachedTypes []string, annotationPairs ...string) *types.IRGraph {
	ann := types.OrderedMap[string]{}
	for i := 0; i+1 < len(annotationPairs); i += 2 {
		ann.Set(annotationPairs[i], annotationPairs[i+1])
	}
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("web", types.IRNode{
		Component:          "example/compute/service",
		CatalogAnnotations: ann,
		SourceFile:         "aggregate.yaml",
	})
	spec := types.IRSpec{Nodes: nodes}

	entries := types.OrderedMap[types.IRFeatureEntry]{}
	for i, ftype := range attachedTypes {
		entries.Set(instanceName(i), types.IRFeatureEntry{Type: ftype})
	}
	per := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	per.Set("web", entries)
	spec.FeatureValuesMapping = per

	return &types.IRGraph{Spec: spec}
}

func instanceName(i int) string {
	return string(rune('a' + i))
}

const monitoringMandate = "features.aggrega.ai/requiredType.monitoring"

// A mandate satisfied by a FREE attachment — an attachment under a name no
// slot declares, which is the case the mandate exists for — passes clean.
func TestRequiredAttachmentTypes_SatisfiedByFreeAttachment(t *testing.T) {
	t.Parallel()
	graph := mandateGraph([]string{"core/prometheus-exporter"},
		monitoringMandate, "core/prometheus-exporter")
	errs, err := validateRequiredAttachmentTypes(context.Background(), graph)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(errs) != 0 {
		t.Fatalf("want no violations, got %v", errs)
	}
}

// A credential attachment satisfies a mandate too: presence is presence, and
// the rule does not care which family the attached type belongs to.
func TestRequiredAttachmentTypes_SatisfiedByCredentialAttachment(t *testing.T) {
	t.Parallel()
	graph := mandateGraph([]string{"core/referenced-secret"},
		"features.aggrega.ai/requiredType.credential", "core/referenced-secret")
	errs, err := validateRequiredAttachmentTypes(context.Background(), graph)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(errs) != 0 {
		t.Fatalf("want no violations, got %v", errs)
	}
}

func TestRequiredAttachmentTypes_UnmetHalts(t *testing.T) {
	t.Parallel()
	graph := mandateGraph([]string{"core/public-ingress"},
		monitoringMandate, "core/prometheus-exporter")
	errs, err := validateRequiredAttachmentTypes(context.Background(), graph)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(errs) != 1 {
		t.Fatalf("want 1 violation, got %d: %v", len(errs), errs)
	}
	if !errors.Is(errs[0].Sentinel, resolver.ErrMissingRequiredFeature) {
		t.Errorf("want ErrMissingRequiredFeature, got %v", errs[0].Sentinel)
	}
	// The diagnostic names the requirement's label AND the required type.
	if !strings.Contains(errs[0].Message, "monitoring") {
		t.Errorf("message must name the requirement label: %q", errs[0].Message)
	}
	if !strings.Contains(errs[0].Message, "core/prometheus-exporter") {
		t.Errorf("message must name the required type: %q", errs[0].Message)
	}
	if errs[0].Field != ".spec.components.web" {
		t.Errorf("want the component-instance locator, got %q", errs[0].Field)
	}
	if errs[0].OriginFile != "aggregate.yaml" {
		t.Errorf("want the node's source file, got %q", errs[0].OriginFile)
	}
	if errs[0].Kind != AnalysisErrorAttachmentRequirement {
		t.Errorf("want the attachment-requirement kind, got %q", errs[0].Kind)
	}
}

// A mandate with no attachments at all on the node halts — the empty-bucket
// path must not skip the check.
func TestRequiredAttachmentTypes_NoAttachmentsAtAllHalts(t *testing.T) {
	t.Parallel()
	graph := mandateGraph(nil, monitoringMandate, "core/prometheus-exporter")
	errs, _ := validateRequiredAttachmentTypes(context.Background(), graph)
	if len(errs) != 1 {
		t.Fatalf("want 1 violation, got %d: %v", len(errs), errs)
	}
}

// A malformed declaration reports as malformed and its requirement is NOT
// tested, so a typo can never read as a satisfied requirement. The two
// outcomes carry different sentinels.
func TestRequiredAttachmentTypes_MalformedIsDistinct(t *testing.T) {
	t.Parallel()
	graph := mandateGraph([]string{"core/prometheus-exporter"},
		monitoringMandate, "prometheus-exporter")
	errs, _ := validateRequiredAttachmentTypes(context.Background(), graph)
	if len(errs) != 1 {
		t.Fatalf("want 1 violation, got %d: %v", len(errs), errs)
	}
	if !errors.Is(errs[0].Sentinel, ErrMalformedAttachmentRequirement) {
		t.Errorf("want ErrMalformedAttachmentRequirement, got %v", errs[0].Sentinel)
	}
	if errors.Is(errs[0].Sentinel, resolver.ErrMissingRequiredFeature) {
		t.Error("a malformed declaration must not report as a missing attachment")
	}
	if !strings.Contains(errs[0].Message, "malformed") {
		t.Errorf("message must say the declaration is malformed: %q", errs[0].Message)
	}
	// A malformed declaration is a catalog-authoring fault, not a developer one.
	if got := errs[0].ToCompilerError().Category; string(got) != "CATALOG_ERROR" {
		t.Errorf("want CATALOG_ERROR category, got %q", got)
	}
}

// A labelless key is reported, not silently dropped — the hazard the shape
// check exists to close.
func TestRequiredAttachmentTypes_LabellessKeyReported(t *testing.T) {
	t.Parallel()
	graph := mandateGraph([]string{"core/prometheus-exporter"},
		"features.aggrega.ai/requiredType", "core/prometheus-exporter")
	errs, _ := validateRequiredAttachmentTypes(context.Background(), graph)
	if len(errs) != 1 {
		t.Fatalf("want 1 violation, got %d: %v", len(errs), errs)
	}
	if !errors.Is(errs[0].Sentinel, ErrMalformedAttachmentRequirement) {
		t.Errorf("want ErrMalformedAttachmentRequirement, got %v", errs[0].Sentinel)
	}
}

// The locator has to read the synthesized-provenance flag, not the path
// string. Normalization folds an Aggregate's top-level components into a
// synthesized `default` subsystem, so a root component arrives carrying a
// subsystem path it was never authored under; treating that as nesting yields
// an empty locator and the diagnostic lands at the head of the file instead of
// on the component key.
func TestRequiredAttachmentTypes_LocatorElidesSynthesizedSubsystem(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		subsystemPath string
		synthesized   bool
		want          string
	}{
		{"root, unnormalized", "", false, ".spec.components.web"},
		{"root, folded into the synthesized default", "default", true, ".spec.components.web"},
		{"inside a subsystem the author declared", "svc", false, ""},
		{"inside a declared subsystem named default", "default", false, ""},
	}
	for _, c := range cases {
		if got := componentInstanceFieldPath("web", c.subsystemPath, c.synthesized); got != c.want {
			t.Errorf("%s: locator = %q; want %q", c.name, got, c.want)
		}
	}
}

func TestRequiredAttachmentTypes_NoMandateNoViolation(t *testing.T) {
	t.Parallel()
	graph := mandateGraph(nil, "component.aggrega.ai/type", "server")
	errs, _ := validateRequiredAttachmentTypes(context.Background(), graph)
	if len(errs) != 0 {
		t.Fatalf("want no violations, got %v", errs)
	}
}

// Multiple mandates on one component each evaluate independently, in the
// annotation map's author order.
func TestRequiredAttachmentTypes_MultipleMandatesDeterministicOrder(t *testing.T) {
	t.Parallel()
	graph := mandateGraph(nil,
		monitoringMandate, "core/prometheus-exporter",
		"features.aggrega.ai/requiredType.tracing", "core/context-propagation")
	errs, _ := validateRequiredAttachmentTypes(context.Background(), graph)
	if len(errs) != 2 {
		t.Fatalf("want 2 violations, got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Message, "monitoring") || !strings.Contains(errs[1].Message, "tracing") {
		t.Errorf("violations must follow annotation author order: %q then %q", errs[0].Message, errs[1].Message)
	}
}

// AnalyzeContext wires the pass in, so a mandate violation reaches the
// analyzer's structured Errors slice the build gates on.
func TestAnalyzeContext_SurfacesMandateViolation(t *testing.T) {
	t.Parallel()
	graph := mandateGraph(nil, monitoringMandate, "core/prometheus-exporter")
	result, err := AnalyzeContext(context.Background(), graph, resolver.CatalogLookup{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var found bool
	for _, e := range result.Errors {
		if errors.Is(e.Sentinel, resolver.ErrMissingRequiredFeature) {
			found = true
			if !strings.HasPrefix(e.Error(), "web: ") {
				t.Errorf("rendered violation must name the node first: %q", e.Error())
			}
		}
	}
	if !found {
		t.Fatal("AnalyzeContext must surface the mandate violation")
	}
}
