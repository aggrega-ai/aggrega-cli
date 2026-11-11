// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/aggrega-ai/aggrega-cli/pkg/machinedirs"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// workspaceCtxKey is the private context key under which the root command's
// PersistentPreRunE stashes the lazy workspace resolver.
type workspaceCtxKey struct{}

// lazyWorkspace resolves the workspace exactly once, on first access, and
// caches the result. Resolution is deferred (not run in PersistentPreRunE) so
// commands that never need a workspace — version, logout — never trigger
// discovery and run cleanly outside any workspace. A command whose result does
// not depend on the workspace root may still read it — login reads it for the
// offline posture of its verification ping — and the root command withholds the
// warning below for every such command (withholdNoWorkspaceNotice).
type lazyWorkspace struct {
	flagRoot string
	// rootSource names where flagRoot came from — the flag as a reader types it,
	// or the variable with the desktop setting that sets it — as
	// workspaceRootFromFlagOrEnv reports it, and is empty where neither supplied
	// one. A named root that is not an existing
	// folder is refused naming it (workspaceconfig.NameRootSource), so a value the
	// reader never typed is reported against the variable that carried it.
	rootSource string
	// lookup reads the environment during resolution. It carries the --config
	// flag as the flag form of the global-config-path override, so both the
	// global layer and any config-mutating command resolve that path through the
	// same ladder. Nil selects the production environment inside Resolve.
	lookup func(string) string
	// platform is the platform whose conventions place the machine's own files —
	// the global configuration file and the machine-wide caches. It is the host
	// on every production invocation; withMachine states another for a test.
	platform machinedirs.Platform
	// flags carries the command-line layer of the precedence ladder — the
	// settings the reader set on this invocation — composed onto the resolution
	// below. It is empty on an invocation that set none, which is the ordinary
	// case, and an empty layer changes nothing.
	//
	// It also carries the --config value, which this resolution reads through
	// lookup rather than through it: both are built from that one flag at the
	// one site that reads it, and the overlay holds it so a server this
	// invocation starts is handed the command line's configuration in one value.
	flags workspaceconfig.FlagOverlay
	// errOut receives the advisories resolution produces — the CI-overlay
	// warnings and the first-run configuration notice. Nil selects the process's
	// own error stream, which is every production path; a test supplies a buffer
	// so it can read what an invocation would have shown the person at the
	// keyboard.
	errOut io.Writer
	// withholdFirstRun suppresses the first-run configuration notice for this
	// invocation. The root command sets it on the command the notice names,
	// which would otherwise be told to run what it is already running.
	withholdFirstRun bool
	// withholdNoWorkspace suppresses the warning that no workspace was found
	// (noWorkspaceNotice) for this invocation. The root command sets it for every
	// command whose result does not depend on which folder is the root
	// (warnsOutsideAWorkspace).
	withholdNoWorkspace bool

	// mu guards subject and resolved, which a command sets and the resolution
	// reads.
	mu sync.Mutex
	// subject is the Release or document the running command is about, as the
	// reader typed it, or empty for a command about neither. The resolution asks
	// the shared rule whether the workspace is found again from that file's
	// folder (workspaceconfig.ResolvedWorkspace.ForSubject), so it is named before
	// the workspace is first read (workspaceForSubject).
	subject string
	// resolved records that the resolution has run, after which the workspace
	// every reader of this invocation sees is settled.
	resolved bool

	once sync.Once
	ws   *workspaceconfig.ResolvedWorkspace
	err  error
}

// advisoryWriter is where this resolution's advisories go: the writer the
// resolver was built with, or the process's error stream when it was built with
// none. Advisories never reach standard output, which is the stream a caller
// parses.
func (l *lazyWorkspace) advisoryWriter() io.Writer {
	if l.errOut != nil {
		return l.errOut
	}
	return os.Stderr
}

