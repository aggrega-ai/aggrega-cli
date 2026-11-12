// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package gotmpl holds the Go-template layer every Aggrega template surface
// shares: the function set templates are parsed and rendered with, the parse
// entry point that applies it, and the field-access census over parsed
// template text.
//
// The package exists so that a surface asking "which context paths does this
// template read?" and the surface that actually renders the template agree
// about which templates parse at all. A template referencing a catalog
// function parses here identically to the way it renders, because both reach
// one function set rather than one copy per surface.
//
// Its dependencies are the standard library, the template-function library and
// the shared entity types — nothing from the rendering or resolution paths.
// That is what lets both of those paths import it, and it is why the census
// lives here rather than beside either caller.
package gotmpl
