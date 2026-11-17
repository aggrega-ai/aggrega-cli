// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

// placement is where a producing template is deployed, as the ARM expressions
// the resource ID of its deployment is built from: the subscription's ID and the
// resource group's name.
type placement struct {
	subscription, resourceGroup string
}

// The expressions that read the consuming template's own subscription and
// resource group where it is deployed. ARM evaluates resourceGroup() only in a
// template deployed to a resource group.
const (
	ownSubscription  = "subscription().subscriptionId"
	ownResourceGroup = "resourceGroup().name"
)

// placementOf reads where a producing template is deployed from the annotations
// of its boundary. AnnotationSubscription and AnnotationResourceGroup, where
// given, are written as ARM string literals. Where one is absent or empty, the
// producer is taken to share the consuming template's own subscription or
// resource group, which ownSubscription and ownResourceGroup read where the
// consumer is deployed.
func placementOf(annotations map[string]string) placement {
	p := placement{subscription: ownSubscription, resourceGroup: ownResourceGroup}
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
