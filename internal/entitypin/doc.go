// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package entitypin is the pin step: the one implementation that records, in the
// entity.lock beside a Recipe or FeatureRecipe, the content pins for the inputs
// the entity reads from outside its own document.
//
// # One step, reached from every caller that pins
//
// An author runs the step on an entity they are about to publish, and a
// dependency resolution or a publication may run it on the author's behalf when
// asked to. Each of those reaches [Compute], and every lock written is written
// through a [Plan] it returns, so the bytes an author's own run records and the
// bytes a resolution or a publication records for the same entity are one
// computation rather than several that agree today. A second writer of the lock
// would be free to route a pin to a different entity, digest an imported file
// differently or order the rows another way, and nothing would report the two
// coming apart until a build refused one of them.
//
// # What the step can pin, and why it needs no Release
//
// A source whose author wrote its identity out in full names one artifact for
// every Release that uses the entity, so it is resolvable with no Release in
// reach: its identity fields carry no template, and what little is evaluated at
// all is evaluated against empty values and infra. A source whose identity comes
// from a template names an artifact only once a Release fills it in, so it is not
// pinned here; each identity it resolves to is pinned in the lockfile of the
// Release that resolved it. An imported file is read from the workspace and
// digested as it stands, and so is each file a local source contributes to what
// the entity carries — the files its declared set names, which are the files a
// publication of it packs.
//
// # The writable home
//
// The step writes a file beside the entity, so it runs only for an entity that
// has a writable home: a document in a catalog directory the step read it from.
// That is a property of where the catalog found the entity, not of how its source
// is written — a reference the workspace already holds and an entity the catalog
// holds a writable file for are two different meanings of "local", and only the
// second decides where the step may write. An entity a resolution reached through
// the artifact cache has no such home and is passed over.
package entitypin
