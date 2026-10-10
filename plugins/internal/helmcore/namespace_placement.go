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

// resourceScope is whether the Kubernetes API keeps a kind's resources inside a
// namespace or across the whole cluster.
type resourceScope int

const (
	// scopeUnknown is a kind nothing in this render classifies.
	scopeUnknown resourceScope = iota
	// scopeNamespaced is a kind whose resources live in a namespace.
	scopeNamespaced
	// scopeCluster is a kind whose resources belong to no namespace.
	scopeCluster
)

// groupKind names a kind the way the Kubernetes API keys it: by its API group,
// "" being the core group, and its kind. The version is no part of it, because
// every version of a group serves one kind at one scope.
type groupKind struct {
	group string
	kind  string
}

// builtInKindScopes is the scope the Kubernetes API itself gives each kind it
// serves without a CustomResourceDefinition, as `kubectl api-resources` reports it
// in its NAMESPACED column. It is the one place a chart render classifies a
// built-in kind, and it is read by lookup alone.
//
// It carries the kinds of the groups an API server serves today and the kinds of
// the groups and kinds later removed from it — the extensions group, and
// PodSecurityPolicy — because a chart still renders those for a cluster that
// serves them, and they kept one scope while they were served. A kind missing
// here is not guessed: a render that emits it with no namespace says so and
// leaves it as it is.
var builtInKindScopes = map[groupKind]resourceScope{
	// core
	{"", "Binding"}:               scopeNamespaced,
	{"", "ComponentStatus"}:       scopeCluster,
	{"", "ConfigMap"}:             scopeNamespaced,
	{"", "Endpoints"}:             scopeNamespaced,
	{"", "Event"}:                 scopeNamespaced,
	{"", "LimitRange"}:            scopeNamespaced,
	{"", "Namespace"}:             scopeCluster,
	{"", "Node"}:                  scopeCluster,
	{"", "PersistentVolume"}:      scopeCluster,
	{"", "PersistentVolumeClaim"}: scopeNamespaced,
	{"", "Pod"}:                   scopeNamespaced,
	{"", "PodTemplate"}:           scopeNamespaced,
	{"", "ReplicationController"}: scopeNamespaced,
	{"", "ResourceQuota"}:         scopeNamespaced,
	{"", "Secret"}:                scopeNamespaced,
	{"", "Service"}:               scopeNamespaced,
	{"", "ServiceAccount"}:        scopeNamespaced,

	{"admissionregistration.k8s.io", "MutatingAdmissionPolicy"}:          scopeCluster,
	{"admissionregistration.k8s.io", "MutatingAdmissionPolicyBinding"}:   scopeCluster,
	{"admissionregistration.k8s.io", "MutatingWebhookConfiguration"}:     scopeCluster,
	{"admissionregistration.k8s.io", "ValidatingAdmissionPolicy"}:        scopeCluster,
	{"admissionregistration.k8s.io", "ValidatingAdmissionPolicyBinding"}: scopeCluster,
	{"admissionregistration.k8s.io", "ValidatingWebhookConfiguration"}:   scopeCluster,

	{"apiextensions.k8s.io", "CustomResourceDefinition"}: scopeCluster,
	{"apiregistration.k8s.io", "APIService"}:             scopeCluster,

	{"apps", "ControllerRevision"}: scopeNamespaced,
	{"apps", "DaemonSet"}:          scopeNamespaced,
	{"apps", "Deployment"}:         scopeNamespaced,
	{"apps", "ReplicaSet"}:         scopeNamespaced,
	{"apps", "StatefulSet"}:        scopeNamespaced,

	{"authentication.k8s.io", "SelfSubjectReview"}:       scopeCluster,
	{"authentication.k8s.io", "TokenReview"}:             scopeCluster,
	{"authorization.k8s.io", "LocalSubjectAccessReview"}: scopeNamespaced,
	{"authorization.k8s.io", "SelfSubjectAccessReview"}:  scopeCluster,
	{"authorization.k8s.io", "SelfSubjectRulesReview"}:   scopeCluster,
	{"authorization.k8s.io", "SubjectAccessReview"}:      scopeCluster,

	{"autoscaling", "HorizontalPodAutoscaler"}: scopeNamespaced,

	{"batch", "CronJob"}: scopeNamespaced,
	{"batch", "Job"}:     scopeNamespaced,

	{"certificates.k8s.io", "CertificateSigningRequest"}: scopeCluster,
	{"certificates.k8s.io", "ClusterTrustBundle"}:        scopeCluster,

	{"coordination.k8s.io", "Lease"}:          scopeNamespaced,
	{"coordination.k8s.io", "LeaseCandidate"}: scopeNamespaced,

	{"discovery.k8s.io", "EndpointSlice"}: scopeNamespaced,
	{"events.k8s.io", "Event"}:            scopeNamespaced,

	{"extensions", "DaemonSet"}:         scopeNamespaced,
	{"extensions", "Deployment"}:        scopeNamespaced,
	{"extensions", "Ingress"}:           scopeNamespaced,
	{"extensions", "NetworkPolicy"}:     scopeNamespaced,
	{"extensions", "PodSecurityPolicy"}: scopeCluster,
	{"extensions", "ReplicaSet"}:        scopeNamespaced,

	{"flowcontrol.apiserver.k8s.io", "FlowSchema"}:                 scopeCluster,
	{"flowcontrol.apiserver.k8s.io", "PriorityLevelConfiguration"}: scopeCluster,

	{"networking.k8s.io", "IPAddress"}:     scopeCluster,
	{"networking.k8s.io", "Ingress"}:       scopeNamespaced,
	{"networking.k8s.io", "IngressClass"}:  scopeCluster,
	{"networking.k8s.io", "NetworkPolicy"}: scopeNamespaced,
	{"networking.k8s.io", "ServiceCIDR"}:   scopeCluster,

	{"node.k8s.io", "RuntimeClass"}: scopeCluster,

	{"policy", "PodDisruptionBudget"}: scopeNamespaced,
	{"policy", "PodSecurityPolicy"}:   scopeCluster,

	{"rbac.authorization.k8s.io", "ClusterRole"}:        scopeCluster,
	{"rbac.authorization.k8s.io", "ClusterRoleBinding"}: scopeCluster,
	{"rbac.authorization.k8s.io", "Role"}:               scopeNamespaced,
	{"rbac.authorization.k8s.io", "RoleBinding"}:        scopeNamespaced,

	{"resource.k8s.io", "DeviceClass"}:           scopeCluster,
	{"resource.k8s.io", "ResourceClaim"}:         scopeNamespaced,
	{"resource.k8s.io", "ResourceClaimTemplate"}: scopeNamespaced,
	{"resource.k8s.io", "ResourceSlice"}:         scopeCluster,

	{"scheduling.k8s.io", "PriorityClass"}: scopeCluster,

	{"storage.k8s.io", "CSIDriver"}:             scopeCluster,
	{"storage.k8s.io", "CSINode"}:               scopeCluster,
	{"storage.k8s.io", "CSIStorageCapacity"}:    scopeNamespaced,
	{"storage.k8s.io", "StorageClass"}:          scopeCluster,
	{"storage.k8s.io", "VolumeAttachment"}:      scopeCluster,
	{"storage.k8s.io", "VolumeAttributesClass"}: scopeCluster,
}

