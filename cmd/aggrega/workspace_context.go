// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"os"
	"strings"
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
	once     sync.Once
	ws       *workspaceconfig.ResolvedWorkspace
	err      error
}

func (l *lazyWorkspace) get() (*workspaceconfig.ResolvedWorkspace, error) {
	l.once.Do(func() {
		l.ws, l.err = workspaceconfig.Resolve("", l.flagRoot)
		if l.err == nil {
			if files := l.ws.ContributingFiles(); len(files) > 0 {
				fmt.Fprintf(os.Stderr, "loaded config from: %s\n", strings.Join(files, ", "))
			}
		}
	})
	return l.ws, l.err
}

// withWorkspaceResolver attaches a lazy workspace resolver carrying the
// --workspace-root flag value to ctx. The root command's PersistentPreRunE
// installs it so every command body can obtain the resolved workspace through
// the single chokepoint.
func withWorkspaceResolver(ctx context.Context, flagRoot string) context.Context {
	return context.WithValue(ctx, workspaceCtxKey{}, &lazyWorkspace{flagRoot: flagRoot})
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
