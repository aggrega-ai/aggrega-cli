// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

// placement is where a producing template is deployed, as the ARM expressions
// the resource ID of its deployment is built from: the subscription's ID, the
// resource group's name and the management group's name.
type placement struct {
	subscription, resourceGroup, managementGroup string
}

// The expressions that read the consuming template's own subscription,
// resource group and management group where it is deployed. ARM evaluates
// resourceGroup() only in a template deployed to a resource group, subscription()
// only in one deployed to a subscription or a resource group, and
// managementGroup() only in one deployed to a management group.
const (
	ownSubscription    = "subscription().subscriptionId"
	ownResourceGroup   = "resourceGroup().name"
	ownManagementGroup = "managementGroup().name"
)

// placementOf reads where a producing template is deployed from the annotations
// of its boundary. AnnotationSubscription, AnnotationResourceGroup and
// AnnotationManagementGroup, where given, are written as ARM string literals.
// Where one is absent or empty, the producer is taken to share the consuming
// template's own subscription, resource group or management group, which
// ownSubscription, ownResourceGroup and ownManagementGroup read where the
// consumer is deployed.
func placementOf(annotations map[string]string) placement {
	p := placement{subscription: ownSubscription, resourceGroup: ownResourceGroup, managementGroup: ownManagementGroup}
	if m := annotations[AnnotationManagementGroup]; m != "" {
		p.managementGroup = StringLiteral(m)
	}
	if s := annotations[AnnotationSubscription]; s != "" {
		p.subscription = StringLiteral(s)
	}
	if r := annotations[AnnotationResourceGroup]; r != "" {
		p.resourceGroup = StringLiteral(r)
	}
	return p
}

// readsOwnResourceGroup reports whether the placement takes the producer's
// resource group to be the consumer's own, because the producer's boundary names
// none.
func (p placement) readsOwnResourceGroup() bool {
	return p.resourceGroup == ownResourceGroup
}

// readsOwnSubscription reports whether the placement takes the producer's
// subscription to be the consumer's own, because the producer's boundary names
// none.
func (p placement) readsOwnSubscription() bool {
	return p.subscription == ownSubscription
}

// readsOwnManagementGroup reports whether the placement takes the producer's
// management group to be the consumer's own, because the producer's boundary
// names none.
func (p placement) readsOwnManagementGroup() bool {
	return p.managementGroup == ownManagementGroup
}