func (l *lazyWorkspace) get() (*workspaceconfig.ResolvedWorkspace, error) {
	l.once.Do(func() {
		l.mu.Lock()
		subject := l.subject
		l.resolved = true
		l.mu.Unlock()
		l.ws, l.err = workspaceconfig.ResolveOn(l.platform, "", l.flagRoot, l.lookup)
		// A named root that is not an existing folder is refused by the
		// resolution itself, before anything else it does; the refusal is told
		// here which of the flag and the variable supplied it.
		l.err = workspaceconfig.NameRootSource(l.err, l.rootSource)
		if l.err == nil {
			// The flag layer is composed here, on the one resolution every
			// command reads, which is what makes it the ladder's top layer
			// rather than a knob each command would have to remember to honour.
			l.ws = l.ws.WithFlags(l.flags)
			// The command's subject is applied before anything is written, so the
			// advisories below are the ones of the workspace the command uses, and
			// a resolution set aside for the subject's says nothing.
			l.ws, l.err = l.ws.ForSubject(subject)
		}
		if l.err == nil {
			out := l.advisoryWriter()
			for _, w := range l.ws.Warnings() {
				fmt.Fprintf(out, "warning: %s\n", w)
			}
			// Written from the resolution the command uses — the subject's, where
			// the shared rule moved it — and from inside the once, so a command
			// that resolves its workspace says at most once which folder it used.
			if !l.withholdNoWorkspace {
				noWorkspaceNotice(out, l.ws)
			}
			// Inside the once, so a command body that reads its configuration
			// through several code paths shows the notice at most once. The
			// notice is written from here and nowhere else, which is what keeps
			// a process that resolves configuration again for its own reasons
			// from producing a second one.
			if !l.withholdFirstRun {
				firstRunConfigNotice(out, l.ws)
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
	platform, _ := machineFromContext(ctx)
	l := &lazyWorkspace{flagRoot: flagRoot, lookup: lookup, platform: platform}
	if len(flags) > 0 {
		l.flags = flags[0]
	}
	return context.WithValue(ctx, workspaceCtxKey{}, l)
}

// machineCtxKey is the private context key under which a caller states the
// machine an invocation runs against — its platform and its environment — in
// place of the process's own.
type machineCtxKey struct{}

// machineView is a stated machine: the platform whose conventions place the
// machine's own files, and the reader its environment is read through.
type machineView struct {
	platform machinedirs.Platform
	lookup   func(string) string
}

// withMachine returns ctx carrying a stated platform and environment for the
// command tree to resolve its configuration against, in place of the process's
// own.
//
// No production path sets it: the root command then resolves on the platform
// this program was built for, through the process environment. A test sets it
// on the context it executes the real command tree with, to run an invocation as
// a whole machine of another kind — a Windows machine's variables and
// conventions on a Linux host — without changing the process it runs in, which
// keeps such a test parallel-safe.
func withMachine(ctx context.Context, platform machinedirs.Platform, lookup func(string) string) context.Context {
	return context.WithValue(ctx, machineCtxKey{}, machineView{platform: platform, lookup: lookup})
}

// machineFromContext returns the machine stated on ctx: its platform, and the
// environment reader to build the invocation's lookup on. With none stated it
// returns the host and a nil reader, which every lookup builder here reads as
// the process environment.
func machineFromContext(ctx context.Context) (machinedirs.Platform, func(string) string) {
	if m, ok := ctx.Value(machineCtxKey{}).(machineView); ok {
		return m.platform, m.lookup
	}
	return machinedirs.Host(), nil
}

// platformFromContext returns the platform the workspace resolver attached to
// ctx resolves on, so a command that resolves its workspace off the shared
// path — the seeding command, which must not read the global file it is about
// to create — resolves on the same platform the root command's resolver would.
// It falls back to the host when no resolver is attached, which happens only
// when a command helper is invoked directly outside the root wiring (tests).
func platformFromContext(ctx context.Context) machinedirs.Platform {
	if l, ok := ctx.Value(workspaceCtxKey{}).(*lazyWorkspace); ok && l.platform != "" {
		return l.platform
	}
	return machinedirs.Host()
}

// withholdFirstRunNotice marks the resolver attached to ctx so this invocation
// shows no first-run configuration notice. It is called for the command the
// notice routes a reader to: telling someone running that command to run it is
// advice they are already taking.
//
// It marks the resolver rather than reading the command later because the
// resolver is what writes the notice and it has no command to consult; the mark
// is set before any command body runs, on the value that same wiring just
// created.
func withholdFirstRunNotice(ctx context.Context) {
	if l, ok := ctx.Value(workspaceCtxKey{}).(*lazyWorkspace); ok {
		l.withholdFirstRun = true
	}
}

// withholdNoWorkspaceNotice marks the resolver attached to ctx so this invocation
// shows no warning that no workspace was found. The root command calls it for a
// command whose result does not depend on which folder is the root
// (warnsOutsideAWorkspace) — login, say, which reads only the offline posture for
// its verification ping and stores the credential where every workspace on the
// machine reads it — so telling its reader how to make a workspace would answer a
// question the command never asks. It must be called before the workspace is
// first read, which is when the warning is written.
func withholdNoWorkspaceNotice(ctx context.Context) {
	if l, ok := ctx.Value(workspaceCtxKey{}).(*lazyWorkspace); ok {
		l.withholdNoWorkspace = true
	}
}

// refuseWorkspace settles the resolver attached to ctx as refused with err,
// before anything has read it, so every later read of this invocation's
// workspace meets err rather than resolving one — the reads that reach it as a
// function value included, such as the compiled-module cache's cap, which is
// read the first time a guest is loaded (compiledModuleCacheCap).
//
// It is for a launch that refuses its workspace for a reason the resolution
// itself would not meet, and is served all the same: the desktop application
// starting `aggrega mcp` with its Workspace folder setting empty names no root,
// and an empty root resolves the folder the process was started in, the one the
// refusal exists to keep from being taken for the workspace. A root that is not
// an existing folder needs no settling, because the resolution refuses it by
// itself. A resolver that has already run keeps what it found.
func refuseWorkspace(ctx context.Context, err error) {
	l, ok := ctx.Value(workspaceCtxKey{}).(*lazyWorkspace)
	if !ok {
		return
	}
	l.once.Do(func() {
		l.mu.Lock()
		l.resolved = true
		l.mu.Unlock()
		l.err = err
	})
}

// nameWorkspaceRootSource records on the resolver attached to ctx where its
// named root came from, as workspaceRootFromFlagOrEnv reports it, so a refusal
// of a root that is not an existing folder names the flag or the variable that
// supplied it. The root command calls it beside withWorkspaceResolver, from the
// one read of the flag and the variable.
func nameWorkspaceRootSource(ctx context.Context, source string) {
	if l, ok := ctx.Value(workspaceCtxKey{}).(*lazyWorkspace); ok {
		l.rootSource = source
	}
}

// workspaceRootSourceFromContext returns where the resolver's named root came
// from — the flag or the variable — for a command that resolves its workspace off
// the shared path and still has to name it in a refusal, as workspaceRootFromContext
// returns the root itself. Empty where nothing named a root or no resolver is
// attached.
func workspaceRootSourceFromContext(ctx context.Context) string {
	if l, ok := ctx.Value(workspaceCtxKey{}).(*lazyWorkspace); ok {
		return l.rootSource
	}
	return ""
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

// flagOverlayFromContext returns the command-line layer the workspace resolver
// carries: the global configuration file named with --config and the settings
// the reader set on this invocation, read once by the root command and stashed
// there.
//
// It exists for a command that resolves a workspace other than the one this
// invocation's resolver holds. A server of its own resolves a workspace for each
// request, and the seeding command resolves one without the global layer it is
// about to write; each lays this same value over what it resolves — the value the
// root command composed, not the flags read a second time. It returns the empty layer when no resolver is attached,
// which happens only when a command helper is invoked directly outside the root
// wiring (tests), and the empty layer changes nothing.
func flagOverlayFromContext(ctx context.Context) workspaceconfig.FlagOverlay {
	if l, ok := ctx.Value(workspaceCtxKey{}).(*lazyWorkspace); ok {
		return l.flags
	}
	return workspaceconfig.FlagOverlay{}
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

// workspaceForSubject is workspaceFromContext for a command about one Release or
// one document: subject is that argument as the reader typed it, empty where the
// invocation names none. Where nothing named a root, the folder the command runs
// in is no workspace and subject is an absolute path, the workspace is the one
// that file's folder resolves; otherwise it is the one workspaceFromContext
// returns. Which of the two is decided by the shared rule
// (workspaceconfig.ResolvedWorkspace.ForSubject), not here.
//
// A command calls it in place of workspaceFromContext, before anything reads the
// workspace, and every later read in the same invocation — through either
// function, from any code path — returns the same workspace. Naming a subject
// after the workspace has been read is a wiring error, since a part of the run
// would already have used a workspace other than the subject's, so it is
// reported rather than ignored.
func workspaceForSubject(ctx context.Context, subject string) (*workspaceconfig.ResolvedWorkspace, error) {
	l, ok := ctx.Value(workspaceCtxKey{}).(*lazyWorkspace)
	if !ok {
		return nil, fmt.Errorf("internal: workspace resolver not attached to command context")
	}
	l.mu.Lock()
	late := l.resolved && l.subject != subject
	if !l.resolved {
		l.subject = subject
	}
	l.mu.Unlock()
	if late {
		return nil, fmt.Errorf("internal: the workspace was read before the command named the file it is about")
	}
	return l.get()
}
