// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package imageref reads a container image reference as Docker reads it: the
// repository it names, in the short form Docker reads it by, and its tag. It
// is the one reading of a reference the importers' image tables share, read
// by the tools generating those tables and by the importers alike.
package imageref
