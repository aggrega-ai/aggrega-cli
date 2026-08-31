// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cfncore

import (
	"encoding/json"
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
)

// ProvenanceMetadataKey is the key a resource's Metadata attribute carries the
// build provenance under.
//
// It is one key rather than the provenance entries written flat, because the
// Metadata attribute is a shared space: the template language reserves its own
// names there (the AWS:: family) and other tools write their own, so a block
// under one reserved-prefix key collides with none of them and a merge into an
// author's own Metadata touches exactly one name. The `aggrega.ai/` prefix is the
// reserved platform-injection keyspace, which is what makes the key unavailable
// to an author and therefore safe to claim.
const ProvenanceMetadataKey = "aggrega.ai/provenance"

// ProvenanceSet folds the two host-computed provenance halves a projection entry
// carries into the single set a CloudFormation resource carries.
//
// A resource's Metadata attribute is arbitrary JSON: it constrains neither a
// value's character set nor its length, and CloudFormation has no second metadata
// surface to split across. So the Kubernetes label/annotation split has nothing to
// key on here and the whole set travels together, minus the keys only a Kubernetes
// runtime consumes. The fold is the shared one, so a key added to either shared
// builder reaches this target with no change here.
func ProvenanceSet(labels, annotations map[string]string) map[string]string {
	return rendercore.FoldProvenanceSet(labels, annotations)
}

// ProvenanceSetFromNodeContext derives the same set from a node's render context,
// for the sourced path — where the payload carries the node's own provenance
// rather than a per-entry pair of halves computed for an artifact.
//
// It reads the shared builders rather than the wire projection's fields directly,
// so the set a sourced template carries and the set an artifact fragment carries
// are the same set from the same source, and neither can drift from what a
// Kubernetes-shaped provider stamps for the same node.
func ProvenanceSetFromNodeContext(nodeCtx rendercore.NodeRenderContext) map[string]string {
	return ProvenanceSet(
		rendercore.BuildAggregaProvenanceLabels(nodeCtx),
		rendercore.BuildAggregaProvenanceAnnotations(nodeCtx, rendercore.ProvenanceRecipeURIFromNodeCtx(nodeCtx)),
	)
}

// StampResourceProvenance stamps a provenance set onto every resource of one
// already-normalized CloudFormation template and returns the placed bytes,
// together with an advisory naming the template when it holds no site to stamp.
//
// Placement is each resource's own Metadata attribute, under
// [ProvenanceMetadataKey]. The template language grants that attribute to every
// resource whatever its type — it is a resource attribute, alongside DependsOn and
// Condition, rather than a property a resource type declares — so one placement
// rule reaches every resource and no type is skipped. Resource tags are the other
// candidate and are not equivalent: Tags is a per-type property, present on some
// resource types and absent on others, and carried as a list of key/value objects
// on most of the types that have it and as a map on the rest. A template naming a
// property its resource type does not declare is refused whole at deploy time, so
// a tag-based stamp would have to be right about which of three answers applies to
// each type, from a table nobody can complete: the type set is open — a custom or
// registry resource type is outside the published specification entirely — and it
// grows without this core being told. The Metadata attribute needs no such table.
// A resource's Metadata is also readable back from the deployed stack rather than
// only from the file, so an operator holding the stack and not the template can
// still ask which component produced a resource.
//
// The stamp merges into whatever Metadata the author wrote rather than replacing
// it: an author's own Metadata names survive alongside the provenance block, and
// only [ProvenanceMetadataKey] itself is written. A resource whose Metadata is
// present but is not a mapping is left alone and named in the advisory, because
// rewriting authored content into a shape this placer finds convenient is not
// this core's to do.
//
// The two outcomes are exhaustive and neither is silent. A template declaring at
// least one resource comes back stamped with an empty advisory. A template
// declaring none — a contributor of Parameters, Mappings or Conditions alone —
// comes back unchanged with an advisory naming it, since a resource's Metadata is
// where CloudFormation provenance lands and such a template has no resource. The
// caller never receives "unstamped" and "nothing to report" together.
//
// An empty provenance set stamps nothing and reports nothing: there is no
// provenance to place, which is not the same condition as having nowhere to place
// it.
func StampResourceProvenance(name string, template []byte, provenance map[string]string) (body []byte, warning string, err error) {
	if len(provenance) == 0 {
		return template, "", nil
	}
	v, err := decodeToValue(template)
	if err != nil {
		return nil, "", fmt.Errorf("cloudformation-provider: stamp provenance into %q: %w", name, err)
	}
	if v == nil {
		return template, "", nil
	}
	doc, ok := v.(map[string]any)
	if !ok {
		return nil, "", fmt.Errorf("cloudformation-provider: stamp provenance into %q: template is not a mapping", name)
	}

	resources, ok := doc[sectionResources].(map[string]any)
	if !ok || len(resources) == 0 {
		return template, provenanceNoResourceWarning(name), nil
	}

	// The provenance block is rebuilt per resource rather than shared, so no two
	// resources hold one map: the assembled stack's own merge compares resource
	// bodies, and a shared sub-map would make two bodies compare equal by identity
	// where the template language sees two independent documents.
	unstampable := make([]string, 0)
	for _, id := range sortedKeys(resources) {
		resource, ok := resources[id].(map[string]any)
		if !ok {
			unstampable = append(unstampable, id)
			continue
		}
		metadata, present := resource[sectionMetadata]
		if !present {
			resource[sectionMetadata] = map[string]any{ProvenanceMetadataKey: provenanceBlock(provenance)}
			continue
		}
		authored, ok := metadata.(map[string]any)
		if !ok {
			unstampable = append(unstampable, id)
			continue
		}
		authored[ProvenanceMetadataKey] = provenanceBlock(provenance)
	}

	out, err := json.Marshal(doc)
	if err != nil {
		return nil, "", fmt.Errorf("cloudformation-provider: stamp provenance into %q: %w", name, err)
	}
	if len(unstampable) > 0 {
		return out, provenanceUnstampableResourceWarning(name, unstampable), nil
	}
	return out, "", nil
}

