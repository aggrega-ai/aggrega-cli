// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import "encoding/json"

// Value is one value a source writes: a property, a resource name, a
// condition, a parameter's default or an output. Each format lowers what it
// reads into these forms, each held by value (Ref{…}, never &Ref{…}), and the
// drafts are written from them alone.
type Value interface{ value() }

// Literal is a JSON literal as the source writes it.
type Literal struct{ JSON json.RawMessage }

// Param is the value of one of the source's parameters, named as the source
// declares it.
type Param struct{ Name string }

// Compose is the string concatenation of its parts, in order.
type Compose struct{ Parts []Value }

// Ref is an attribute of a node of the graph, read at the path Attr; an empty
// Attr is the node's identifier.
type Ref struct {
	Node string
	Attr []string
}

// Credential is a credential read from a node: What names the source's call or
// output that reads it.
type Credential struct{ Node, What string }

// Opaque is text evaluated when the source deploys, written in the source's
// own language: a draft for Target can carry it as written, and a draft for any
// other target cannot.
type Opaque struct {
	Text, Target string
	// Params are the source's parameters the format wrote in Text as the
	// literal value each is given, in the order Text reads them, each named in
	// a comment above the value written.
	Params []string
	// Owed says why Text cannot be written as it is, naming what it reads that
	// the format could not write in its place and no template of the draft
	// declares; empty where it can be. Text owing something is not written:
	// its key is left for the Release's answers file, and value-not-carried
	// warns of it.
	Owed string
}

// Stored is a secret a secret store holds, which the source reads by its
// locator and the format's own target resolves when the source deploys: Store
// names the store, as the source's language names it, Locator is where the
// store keeps the secret, and Text is the source's whole reference to it. The
// draft never carries the secret's value. Placed in a credential slot of a
// mapped instance's row, it is the kit's referenced secret whose origin is the
// locator; anywhere else, Text is written as it is where the import drafts for
// the format's own target, and the key is left, its comment naming the store
// and the locator, on any other.
type Stored struct {
	Store, Locator, Text string
}

func (Literal) value()    {}
func (Param) value()      {}
func (Compose) value()    {}
func (Ref) value()        {}
func (Credential) value() {}
func (Opaque) value()     {}
func (Stored) value()     {}
