// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package cfntemplate reads a CloudFormation template and holds the grammar of
// the references inside it.
//
// [Normalize] reads one template, YAML with short forms or JSON, as CloudFormation
// reads it and returns sorted-key long-form JSON; no intrinsic is evaluated. The
// grammar helpers read the strings and arguments an intrinsic carries. Every surface that reads a template or a reference calls here,
// so the reading is decided in one place.
//
// The package is a leaf: it depends on a YAML reader, the template reader's
// intrinsics package and the anchor-cycle check, and on nothing else of the
// compiler.
package cfntemplate