// StampSourcedProvenance stamps a node's provenance onto every CloudFormation
// template of a sourced source's already-normalized files, preserving the packed
// order and each file's path, and returns one advisory per template that held no
// site to stamp.
//
// It skips the same non-template content [NormalizeSourced] vendors verbatim, for
// the same reason: a directory source's README or parameters file declares no
// resource, so it is not a template that failed to carry provenance but a file
// that was never asked to. A single-file source's lone template is stamped whole.
func StampSourcedProvenance(files []SourcedFile, rootIsDir bool, provenance map[string]string) ([]SourcedFile, []string, error) {
	out := make([]SourcedFile, 0, len(files))
	var warnings []string
	for _, f := range files {
		if rootIsDir && !isTemplateFile(f.Path) {
			out = append(out, f)
			continue
		}
		body, warning, err := StampResourceProvenance(f.Path, f.Content, provenance)
		if err != nil {
			return nil, nil, err
		}
		if warning != "" {
			warnings = append(warnings, warning)
		}
		out = append(out, SourcedFile{Path: f.Path, Content: body})
	}
	return out, warnings, nil
}

// provenanceBlock copies a provenance set into the value tree the template
// serializes from. A fresh map per call keeps each resource's block its own value,
// and the string keys serialize in sorted order, so the stamped bytes are a
// function of the set alone.
func provenanceBlock(provenance map[string]string) map[string]any {
	block := make(map[string]any, len(provenance))
	for k, v := range provenance {
		block[k] = v
	}
	return block
}

// provenanceNoResourceWarning is the user-facing message emitted when a
// CloudFormation template declares no resource, so the build provenance has
// nowhere to land and the template is emitted unchanged. It names the template,
// says where CloudFormation provenance lands, and says what the template carries
// instead, so a reader diffing output can tell an unstamped template from a missed
// one.
func provenanceNoResourceWarning(name string) string {
	return fmt.Sprintf(
		"template %q: skipped provenance injection — the template declares no resource. CloudFormation carries build provenance on each resource's Metadata attribute, so a template contributing only Parameters, Mappings, Conditions or Rules carries none; the resources this component contributes elsewhere carry the full set.",
		name,
	)
}

// provenanceUnstampableResourceWarning is the user-facing message emitted when a
// resource is declared in a shape the provenance stamp cannot reach — the resource
// body or its Metadata attribute written as something other than a mapping. The
// resource is emitted exactly as authored and named here, because a value of that
// shape is content this placer has no reading of and rewriting it is not the
// compiler's to do.
func provenanceUnstampableResourceWarning(name string, ids []string) string {
	return fmt.Sprintf(
		"template %q: skipped provenance injection for %v — a resource carries build provenance under its Metadata attribute, and these are declared with a body or a Metadata attribute that is not a mapping. They are emitted exactly as authored; write each as a mapping for it to carry provenance.",
		name, ids,
	)
}
