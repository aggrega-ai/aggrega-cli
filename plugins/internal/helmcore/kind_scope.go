// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore

import "go.yaml.in/yaml/v3"

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
// in its NAMESPACED column. It is the one place this provider classifies a
// built-in kind, and it is read by lookup alone, through [kindScopes].
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

// kindScopes is the one classification of kinds by scope this provider holds:
// whether the Kubernetes API keeps the resources of a kind inside a namespace or
// across the whole cluster. A built-in kind takes the API's own scope
// ([builtInKindScopes]); any other kind takes the scope a CustomResourceDefinition
// among the same render's documents declares for it — from a chart's templates or
// its crds/ directory, or from an artifact body. A kind two such definitions give
// different scopes, a custom kind no definition in the render declares, and a
// document naming no apiVersion — which names no API group to key a kind by — are
// classified as neither.
//
// Two readers classify a render's kinds, and both classify through this alone:
// the namespace placement, which leaves a cluster-scoped resource without the
// declared namespace, and the names declaration, which declares a cluster-scoped
// resource's name without a namespace. So a kind added to the list, or a
// definition added to a render, moves both together. A second list or a second
// definition reader beside either of them would agree with this one until the day
// a kind was added to one and not the other, and the two would then disagree about
// one resource with nothing failing.
//
// It takes no namespace, so it classifies the same whether or not the Release
// declares one for the node.
type kindScopes struct {
	// declared is the scope each custom kind's CustomResourceDefinition in the
	// render gives it. It is read by lookup alone.
	declared map[groupKind]resourceScope
}

// classifyKinds reads the scope every CustomResourceDefinition among one render's
// documents declares for the kind it defines. roots are the documents' root
// mappings, and one that is not a definition classifies nothing.
//
// The result is a function of the set of scopes declared for each kind: one scope
// classifies the kind, and two classify it as neither, which a third never undoes.
// So the order the documents come in, and a definition repeated byte for byte,
// move nothing.
func classifyKinds(roots []*yaml.Node) kindScopes {
	declared := map[groupKind]resourceScope{}
	for _, root := range roots {
		defined, scope, ok := definedScope(root)
		if !ok {
			continue
		}
		if prior, seen := declared[defined]; seen && prior != scope {
			scope = scopeUnknown
		}
		declared[defined] = scope
	}
	return kindScopes{declared: declared}
}

// definedScope is the kind a CustomResourceDefinition document defines and the
// scope it gives that kind. A document that is not a definition, and a definition
// naming no group, no kind or a scope other than the two the API accepts, define
// nothing.
func definedScope(root *yaml.Node) (groupKind, resourceScope, bool) {
	if gk, ok := groupKindOf(root, scalarChildValue(root, "kind")); !ok || gk != customResourceDefinition {
		return groupKind{}, scopeUnknown, false
	}
	spec := childOf(root, "spec")
	defined := groupKind{group: scalarChildValue(spec, "group"), kind: scalarChildValue(childOf(spec, "names"), "kind")}
	if defined.group == "" || defined.kind == "" {
		return groupKind{}, scopeUnknown, false
	}
	switch scalarChildValue(spec, "scope") {
	case "Namespaced":
		return defined, scopeNamespaced, true
	case "Cluster":
		return defined, scopeCluster, true
	}
	return groupKind{}, scopeUnknown, false
}

// scopeOf classifies the kind of one document whose root mapping is root: the
// kind keyed by the API group its apiVersion names, whether the document names an
// apiVersion to read a group from, and the kind's scope — the Kubernetes API's own
// for a built-in kind, else the one a CustomResourceDefinition in the render
// declares. A document naming no apiVersion is classified as neither.
func (s kindScopes) scopeOf(root *yaml.Node, kind string) (groupKind, bool, resourceScope) {
	gk, recognisable := groupKindOf(root, kind)
	if !recognisable {
		return gk, false, scopeUnknown
	}
	if scope, builtIn := builtInKindScopes[gk]; builtIn {
		return gk, true, scope
	}
	return gk, true, s.declared[gk]
}

// groupKindOf is a document's kind keyed by the API group its apiVersion names,
// and whether the document names an apiVersion to read a group from.
func groupKindOf(root *yaml.Node, kind string) (groupKind, bool) {
	group, recognisable := apiGroup(scalarChildValue(root, "apiVersion"))
	return groupKind{group: group, kind: kind}, recognisable
}