// customResourceDefinition is the kind a chart declares a custom kind's scope with.
var customResourceDefinition = groupKind{"apiextensions.k8s.io", "CustomResourceDefinition"}

// namespacePlacement writes the namespace a node's Release declares into the
// resources of a chart render that need one and name none, which is where a Helm
// install with that release namespace puts them.
//
// A resource is placed when its kind is namespaced and its document writes no
// metadata.namespace, or writes it empty or null, which names none either. A
// resource writing a namespace keeps it, whatever it is. A cluster-scoped kind is
// left without one. A kind is classified by the Kubernetes API's own scope for a
// built-in kind ([builtInKindScopes]), and otherwise by a CustomResourceDefinition
// the same render emits, from its templates or its crds/ directory. A kind neither
// classifies is left as it is, and the render reports it once, naming the kind and
// its API group, the first time it meets a resource of it writing no namespace.
//
// It does nothing where the Release declares no namespace for the node. The chart
// then renders against the cluster default, and writing that default would place
// resources the chart left unplaced into one particular namespace the author never
// named.
type namespacePlacement struct {
	namespace string
	indent    int
	// declared is the scope each custom kind's CustomResourceDefinition in the
	// render gives it. Two definitions giving one kind different scopes leave it
	// unclassified.
	declared map[groupKind]resourceScope
	// advised is the kinds already reported, so each is reported once.
	advised map[advisedKind]bool
	// advisories is what the render reports, in the order it met each kind.
	advisories []renderctx.Diagnostic
}

// newNamespacePlacement prepares the placement of one render's documents into
// namespace, reading the scope every CustomResourceDefinition among them declares.
// An empty namespace yields a placement that places nothing.
func newNamespacePlacement(namespace string, sources [][]renderedDocument, indent int) *namespacePlacement {
	p := &namespacePlacement{namespace: namespace, indent: indent}
	if namespace == "" {
		return p
	}
	p.declared = map[groupKind]resourceScope{}
	p.advised = map[advisedKind]bool{}
	for _, docs := range sources {
		for i := range docs {
			p.readDeclaredScope(docs[i])
		}
	}
	return p
}

// readDeclaredScope records the scope a CustomResourceDefinition document gives
// the kind it defines. A definition naming no group, no kind or a scope other than
// the two the API accepts classifies nothing.
func (p *namespacePlacement) readDeclaredScope(d renderedDocument) {
	root := d.root()
	if gk, ok := groupKindOf(root, d.kind); !ok || gk != customResourceDefinition {
		return
	}
	spec := childOf(root, "spec")
	defined := groupKind{group: scalarChildValue(spec, "group"), kind: scalarChildValue(childOf(spec, "names"), "kind")}
	if defined.group == "" || defined.kind == "" {
		return
	}
	var scope resourceScope
	switch scalarChildValue(spec, "scope") {
	case "Namespaced":
		scope = scopeNamespaced
	case "Cluster":
		scope = scopeCluster
	default:
		return
	}
	if prior, seen := p.declared[defined]; seen && prior != scope {
		scope = scopeUnknown
	}
	p.declared[defined] = scope
}

// scopeOf classifies a kind: the Kubernetes API's own scope for a built-in kind,
// else the scope a CustomResourceDefinition in the render declares for it.
func (p *namespacePlacement) scopeOf(gk groupKind) resourceScope {
	if scope, builtIn := builtInKindScopes[gk]; builtIn {
		return scope
	}
	return p.declared[gk]
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

	gk, recognisable := groupKindOf(root, d.kind)
	scope := scopeUnknown
	if recognisable {
		scope = p.scopeOf(gk)
	}
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

// groupKindOf is a document's kind keyed by the API group its apiVersion names,
// and whether the document names an apiVersion to read a group from.
func groupKindOf(root *yaml.Node, kind string) (groupKind, bool) {
	group, recognisable := apiGroup(scalarChildValue(root, "apiVersion"))
	return groupKind{group: group, kind: kind}, recognisable
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
