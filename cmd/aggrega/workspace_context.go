// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// workspaceCtxKey is the private context key under which the root command's
// PersistentPreRunE stashes the lazy workspace resolver.
type workspaceCtxKey struct{}

// lazyWorkspace resolves the workspace exactly once, on first access, and
// caches the result. Resolution is deferred (not run in PersistentPreRunE) so
// commands that never need a workspace — version, login, logout — never
// trigger discovery and run cleanly outside any workspace.
type lazyWorkspace struct {
	flagRoot string
	// lookup reads the environment during resolution. It carries the --config
	// flag as the flag form of the global-config-path override, so both the
	// global layer and any config-mutating command resolve that path through the
	// same ladder. Nil selects the production environment inside Resolve.
	lookup func(string) string
	once   sync.Once
	ws     *workspaceconfig.ResolvedWorkspace
	err    error
}

func (l *lazyWorkspace) get() (*workspaceconfig.ResolvedWorkspace, error) {
	l.once.Do(func() {
		l.ws, l.err = workspaceconfig.Resolve("", l.flagRoot, l.lookup)
		if l.err == nil {
			for _, w := range l.ws.Warnings() {
				fmt.Fprintf(os.Stderr, "warning: %s\n", w)
			}
		}
	})
	return l.ws, l.err
}

// withWorkspaceResolver attaches a lazy workspace resolver carrying the
// --workspace-root flag value and the flag-aware environment lookup to ctx. The
// root command's PersistentPreRunE installs it so every command body can obtain
// the resolved workspace — and the same lookup — through the single chokepoint.
func withWorkspaceResolver(ctx context.Context, flagRoot string, lookup func(string) string) context.Context {
	return context.WithValue(ctx, workspaceCtxKey{}, &lazyWorkspace{flagRoot: flagRoot, lookup: lookup})
}

// configLookupFromContext returns the flag-aware environment lookup carried by
// the workspace resolver, so a config-mutating command resolves the global
// config path through the same --config-flag-then-environment ladder the
// workspace resolution uses, without re-reading the flag. It falls back to the
// live process environment when no resolver is attached or none was set, which
// happens only when a command helper is invoked directly outside the root
// wiring (tests).
func configLookupFromContext(ctx context.Context) func(string) string {
	if l, ok := ctx.Value(workspaceCtxKey{}).(*lazyWorkspace); ok && l.lookup != nil {
		return l.lookup
	}
	return os.Getenv
}

// workspaceRootFromContext returns the resolved --workspace-root value (flag or
// environment) carried by the workspace resolver, so a config-mutating command
// that resolves the workspace off the shared path still pins the exact root the
// root command's resolver would. Empty lets discovery proceed. It falls back to
// empty when no resolver is attached, which happens only when a command helper
// is invoked directly outside the root wiring (tests).
func workspaceRootFromContext(ctx context.Context) string {
	if l, ok := ctx.Value(workspaceCtxKey{}).(*lazyWorkspace); ok {
		return l.flagRoot
	}
	return ""
}

// workspaceOrResolve returns ws when set — the production path, where a
// command's RunE threads in the context-resolved workspace. When ws is nil it
// resolves from the current directory; that path is reachable only from tests
// that invoke the command helpers directly (production RunE always passes the
// context-resolved workspace, asserted by the guard test). Centralizing the
// branch keeps the test-only fallback in exactly one place.
func workspaceOrResolve(ws *workspaceconfig.ResolvedWorkspace) (*workspaceconfig.ResolvedWorkspace, error) {
	if ws != nil {
		return ws, nil
	}
	return workspaceconfig.Resolve("", "")
}

// workspaceFromContext resolves (once) and returns the workspace for the
// running command. It errors loudly when no resolver is attached — that means
// the command ran outside the root command's wiring, a programming error
// rather than a user-facing condition, so it must never be silently treated
// as "no workspace."
func workspaceFromContext(ctx context.Context) (*workspaceconfig.ResolvedWorkspace, error) {
	l, ok := ctx.Value(workspaceCtxKey{}).(*lazyWorkspace)
	if !ok {
		return nil, fmt.Errorf("internal: workspace resolver not attached to command context")
	}
	return l.get()
}
