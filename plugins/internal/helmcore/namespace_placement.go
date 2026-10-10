// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore

import (
	"bytes"
	"fmt"

	"go.yaml.in/yaml/v3"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
)

// namespacePlacement writes the namespace a node's Release declares into the
// resources of a chart render that need one and name none, which is where a Helm
// install with that release namespace puts them.
//
// A resource is placed when its kind is namespaced and its document writes no
// metadata.namespace, or writes it empty or null, which names none either. A
// resource writing a namespace keeps it, whatever it is. A cluster-scoped kind is
// left without one. A kind is classified by the one classification this provider
// holds ([kindScopes]): the Kubernetes API's own scope for a built-in kind, and
// otherwise a CustomResourceDefinition the same render emits, from its templates
// or its crds/ directory. A kind neither classifies is left as it is, and the
// render reports it once, naming the kind and its API group, the first time it
// meets a resource of it writing no namespace.
//
// It does nothing where the Release declares no namespace for the node. The chart
// then renders against the cluster default, and writing that default would place
// resources the chart left unplaced into one particular namespace the author never
// named.
type namespacePlacement struct {
	namespace string
	indent    int
	// scopes classifies the render's kinds.
	scopes kindScopes
	// advised is the kinds already reported, so each is reported once.
	advised map[advisedKind]bool
	// advisories is what the render reports, in the order it met each kind.
	advisories []renderctx.Diagnostic
}

// newNamespacePlacement prepares the placement of one render's documents into
// namespace, classifying their kinds by scopes. An empty namespace yields a
// placement that places nothing.
func newNamespacePlacement(namespace string, scopes kindScopes, indent int) *namespacePlacement {
	p := &namespacePlacement{namespace: namespace, indent: indent, scopes: scopes}
	if namespace == "" {
		return p
	}
	p.advised = map[advisedKind]bool{}
	return p
}

// place writes the namespace into one document where it belongs there, re-encoding
// the document and updating the namespace its identity is derived from. A document
// placement does not reach keeps its bytes exactly as split.
func (p *namespacePlacement) place(d *renderedDocument) error {
	if p.namespace == "" || d.kind == "" {
		return nil
	}
	root := d.root()
	metadata := findMapChild(root, "metadata")
	if metadata != nil && metadata.Kind != yaml.MappingNode {
		// A metadata written as something other than a mapping is not a resource's
		// metadata; there is nowhere in it to write a namespace.
		return nil
	}
	written := findMapChild(metadata, "namespace")
	if written != nil && !(written.Kind == yaml.ScalarNode && (written.Tag == "!!null" || written.Value == "")) {
		return nil
	}

	gk, recognisable, scope := p.scopes.scopeOf(root, d.kind)
	switch scope {
	case scopeCluster:
		return nil
	case scopeUnknown:
		p.advise(gk, recognisable)
		return nil
	}

	if metadata == nil {
		metadata = appendMapChild(root, "metadata")
	}
	if written != nil {
		// The value is replaced where it stands, so the key keeps its place and its
		// comments in the document.
		written.Tag, written.Value, written.Style = "!!str", p.namespace, 0
	} else {
		insertMapChildAfter(metadata, "namespace", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: p.namespace}, "name", "generateName")
	}
	content, err := encodeRenderedDocument(d.doc, p.indent)
	if err != nil {
		return err
	}
	d.content = content
	d.namespace = p.namespace
	return nil
}

// advise reports, once per kind, that resources of a kind nothing classifies are
// left without a namespace.
func (p *namespacePlacement) advise(gk groupKind, recognisable bool) {
	key := advisedKind{groupKind: gk, recognisable: recognisable}
	if p.advised[key] {
		return
	}
	p.advised[key] = true
	var message string
	if recognisable {
		group := fmt.Sprintf("API group %q", gk.group)
		if gk.group == "" {
			group = "the core API group"
		}
		message = fmt.Sprintf("resources of kind %q in %s write no namespace and are left without one rather than placed in namespace %q: "+
			"the kind is not one the Kubernetes API serves itself, and no CustomResourceDefinition in this chart's render gives it a single scope, "+
			"so whether it is namespaced is not known when the build runs, and they land wherever the tool applying the output places them",
			gk.kind, group, p.namespace)
	} else {
		message = fmt.Sprintf("resources of kind %q whose documents name no apiVersion write no namespace and are left without one rather than placed in namespace %q: "+
			"with no API group to read, whether the kind is namespaced is not known when the build runs, "+
			"and they land wherever the tool applying the output places them",
			gk.kind, p.namespace)
	}
	p.advisories = append(p.advisories, renderctx.Diagnostic{Severity: abi.SeverityWarn, Message: message})
}

// advisedKind keys the kinds already reported. A document naming no apiVersion is
// keyed apart from a core-group kind of the same name, because its report says a
// different thing.
type advisedKind struct {
	groupKind
	recognisable bool
}

// insertMapChildAfter adds key with value to a mapping, right after the first of
// the named keys the mapping holds, or at its end when it holds none of them.
func insertMapChildAfter(m *yaml.Node, key string, value *yaml.Node, after ...string) {
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	for _, a := range after {
		for i := 0; i+1 < len(m.Content); i += 2 {
			if k := m.Content[i]; k != nil && k.Kind == yaml.ScalarNode && k.Value == a {
				at := i + 2
				m.Content = append(m.Content[:at], append([]*yaml.Node{keyNode, value}, m.Content[at:]...)...)
				return
			}
		}
	}
	m.Content = append(m.Content, keyNode, value)
}

// encodeRenderedDocument writes one split document at the render's indentation.
// It is the one encoder of a split document, so a document placement re-encodes is
// written exactly as one it never touched.
func encodeRenderedDocument(doc *yaml.Node, indent int) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(rendercore.ClampYAMLIndent(indent))
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("encode rendered YAML: %w", err)
	}
	_ = enc.Close()
	return buf.Bytes(), nil
}
