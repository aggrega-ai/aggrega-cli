// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// WorkspaceRootFlagName is the long name, without its leading dashes, of the
// global flag naming the workspace root: the command line's form of
// EnvWorkspaceRoot, which it wins over. It is held here, beside the sentences
// below that name it, so the flag's declaration and every sentence telling a
// reader to pass it spell it from one place.
const WorkspaceRootFlagName = "workspace-root"

// WorkspaceRootArgument is the argument an assistant's call names its workspace
// root with, spelled as the caller spells it. It is held here for the reason
// WorkspaceRootFlagName is: the refusal below names it to a caller of either
// surface, and the surface that reads the argument spells it from this one place.
const WorkspaceRootArgument = "workspace_root"

// FoundNoWorkspace reports whether this resolution found no workspace and was
// handed none: no marker anchored a root, and the folder the resolution started
// from is used as the root.
//
// That is the one predicate behind what every surface says or refuses about such
// a resolution — the notice naming the folder used, and the refusal of a write
// into the artifact cache there ([ResolvedWorkspace.CacheWriteRefusal]) — and it
// reads the marker and nothing else. A root somebody named — --workspace-root,
// AGGREGA_WORKSPACE_ROOT, a call's workspace_root, the root a server was launched
// with — reaches the resolution as its explicit root and is reported as
// MarkerNone, so the marker alone also answers whether anything named a root;
// testing the explicit root beside it would be a second reading of the same fact.
//
// It is asked of the resolution a run actually uses: where the shared rule found
// the workspace again from the folder of the file a run is about
// ([ResolvedWorkspace.ForSubject]), it is that resolution's answer, so a run whose
// subject's folder anchors a workspace is not reported as having found none.
func (r *ResolvedWorkspace) FoundNoWorkspace() bool {
	return r != nil && r.marker == MarkerCWD
}

// CacheWriteRefusal returns the refusal of a write into the artifact cache for a
// resolution that found no workspace and was handed none ([ResolvedWorkspace.FoundNoWorkspace]),
// and nil for every other resolution.
//
// The cache is joined to the workspace root, and the folder such a resolution
// uses as its root is one nobody chose: a cache written there would leave a
// directory the next resolution reads as a workspace marker, so a folder becomes
// a workspace because something was once fetched in it. The write is refused
// instead, with the way out in the refusal: name the workspace, or mark the
// folder as one. Where the cache directory sits beneath that root changes
// nothing, because it is joined to whichever root the resolution found.
//
// It is the one home of that refusal. Each operation whose work fills the cache
// asks it before it fetches anything, so nothing is fetched for a write that
// cannot land; nothing that only reads the cache asks it.
func (r *ResolvedWorkspace) CacheWriteRefusal() error {
	if !r.FoundNoWorkspace() {
		return nil
	}
	return &CacheWriteRefusedError{root: r.root}
}

// CacheWriteRefusedError is the refusal [ResolvedWorkspace.CacheWriteRefusal]
// returns: a write into the artifact cache was asked for in a folder that is no
// workspace while nothing named one. Its message is one sentence naming the
// folder and the two ways out. Match it with errors.As.
type CacheWriteRefusedError struct{ root string }

func (e *CacheWriteRefusedError) Error() string {
	return fmt.Sprintf("no Aggrega workspace was found from %s and none was named, so nothing is written to an "+
		"artifact cache there: name your workspace with --%s or %s, or with %s on an assistant's call, or make %s "+
		"a workspace by giving it a %s that carries a workspace: block",
		e.root, WorkspaceRootFlagName, EnvWorkspaceRoot, WorkspaceRootArgument, e.root, ConfigFilename)
}

// Root is the folder the refused write would have written its cache beneath.
func (e *CacheWriteRefusedError) Root() string { return e.root }

