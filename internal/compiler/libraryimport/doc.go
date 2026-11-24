// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package libraryimport resolves a Recipe's `$import` of a MappingLibrary or an
// ArtifactLibrary entry.
//
// A library import names a library and an entry by bare-name identity,
// `<Library>[:<version>]#/mappings/<entry>` or `<Library>[:<version>]#/artifacts/<bundle>`,
// and resolves against the set of libraries a build has already loaded: the local
// catalog plus the lockfile-pinned libraries. Resolution touches nothing outside
// that set, so the same reference over the same set always answers identically.
//
// Every caller that needs to resolve such a reference — a build rendering a
// Recipe, and a tool that reads a Recipe's imports without rendering — calls the
// functions here, so the answer and the halting conditions are one.
package libraryimport
