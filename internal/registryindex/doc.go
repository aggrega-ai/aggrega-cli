// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package registryindex holds the local searchable index a registry refresh
// writes: its record shape, its on-disk read, the withdrawal facts it records,
// how such a fact is discovered and verified, and the screen that keeps a
// withdrawn version from being selected by a reference that did not name it.
//
// The withdrawal half is the reason the package spans discovery as well as
// reading. A withdrawal is a signed marker attached to a published artifact, and
// two unrelated passes discover one: the index build, which records a marker per
// published version, and dependency resolution, which records one onto a lockfile
// row at the moment it touches the registry. Both must honor a marker on exactly
// the same grounds, so the discovery, the marker-signature verification and the
// honored-signer policy live here once rather than once per pass.
//
// A screen performs no lookup of its own — every fact it acts on was already
// gathered before selection began. That is what lets a screen sit inside a
// selection primitive without putting a network dependency there, and it is why
// the discovery entry points are separate from the screen constructors.
//
// It also holds the two halves of the honored-withdrawal rule — whether a record
// rests on a verified signature, and which severity excludes a version — so that
// every surface deciding what a reference resolves to decides it on identical
// grounds. A second copy of either half would be free to drift on exactly the
// judgement that changes which bytes a build is made of.
//
// It exists as its own package because two unrelated consumers need it and
// neither belongs inside the other: the surfaces that maintain and search the
// index, and the dependency-resolution layer that must not float onto a version
// somebody withdrew. Reaching one through the other would be a dependency
// describing how the code was assembled rather than what it needs.
package registryindex
