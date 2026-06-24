// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package pluginpath is the single home for resolving a Wasm-plugin URI to
// the filesystem path of its .wasm bytes. Every socket that loads a plugin —
// the publishing-linter, and the render pipeline's per-step plugins — resolves
// through the same Resolve function, so the resolution logic exists exactly
// once. Only the URI SOURCE differs per socket (workspace config for the
// linter; a recipe's spec.pipeline[].plugin for pipeline steps); that
// per-socket field-reading stays at each caller and feeds this resolver.
//
// Resolution handles local/relative paths and an explicit filesystem override.
// Registry-pinned (OCI) URIs are rejected uniformly with a not-yet-wired
// diagnostic from one site, until OCI plugin loading lands.
package pluginpath

import (
	"fmt"
	"path/filepath"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// Lookup resolves a plugin URI to a .wasm filesystem path. It is the seam a
// socket binds when it has no explicit override (the render pipeline binds
// `func(uri string) (string, error) { return Resolve(uri, "") }`); the linter
// calls Resolve directly so it can pass its --plugin override.
type Lookup func(uri string) (string, error)

// ociNotWiredMsg is the single-sourced rejection for a registry (OCI) plugin
// URI. The substring "OCI plugin URI resolution not yet wired" is load-bearing:
// callers and tests match on it.
const ociNotWiredMsg = "plugin URI %q is an OCI reference; OCI plugin URI resolution not yet wired. Either:\n" +
	"  - pass an explicit filesystem path\n" +
	"  - or set the plugin URI to a workspace-relative path until OCI loading lands\n" +
	"(standard URI for the publishing-linter: hub.aggrega.ai/wasm/linter/aggrega-publishing:1.0.0)"

// unsetMsg is the single-sourced diagnostic for the no-URI / no-override case.
// The substring "no plugin path configured" is load-bearing: callers and tests
// match on it.
const unsetMsg = "no plugin path configured. Either:\n" +
	"  - pass an explicit filesystem path\n" +
	"  - or set the plugin URI in .aggrega-config.yaml to a workspace-relative path"

// Resolve maps a plugin URI to the filesystem path of its .wasm bytes.
//
// override, when non-empty, is an explicit filesystem path that wins over uri
// (the linter's --plugin flag). It is returned verbatim when already absolute,
// otherwise made absolute against the working directory — it carries no
// declaring file to anchor against.
//
// uri is the per-socket plugin reference. A local/relative URI is returned
// verbatim: workspace-config URIs arrive already absolute (the config cascade
// anchored them against their declaring file at load), and pipeline-step URIs
// arrive already resolved by their own socket — Resolve treats uri as an
// opaque resolved location and does not re-base it. An OCI URI is rejected
// uniformly (see ociNotWiredMsg). With neither override nor uri, the unset
// diagnostic is returned.
func Resolve(uri, override string) (string, error) {
	if override != "" {
		if filepath.IsAbs(override) {
			return override, nil
		}
		abs, err := filepath.Abs(override)
		if err != nil {
			return "", fmt.Errorf("resolve plugin path %q: %w", override, err)
		}
		return abs, nil
	}
	if uri != "" {
		if workspaceconfig.LooksLikeOCIURI(uri) {
			return "", fmt.Errorf(ociNotWiredMsg, uri)
		}
		return uri, nil
	}
	return "", fmt.Errorf(unsetMsg)
}