// NamedRootDir returns, as an absolute path, the root given names, where it is
// an existing folder, and the [NamedRootError] refusal otherwise.
//
// It is the one judgement of whether a root somebody named can be the workspace
// root. The explicit-root branch of every resolution asks it, so a named root
// that does not exist is refused before anything else a resolution does; a
// server's launch asks it before the server starts, and a request naming its
// root asks it before that root is resolved. given is the root as it was given,
// and it is what the refusal quotes.
func NamedRootDir(given string) (string, error) {
	abs, err := filepath.Abs(given)
	if err != nil {
		return "", &NamedRootError{given: given, cause: err}
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "", &NamedRootError{given: given, abs: abs, cause: err}
	}
	if !st.IsDir() {
		return "", &NamedRootError{given: given, abs: abs}
	}
	return abs, nil
}

// NamedRootError reports that a root somebody named is not an existing folder,
// so it cannot be the workspace root.
//
// Its message quotes the root exactly as it was given, between plain quotes and
// with nothing escaped, so a path reads as its owner typed it and a stray space
// at either end stays visible: a root is often composed by a client rather than
// typed, so the refusal reports what arrived. Where the surface that read the
// root has named where it came from ([NamedRootError.NamedBy]) the message opens
// with that — the flag, the variable, a call's argument or a server's launch.
// It unwraps to the reason the filesystem gave, where it gave one.
type NamedRootError struct {
	given  string
	abs    string
	cause  error
	source string
}

func (e *NamedRootError) Error() string {
	subject := fmt.Sprintf(`the workspace root "%s"`, e.given)
	if e.source != "" {
		subject = fmt.Sprintf(`%s names the workspace root "%s",`, e.source, e.given)
	}
	switch {
	case e.cause != nil && e.source != "":
		return fmt.Sprintf("%s which cannot be used: %v", subject, e.cause)
	case e.cause != nil:
		return fmt.Sprintf("%s cannot be used: %v", subject, e.cause)
	case e.source != "":
		return fmt.Sprintf("%s which is not a directory: %s", subject, e.abs)
	default:
		return fmt.Sprintf("%s is not a directory: %s", subject, e.abs)
	}
}

// Unwrap returns the reason the filesystem gave, or nil where the root exists
// and is not a folder.
func (e *NamedRootError) Unwrap() error { return e.cause }

// Given is the root exactly as it was given.
func (e *NamedRootError) Given() string { return e.given }

// NotADirectory reports that the root exists and is not a folder.
func (e *NamedRootError) NotADirectory() bool { return e.cause == nil }

// NamedBy returns the refusal opening with source, the name of where the root
// came from as its reader would recognise it.
func (e *NamedRootError) NamedBy(source string) *NamedRootError {
	named := *e
	named.source = source
	return &named
}

// NameRootSource returns err with the refusal of a named root that is not an
// existing folder opening with source, where err is that refusal, and err
// unchanged otherwise — a nil err and an empty source included.
//
// A surface calls it on the error a resolution returned, with the name of
// whatever supplied the root it handed that resolution, so the refusal names the
// flag, the variable or the argument the reader has to correct.
func NameRootSource(err error, source string) error {
	var named *NamedRootError
	if source == "" || !errors.As(err, &named) {
		return err
	}
	return named.NamedBy(source)
}

// EmptyWorkspaceFolderError reports that the desktop application launched a
// server with its Workspace folder setting ([DesktopWorkspaceFolderSetting])
// empty, so [EnvWorkspaceRoot] names no workspace root.
//
// Everywhere else an empty EnvWorkspaceRoot is read as unset and the workspace is
// found from the folder the process was started in. The desktop application
// starts its server in a folder of its own choosing, often a system folder that
// is no workspace, so on that launch an empty setting is a mistake to name rather
// than a folder to search from: the refusal names the setting, the one thing the
// person reading it in the application can change, and the folder to choose.
// Which launch that is, is decided where a server is launched; this type holds
// the sentence alone, composed from the names the desktop bundle's manifest is
// held to, so every surface answering the refusal reads one sentence. Match it
// with errors.As.
type EmptyWorkspaceFolderError struct{}

func (*EmptyWorkspaceFolderError) Error() string {
	return fmt.Sprintf("the desktop application launched this server with its %s setting empty, so %s names no "+
		"workspace root: set %s to the folder that holds your workspace's %s",
		DesktopWorkspaceFolderSetting, EnvWorkspaceRoot, DesktopWorkspaceFolderSetting, ConfigFilename)
}
