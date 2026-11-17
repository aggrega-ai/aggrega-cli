// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

// placement is where a producing template is deployed, as the ARM expressions
// the resource ID of its deployment is built from: the subscription's ID and the
// resource group's name.
type placement struct {
	subscription, resourceGroup string
}

// placementOf reads where a producing template is deployed from the annotations
// of its boundary. AnnotationSubscription and AnnotationResourceGroup, where
// given, are written as ARM string literals. Where one is absent or empty, the
// producer is taken to share the consuming template's own subscription or
// resource group, which subscription().subscriptionId and resourceGroup().name
// read where the consumer is deployed.
func placementOf(annotations map[string]string) placement {
	p := placement{subscription: "subscription().subscriptionId", resourceGroup: "resourceGroup().name"}
	if s := annotations[AnnotationSubscription]; s != "" {
		p.subscription = StringLiteral(s)
	}
	if r := annotations[AnnotationResourceGroup]; r != "" {
		p.resourceGroup = StringLiteral(r)
	}
	return p
}
