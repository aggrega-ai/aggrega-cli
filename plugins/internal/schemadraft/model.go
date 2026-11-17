// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package schemadraft

import "encoding/json"

// Kind is the type of a property's value.
type Kind string

// The kinds a property takes.
const (
	KindString  Kind = "string"
	KindInteger Kind = "integer"
	KindNumber  Kind = "number"
	KindBoolean Kind = "boolean"
	KindObject  Kind = "object"
	KindArray   Kind = "array"
	KindAny     Kind = "any"
)

// Block describes how a Terraform nested block repeats: its nesting mode
// (single, list, set, map or group) and the bounds on its item count.
type Block struct {
	Nesting  string
	MinItems int
	MaxItems int
}

// ProviderAddress is the registry address of a Terraform provider. Version is
// empty when the export states none.
type ProviderAddress struct {
	Host      string
	Namespace string
	Type      string
	Version   string
}

// Property is one property of a resource body, in the vendor's spelling.
type Property struct {
	// Name is the vendor spelling; Path the vendor path from the resource body root.
	Name string
	Path []string
	// Kinds holds one kind, or the open union of the branches of a oneOf or anyOf.
	Kinds []Kind
	// Description is the first paragraph of the vendor text, unmarked.
	Description string

	Required, Identifier, CreateOnly, ReadOnly, WriteOnly, Sensitive, Deprecated bool

	Enum                 []json.RawMessage
	Pattern, Format      string
	Min, Max             *json.Number
	MinLength, MaxLength *int

	// Items is the element property of an array.
	Items *Property
	// Properties are the members of an object, sorted by Name.
	Properties []*Property
	// Open reports that the object accepts members beyond Properties.
	Open  bool
	Block *Block
	// Cut is "cycle" or "depth" where the subtree was cut to an open object.
	Cut string
	// Dropped names the constraint keywords read and not carried, with why.
	Dropped []string
}

// Type is one resource type a reader has lifted from its source.
type Type struct {
	Name, APIVersion, Description, Source string
	Properties                            []*Property
	PrimaryIdentifier                     [][]string
	Provider                              *ProviderAddress
	// Scopes are the writable scopes of an ARM type, in the order resourceGroup,
	// subscription, managementGroup, tenant, extension.
	Scopes []string
}

// TypeRef names a resource type a source holds, before it is lifted. Body is
// private to the format that produced it.
type TypeRef struct {
	Name, APIVersion, Source string
	Provider                 *ProviderAddress
	Body                     json.RawMessage
}

// Request is one entry of the resource_types setting.
type Request struct{ Type, APIVersion, Component string }

// Settings are the settings a schema import reads.
type Settings struct {
	ResourceTypes   []Request
	TargetNamespace string
}

// Selected is a type chosen for drafting, with the Component name its request gave.
type Selected struct {
	Ref       TypeRef
	Component string
}
