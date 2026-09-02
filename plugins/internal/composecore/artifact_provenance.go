// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Provenance placement for compose artifact fragments — the compose-shaped
// counterpart to the Kubernetes-manifest placer in the shared render core.
//
// A compose fragment's provenance site is each service's own `labels:` block,
// not a document-level metadata block, so the two placers differ in exactly one
// thing: which node they locate to merge into. The merge itself, the
// alphabetical append order, the overwrite-on-collision rule and the refusal to
// discard a `labels:` block written in a shape it cannot merge into all come
// from the one shared primitive, so a fragment's service and a synthesized
// service cannot drift apart on any of them.
//
// This file carries no build tag: it compiles for the host and for the wasip1
// guest alike, so it imports no host-only capability (no filesystem, no
// network).

package composecore

import (
	"bytes"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// SpliceArtifactServiceProvenance stamps the build provenance the host computed
// for a node into a compose artifact fragment, returning the placed bytes. It is
// the placer the compose provider guest calls once per artifact entry: the guest
// supplies the entry's already-template-evaluated bytes and the host-computed
// label and annotation sets, and receives the stamped body, an optional warning
// to raise as a diagnostic, and a hard error.
//
// Placement. Compose label values are unconstrained and compose has no
// annotation concept, so the two host-computed halves fold into one set and land
// together on every service the fragment declares — the same set, from the same
// fold, that a synthesized or source-rendered service carries. A fragment
// declaring several services stamps all of them; a stream carrying several
// documents is walked in full, so no document is stamped while another is
// skipped unnoticed.
//
// The two outcomes are exhaustive, and neither is silent. When the fragment
// declares at least one service, the placed bytes carry the provenance and the
// warning is empty. When it declares none — a fragment contributing only
// top-level networks, volumes or configs — the bytes go back unchanged and the
// warning names the artifact, which the guest raises as the
// artifact-provenance-skipped diagnostic. Compose stamps provenance on service
// labels, so a fragment with no service has no site for it, and saying so is the
// only honest report available; the caller never receives "unstamped" and
// "nothing to report" together.
//
// A fragment whose bytes do not parse as YAML is a hard CATALOG_ERROR. The
// entry's content reached here already evaluated against Aggrega's own default
// delimiter pair, so nothing downstream is waiting to rewrite it and a
// non-parsing body is an authoring fault rather than a shape to pass through.
//
// indent is the YAML indentation width the re-marshal uses. Empty content is
// returned unchanged with no warning.
func SpliceArtifactServiceProvenance(name string, rendered []byte, labels, annotations map[string]string, indent int) (body []byte, warning string, err error) {
	if strings.TrimSpace(string(rendered)) == "" {
		return rendered, "", nil
	}

	docs, decErr := rendercore.DecodeAllYAMLDocs(string(rendered))
	if decErr != nil {
		return nil, "", cerrors.Wrap(decErr, cerrors.CatalogError, cerrors.SourceLocation{File: name},
			fmt.Sprintf("compose: artifact %q template output is not valid YAML", name))
	}

	provenance := foldComposeProvenanceSet(labels, annotations)
	stamped := 0
	for _, doc := range docs {
		n, stampErr := stampDocServiceProvenance(name, doc, provenance)
		if stampErr != nil {
			return nil, "", stampErr
		}
		stamped += n
	}
	if stamped == 0 {
		return rendered, artifactServiceProvenanceSkipWarning(name), nil
	}

	var buf bytes.Buffer
	for i, doc := range docs {
		if doc.Kind == 0 {
			continue
		}
		b, mErr := rendercore.MarshalYAMLIndent(doc, indent)
		if mErr != nil {
			return nil, "", cerrors.Wrap(mErr, cerrors.TranslationError, cerrors.SourceLocation{File: name},
				fmt.Sprintf("compose: artifact %q: marshal stamped fragment", name))
		}
		if i > 0 {
			buf.WriteString("---\n")
		}
		buf.Write(b)
	}
	return buf.Bytes(), "", nil
}

// stampDocServiceProvenance merges provenance into every service the document
// declares and reports how many it stamped. A document that is not a mapping, or
// carries no `services:` mapping, stamps nothing and reports zero — the caller
// turns a zero across the whole stream into the named advisory.
//
// A service entry whose body is not a mapping is left alone rather than
// rewritten: the compiler does not mutate authored content into a shape it finds
// convenient, and a service that is not a mapping is content this placer has no
// reading of. A service whose `labels:` exists in a shape provenance cannot
// merge into is a refusal rather than a silent overwrite, which the shared merge
// primitive owns.
func stampDocServiceProvenance(name string, doc *yaml.Node, provenance map[string]string) (int, error) {
	root := rendercore.DocumentMapping(doc)
	if root == nil {
		return 0, nil
	}
	services := rendercore.UnwrapAlias(rendercore.MappingValueByKey(root, "services"))
	if services == nil || services.Kind != yaml.MappingNode {
		return 0, nil
	}

	stamped := 0
	for i := 0; i+1 < len(services.Content); i += 2 {
		serviceName := services.Content[i].Value
		service := rendercore.UnwrapAlias(services.Content[i+1])
		if service == nil || service.Kind != yaml.MappingNode {
			continue
		}
		if err := rendercore.MergeProvenanceIntoMappingChild(
			name, "services."+serviceName, service, "labels", provenance,
		); err != nil {
			return 0, err
		}
		stamped++
	}
	return stamped, nil
}

// artifactServiceProvenanceSkipWarning is the user-facing message emitted when a
// compose artifact fragment declares no service, so the build provenance has
// nowhere to land and the fragment is emitted unchanged. It names the artifact,
// says where compose provenance lands, and states what the fragment carries
// instead, so a reader diffing output can tell an unstamped fragment from a
// missed one.
func artifactServiceProvenanceSkipWarning(filename string) string {
	return fmt.Sprintf(
		"artifact %q: skipped provenance label injection — the fragment declares no compose service. Compose carries build provenance as service labels, so a fragment contributing only top-level networks, volumes or configs carries none; the services this component emits elsewhere carry the full set.",
		filename,
	)
}
