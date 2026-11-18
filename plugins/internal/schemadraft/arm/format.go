// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package arm

import (
	"fmt"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/armcore"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

const (
	formatName = "arm-schema"
	capability = armcore.TargetName
	syntax     = "json"
)

// The keys of a resource body the rules read.
const (
	keyName       = "name"
	keyLocation   = "location"
	keyID         = "id"
	keyProperties = "properties"
	keySystemData = "systemData"
)

// Format is the Bicep type file format, arm-schema: a types.json from
// Azure/bicep-types-az, one resource provider namespace at one apiVersion,
// holding several resource types, whose names are matched without regard to
// case as Azure Resource Manager reads them, the draft keeping the file's
// spelling. Its Recipes target the arm provider. Placement is the root
// location; identity is the root name; a secret is a sensitive property,
// declared as a deploy-time parameter; a read-back scalar nested in objects is
// provided, since reference reads it by a dotted path, except within the root
// systemData, which is not provided. A type writable at no scope the provider
// deploys at is refused.
var Format = schemadraft.Format{
	Name:       formatName,
	Capability: capability,
	Syntax:     syntax,
	FoldCase:   true,
	List:       list,
	Lift:       lift,
	Refuse:     refuse,
	Rules: schemadraft.Rules{
		Placement:      placement,
		Identity:       identity,
		Secret:         secret,
		NestedProvides: true,
		NotProvided:    notProvided,
	},
	Category: category,
	Write:    Write,
}

// placement reports whether p is the root location, where the resource is
// deployed.
func placement(_ *schemadraft.Type, p *schemadraft.Property) bool {
	return slices.Equal(p.Path, []string{keyLocation})
}

// identity reports whether p is the root name, the resource's own name.
func identity(_ *schemadraft.Type, p *schemadraft.Property) bool {
	return slices.Equal(p.Path, []string{keyName})
}

// secret reports whether p is a secret: a property the file marks sensitive.
func secret(_ *schemadraft.Type, p *schemadraft.Property) bool { return p.Sensitive }

// systemDataWhy says why the root systemData is not provided.
const systemDataWhy = "it is the metadata Azure Resource Manager keeps on every resource it tracks, naming who " +
	"created and last changed it, not a value the resource publishes for its neighbours"

// notProvided says why p is left out of provides: the root systemData, read
// back on every tracked resource; "" for any other property.
func notProvided(_ *schemadraft.Type, p *schemadraft.Property) string {
	if slices.Equal(p.Path, []string{keySystemData}) {
		return systemDataWhy
	}
	return ""
}

// The scopes a Bicep type file states a type is writable at, by the bit of
// writableScopes each takes, in the order schemadraft.Type lists them.
var scopeBits = []struct {
	scope string
	bit   int
}{
	{string(armcore.ScopeResourceGroup), 8},
	{string(armcore.ScopeSubscription), 4},
	{string(armcore.ScopeManagementGroup), 2},
	{scopeTenant, 1},
	{scopeExtension, 16},
}

// The scopes the arm provider deploys no template at.
const (
	scopeTenant    = "tenant"
	scopeExtension = "extension"
)

// servedScopes are the scopes the arm provider deploys a template at, narrowest
// first.
var servedScopes = []armcore.Scope{armcore.ScopeResourceGroup, armcore.ScopeSubscription, armcore.ScopeManagementGroup}

// scopesOf names the scopes the writableScopes bits give, in scopeBits' order.
func scopesOf(bits int) []string {
	var out []string
	for _, s := range scopeBits {
		if bits&s.bit != 0 {
			out = append(out, s.scope)
		}
	}
	return out
}

// servedScope is the narrowest of scopes the arm provider deploys at, and
// whether there is one.
func servedScope(scopes []string) (armcore.Scope, bool) {
	for _, s := range servedScopes {
		if slices.Contains(scopes, string(s)) {
			return s, true
		}
	}
	return "", false
}

// contentScope is the scope a fragment declaring a resource of t is written
// for: the narrowest scope t is writable at that the arm provider deploys at,
// and a resource group for a type that states no scope.
func contentScope(t *schemadraft.Type) armcore.Scope {
	if scope, ok := servedScope(t.Scopes); ok {
		return scope
	}
	return armcore.ScopeResourceGroup
}

// refuse halts on a type writable at no scope the arm provider deploys a
// template at: only at the tenant scope, which it does not assemble; only as an
// extension resource, deployed onto another resource the draft holds no input
// naming; or at no scope, Azure Resource Manager reading the type and deploying
// none.
func refuse(t *schemadraft.Type) *aggrega.Diagnostic {
	if _, ok := servedScope(t.Scopes); ok {
		return nil
	}
	why := "is writable at no scope: Azure Resource Manager reads it and deploys none, so no template declares it; " +
		"import a type a template deploys"
	if len(t.Scopes) > 0 {
		scopes := "scope"
		if len(t.Scopes) > 1 {
			scopes = "scopes"
		}
		why = fmt.Sprintf("is writable only at the %s %s, and the arm provider deploys a template at the resource "+
			"group, subscription or management group scope", strings.Join(t.Scopes, " and "), scopes)
		if slices.Contains(t.Scopes, scopeExtension) {
			why += "; an extension resource is deployed onto another resource, which the draft holds no input naming"
		}
		why += "; import a type writable at one of the scopes the provider deploys at"
	}
	return &aggrega.Diagnostic{
		Severity:  aggrega.SeverityHalt,
		Code:      schemadraft.CodeResourceTypeRefused,
		Message:   fmt.Sprintf("the resource type %s@%s %s", t.Name, t.APIVersion, why),
		SourceRef: t.Source,
	}
}
