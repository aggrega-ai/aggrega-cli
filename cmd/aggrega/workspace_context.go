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
	// flags carries the command-line layer of the precedence ladder — the
	// settings the reader set on this invocation — composed onto the resolution
	// below. It is empty on an invocation that set none, which is the ordinary
	// case, and an empty layer changes nothing.
	flags workspaceconfig.FlagOverlay
	once  sync.Once
	ws    *workspaceconfig.ResolvedWorkspace
	err   error
}

func (l *lazyWorkspace) get() (*workspaceconfig.ResolvedWorkspace, error) {
	l.once.Do(func() {
		l.ws, l.err = workspaceconfig.Resolve("", l.flagRoot, l.lookup)
		if l.err == nil {
			// The flag layer is composed here, on the one resolution every
			// command reads, which is what makes it the ladder's top layer
			// rather than a knob each command would have to remember to honour.
			l.ws = l.ws.WithFlags(l.flags)
			for _, w := range l.ws.Warnings() {
				fmt.Fprintf(os.Stderr, "warning: %s\n", w)
			}
		}
	})
	return l.ws, l.err
}

// withWorkspaceResolver attaches a lazy workspace resolver carrying the
// --workspace-root flag value, the flag-aware environment lookup, and
// optionally the command-line layer of the precedence ladder to ctx. The root
// command's PersistentPreRunE installs it so every command body can obtain the
// resolved workspace — and the same lookup — through the single chokepoint.
//
// The flag layer is variadic for the same reason Resolve's lookup is: it is an
// input only one kind of caller has. A command line fills it from what the
// reader typed; a caller with no command line — the daemon's own resolution, a
// test driving a command body directly — omits it and resolves exactly the
// cascade it would have resolved anyway. Omitted and empty mean the same thing,
// so there is no third state for a caller to get wrong.
func withWorkspaceResolver(ctx context.Context, flagRoot string, lookup func(string) string, flags ...workspaceconfig.FlagOverlay) context.Context {
	l := &lazyWorkspace{flagRoot: flagRoot, lookup: lookup}
	if len(flags) > 0 {
		l.flags = flags[0]
	}
	return context.WithValue(ctx, workspaceCtxKey{}, l)
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

// workspaceOrResolve returns the workspace a command's RunE threaded in from the
// shared resolution chokepoint. A nil workspace is a wiring error — the call was
// reached without any caller having resolved one — so it is reported rather than
// repaired. Resolving here from the current directory would make the result
// depend on a directory no caller named, which is exactly the dependency routing
// every command through one resolution point exists to remove.
//
// The structural guard over this package rejects the same mistake in production
// code at parse time, where the diagnostic can name the offending site, and it
// takes three checks rather than one, because a nil reaches this call by three
// different routes: written here as a literal, left out of a command config that
// carries the resolved workspace as a field, or handed to a helper that threads
// its own workspace parameter down to here. The third route is the one a check on
// this call alone cannot see, since every caller passes a named value at this
// point and the literal sits a hop earlier. The refusal below is what covers all
// three at run time, for any caller.
func workspaceOrResolve(ws *workspaceconfig.ResolvedWorkspace) (*workspaceconfig.ResolvedWorkspace, error) {
	if ws == nil {
		return nil, fmt.Errorf("internal: no resolved workspace supplied — the caller must resolve one and pass it in")
	}
	return ws, nil
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
