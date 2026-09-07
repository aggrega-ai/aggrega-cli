// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package configops holds the workspace-configuration operations every
// caller-facing surface reaches: seeding a working configuration with a registry
// and its signature trust policy, and freezing the effective build-critical
// configuration into a workspace so the workspace builds the same without the
// machine's global configuration underneath it.
//
// The package exists because the logic these operations carry used to sit in the
// command-line package, which is package main and therefore unimportable by any
// other package — a language rule rather than a convention. A surface that is not
// the command line could not reach the code even though the code was right there.
//
// One package carries both rather than one package per operation. They are one
// domain that happens to expose two verbs: both resolve a configuration file to
// write, both write the configuration the workspace resolved, and both answer
// with the file they landed in and how far that file reaches. Two packages would
// either duplicate that vocabulary — the dis-unification a second copy always is,
// invisible for exactly as long as the copies agree — or force a shared package
// anyway.
//
// Reporting the effective configuration is deliberately NOT here. It shares no
// helper with either operation and it changes nothing, so lifting it would move
// code no second surface was waiting for; the daemon already answers that
// question from the resolution it holds.
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
// operation itself has no opinion about which surface asked.
package configops

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// workspaceOrRefuse returns the resolved workspace the caller threaded in.
//
// A nil workspace is a wiring error — the call was reached without any caller
// having resolved one — so it is reported rather than repaired. Resolving here
// from the current directory would make the result depend on a directory no
// caller named, which is exactly the dependency routing every surface through one
// resolution point exists to remove.
func workspaceOrRefuse(rw *workspaceconfig.ResolvedWorkspace) (*workspaceconfig.ResolvedWorkspace, error) {
	if rw == nil {
		return nil, fmt.Errorf("internal: no resolved workspace supplied — the caller must resolve one and pass it in")
	}
	return rw, nil
}
