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
type Opaque struct{ Text, Target string }

func (Literal) value()    {}
func (Param) value()      {}
func (Compose) value()    {}
func (Ref) value()        {}
func (Credential) value() {}
func (Opaque) value()     {}
