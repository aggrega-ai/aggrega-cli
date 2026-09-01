// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore

import (
	"bytes"
	"io"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/pkg/namesegment"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// resourceNameSpacePrefix opens every key space an emitted resource's name is
// declared into, so a reader meeting one in a refusal can tell what kind of name
// it is talking about before reading the rest of it.
//
// It is a fixed literal and reserves nothing: it is constant across every space
// assembled here, so it is no part of what tells two spaces apart — the kind and
// the namespace following it are.
const resourceNameSpacePrefix = "resource-names:"

// resourceNameSpaceEncoding assembles the scope half of a key space from the two
// coordinates that bound a Kubernetes resource name.
//
// Its admitted alphabet is the ASCII letters and digits. That is stated here
// rather than taken from the encoding because it is a property of what these two
// coordinates carry — a kind is written in letters, a namespace in lowercase
// letters, digits, hyphens and dots — and not of the encoding, which serves each
// caller's own target.
//
// Encoding rather than joining is the property, not a preference. A namespace may
// contain a hyphen, so a plain hyphen join over the pair folds: the kind `a` in
// namespace `b-c` and the kind `a-b` in namespace `c` reach one string, and two
// resources whose names may legitimately coincide would then be compared against
// each other. The two-hyphen join no encoded segment contains cannot fold that
// way, so distinct pairs always reach distinct spaces.
var resourceNameSpaceEncoding = namesegment.NewEncoding(isResourceNameSpaceRune)

// isResourceNameSpaceRune reports whether a character is carried through the key
// space encoding as itself: an ASCII letter or digit.
func isResourceNameSpaceRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}

// resourceNameSpace is the key space one emitted resource's name is declared
// into: the fixed prefix, then the kind and the namespace the resource lands in,
// assembled injectively.
//
// The scope belongs in the SPACE and the name in the key, rather than all three
// being run together into one declared name. Two reasons, and the second is the
// stronger. A key space is exactly the mechanism the render contract offers a
// guest that needs uniqueness at a narrower scope than the whole assembled
// output, so expressing the scope there keeps this guest's knowledge of what
// bounds a Kubernetes name inside this guest. And the declared name then stays
// byte-identical to the string the chart's author wrote, so a refusal naming it
// doubles as the term to search their own templates for — a composed name would
// send them looking for a string that appears in none of their files.
//
// Both coordinates are needed and neither is optional. A Service and a Deployment
// named alike is ordinary correct Kubernetes authoring, so a space that dropped
// the kind would refuse it; two namespaces hold two resources of one name
// independently, so a space that dropped the namespace would refuse that too.
func resourceNameSpace(kind, namespace string) string {
	return resourceNameSpacePrefix + resourceNameSpaceEncoding.Name(kind, namespace)
}

// EffectiveNamespace is the namespace a node's resources land in when the
// resource itself names none: the namespace the author annotated, or the cluster
// default that a target with no namespace in hand falls back to.
//
// It is one function rather than a fallback written out at each site, because a
// second copy would agree with this one on the day it was written and drift
// afterwards — and one site resolving the fallback differently from another would
// leave a resource declared under one namespace and rendered under another, with
// nothing failing.
func EffectiveNamespace(nodeCtx rendercore.NodeRenderContext) string {
	if ns := rendercore.NamespaceFromAnnotations(nodeCtx.Annotations); ns != "" {
		return ns
	}
	return defaultNamespace
}

// defaultNamespace is what a Kubernetes target addresses when nothing names a
// namespace.
const defaultNamespace = "default"

// DeclaredResourceNames reports the names this node's emitted output addresses
// its resources by, per key space, for the host to confirm that no two
// contributors to one assembled output claim the same one.
//
// # Why the guest declares rather than the host reading
//
// A chart's own templates name the resources they emit, routinely after the
// release name, and that name lives inside the file bytes the host writes
// verbatim. The host cannot see it, and reading it back would mean parsing
// Kubernetes manifests in the host, which is the domain knowledge the plugin
// boundary keeps out. So the guest declares what it emitted, and what crosses is
// already the answer to every Kubernetes-specific question — which kind, which
// namespace, which field is the name.
//
// # The declaration mirrors the emitted set, and moves none of it
//
// It is derived from the files the render is about to hand back rather than from
// anything upstream of them, so what is declared is exactly what reaches disk. A
// document the render dropped is not declared, and a document the render merged
// away as a byte-identical duplicate is declared once, because there is one file.
// Nothing here writes to a file, changes a byte of one, or reorders them: the
// emitted output of a node that declares is identical to the emitted output of the
// same node before it did.
//
// # It never fails
//
// A file that does not parse, a document that is not a resource mapping, and a
// document carrying no kind or no name are all skipped rather than reported.
// Declaring is an addition to what a render already does, so it introduces no
// failure of its own; the only refusal in this mechanism is the host's, over
// names two contributors both claimed.
//
// # What it does NOT cover
//
// It sees the name a document carries in metadata.name, which is the name the
// emitted bytes actually declare. Two classes stay outside it, and both are
// bounds rather than gaps awaiting work. A resource whose name the cluster
// completes at admission — a generateName resource — has no name here to declare,
// so nothing about it is compared. And a name that never reaches metadata.name at
// all, because a chart writes it into some other field a controller later reads,
// is not a resource name this or any other reader of the emitted documents can
// recognise. So a green from the host's comparison means no two declared names
// met; it does not mean every name the output will eventually carry was watched.
func DeclaredResourceNames(
	files []rendercore.RenderedFile,
	fallbackNamespace string,
) types.OrderedMap[[]string] {
	declared := types.OrderedMap[[]string]{}
	// Walked in the order the render emits, so the declaration's space order and
	// each space's name order are functions of the render's own output rather than
	// of a map walk — which is what keeps the payload byte-identical across runs
	// of identical input.
	for _, f := range files {
		for _, root := range resourceMappings(f.Content) {
			kind := scalarChildValue(root, "kind")
			metadata := findMapChild(root, "metadata")
			name := scalarChildValue(metadata, "name")
			if kind == "" || name == "" {
				continue
			}
			namespace := scalarChildValue(metadata, "namespace")
			if namespace == "" {
				namespace = fallbackNamespace
			}
			space := resourceNameSpace(kind, namespace)
			names, _ := declared.Get(space)
			declared.Set(space, append(names, name))
		}
	}
	return declared
}

// resourceMappings returns the root mapping node of every document in one
// emitted file that is shaped like a resource, skipping everything else.
//
// A parse failure yields nothing rather than an error, for the reason given on
// [DeclaredResourceNames]: this reader is downstream of the render, and a reader
// that could refuse would give a render a second way to fail for a reason that has
// nothing to do with what it emitted.
func resourceMappings(content []byte) []*yaml.Node {
	dec := yaml.NewDecoder(bytes.NewReader(content))
	var out []*yaml.Node
	for {
		var doc yaml.Node
		if err := dec.Decode(&doc); err != nil {
			if err == io.EOF {
				break
			}
			return out
		}
		if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
			continue
		}
		root := doc.Content[0]
		if root == nil || root.Kind != yaml.MappingNode {
			continue
		}
		out = append(out, root)
	}
	return out
}
