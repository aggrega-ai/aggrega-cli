// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package importops holds the import operation every caller-facing surface
// reaches: turning a foreign source description into draft Aggrega authoring
// files, through the importer the catalog resolves for the source's format.
//
// The package exists because the logic used to sit in the command-line package,
// which is package main and therefore unimportable by any other package — a
// language rule rather than a convention. A surface that is not the command line
// could not reach the code even though the code was right there.
//
// # One operation, and why it is nonetheless its own package
//
// The subsystem carries a per-format plugin seam: an importer is resolved by the
// format it advertises — among several advertisers, by the attachment naming
// it — an attachment's inputs reach the importer it names and no other, and the
// source format is either named outright or recognised by the signatures the
// catalog's importers declare. That is a format-detection and
// plugin-selection surface, and drafting a starter document from a kind's schema
// — the other capability that produces authoring files — has no use for any of
// it: one converts something that already exists in a foreign format, the other
// starts from a schema. Sharing a package would have named it after one of two
// jobs.
//
// # The format is selected once, for every surface
//
// [SelectFormat] is the only implementation of the explicit-versus-detected
// precedence, and [Import] its only caller. A surface that wants to name a
// format sets Options.Format; a surface that wants detection leaves it empty and
// reads a refusal by its type off the error [Import] returns. That is a
// parameter on one entry point rather than a second path, because a second
// detection implementation would agree with the first on the day it was written
// and drift silently afterwards — and the two surfaces would then disagree about
// what a source file is.
//
// Detection knows no format. It reads the recognition signatures the importer
// entities of the import's catalog view record — the syntax a source is parsed
// in, the file names it applies to, the paths its parsed tree must or must not
// carry — parses the source through the parser socket and evaluates those
// predicates generically, so a format a new importer serves is recognised with
// no change here.
//
// # A source is a file or a directory
//
// [Import] reads the source itself, from the path a surface hands it after that
// surface's own rule for which paths it may name, so a file and a directory are
// told apart in one place. A file is handed to the importer whole. A directory is
// handed as named sources: the files the importer's declared file patterns select
// and no others, named by their slash-separated paths inside the directory, in
// name order, with no link followed out of it — the directory is a root of its own
// wherever it lies, judged by the workspace's one definition of inside. Which
// files an importer reads is the importer's declaration and never the host's
// guess, so an importer declaring no file pattern refuses a directory.
//
// # The write is contained
//
// A scaffold path is supplied by the importer plugin, so it is untrusted input.
// [Import] refuses a path that does not stay under the output root before it
// writes anything, through the one home for that predicate. See [Import].
//
// Each operation takes an Options value and returns a Result, and takes the
// resolved workspace as an explicit parameter rather than as a field. The
// workspace is at a named parameter so a caller that has not resolved one cannot
// omit it silently: every call site names a value there, and a nil is refused
// below rather than repaired, because resolving one here from the current
// directory would make the answer depend on a directory no caller named.
//
// A Result carries what the operation did rather than the prose describing it.
// The command line renders its own sentence from those fields and a protocol
// surface serializes them, so neither reads the other's presentation, and the
// operation itself has no opinion about which surface asked. The one sentence
// two surfaces print alike — the line naming a mapping decision, which the
// command line and the assistant's text answer both write — is composed here, by
// [DecisionLine], so it has one wording rather than one per surface.
package importops

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// PluginBytesResolver resolves a plugin URI to its module bytes through the
// shared plugin cascade.
//
// It is a seam the caller supplies rather than a cascade this package builds,
// and the reason is single-sourcing rather than testability. The cascade
// construction that serves every plugin type — the entity-fronted resolution,
// the content-cache tier and the per-URI pins the installed command-extension
// plugins carry — lives in the command layer alongside the install path, the
// listing and the runtime plugin runner, so that a URI one path accepts every
// other path resolves identically. Building a second cascade here would be a
// copy that agreed with that one on the day it was written, and the agreement is
// what would hide the first divergence.
//
// colocated is the one thing an import adds to that cascade: where each Plugin
// entity of the catalog view that selected the plugin was authored, keyed by the
// entity's artifact reference, exactly as that catalog indexed it — the
// catalog's PluginColocatedDirs, read once per import by the seam that loaded
// the catalog. A resolver hands it to the cascade's tier that reads a module
// laid down beside its entity, so an importer — and the parser it draws on —
// authored in a catalog directory runs from its catalog folder the way a build
// loads a plugin from one. It comes from the seam rather than from the resolver
// because only the catalog view that chose the plugin knows where its entity
// sits; a resolver deriving the place for itself would be a second answer to a
// question the catalog already answered. A nil map names no such place, and the
// cascade then reads only its other tiers.
//
// One plugin is not resolved through it: a core importer of the compiler's
// release, which the import's catalog view serves from what the binary carries.
// Its module is the one the binary names by digest, so it is read through a
// cascade this package builds from the workspace's content cache and that digest
// — the slot a fetch of it writes, which no pin the workspace records addresses —
// and acquired from the workspace's default registry where the machine does not
// hold it. Nothing a resolver here holds a module to, and no entity it fronts one
// with, is a statement about that module.
//
// A nil resolver is refused by [Import] rather than defaulted, because a default
// would resolve plugin bytes by a rule no caller chose.
type PluginBytesResolver func(rw *workspaceconfig.ResolvedWorkspace, uri string,
	colocated map[string]pluginpath.ColocatedDir) (pluginpath.ResolvedPlugin, error)

// workspaceOrRefuse returns the resolved workspace the caller threaded in.
//
// A nil workspace here is a reachable, user-facing condition rather than a
// wiring error: an import run outside any workspace resolves no configuration,
// so there is no catalog to select an importer from. The sentence therefore
// names what the reader has to change — being in a workspace whose catalog holds
// an importer plugin advertising the source's format, which is how an importer is
// selected — rather than reporting an internal fault.
//
// Resolving one here from the current directory would make the result depend on a
// directory no caller named, which is exactly the dependency routing every
// surface through one resolution point exists to remove.
//
// A protocol surface guards its own session before calling, so it words this
// condition for its own reader and never reaches this sentence.
func workspaceOrRefuse(rw *workspaceconfig.ResolvedWorkspace) (*workspaceconfig.ResolvedWorkspace, error) {
	if rw == nil {
		return nil, fmt.Errorf("no workspace configuration resolved; run import from within a workspace whose catalog " +
			"holds an importer plugin advertising the source's format")
	}
	return rw, nil
}
