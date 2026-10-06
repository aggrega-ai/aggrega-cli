// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"errors"
	"path/filepath"
	"slices"

	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
)

// HeldWorkspace is a workspace whose configuration is held: a configuration file
// its cascade merges — a directory's own configuration file, or the CI-owned
// overlay beside it — is a link leading out of the workspace, so the
// configuration is not read at all.
//
// It carries no configuration, and that is what the type is for. Every operation
// whose answer the configuration decides — a build, a graph, a validation, a
// pull, a write — halts on such a workspace with the hold's refusal
// ([HeldWorkspace.Refusal]), exactly as the strict resolution halts. What a held
// workspace still answers is a listing of what it holds, read with nothing
// configured: the listing names every held file among the files it did not read
// ([HeldWorkspace.NamedWith]), which is how a client learns the configuration file
// leads out of the workspace without reading a refusal's prose. Such a listing
// reads no setting, because there is none to read, rather than a default standing
// in for one.
//
// It is obtained only from [ResolveOrHold], which a surface asks only where it is
// answering a listing.
type HeldWorkspace struct {
	root    string
	marker  Marker
	files   []string
	refusal error
}

// Root returns the absolute workspace root.
func (h *HeldWorkspace) Root() string { return h.root }

// Marker returns what anchored the root, as [ResolvedWorkspace.Marker] does.
func (h *HeldWorkspace) Marker() Marker { return h.marker }

// Files returns the configuration files held, absolute and in the order the
// cascade met them.
func (h *HeldWorkspace) Files() []string { return slices.Clone(h.files) }

// Refusal returns the refusal the strict resolution of the same workspace
// returns: the read hold naming the first held file and the link it leads out
// through. It is what every operation the configuration decides answers with.
func (h *HeldWorkspace) Refusal() error { return h.refusal }

// NamedWith returns unread — the links a listing's walk named as not read, each
// relative to the workspace root — with every held configuration file added after
// them, in the walk's own spelling, where the walk did not already name it. The
// walk names such a file when it meets it, since it is a link to a file outside
// the workspace; adding it here is what keeps a listing truthful about the
// configuration where the walk did not meet it, and naming it twice would report
// one link as two. Never nil.
func (h *HeldWorkspace) NamedWith(unread []string) []string {
	out := append([]string{}, unread...)
	root := absoluteConfigPath(h.root)
	for _, file := range h.files {
		rel, inside := pathscope.Relative(root, absoluteConfigPath(file))
		if !inside || slices.Contains(out, rel) {
			continue
		}
		out = append(out, rel)
	}
	return out
}

// ResolveOrHold resolves the workspace exactly as [ResolveWithFlags] does, and
// where it cannot be resolved only because configuration files its cascade merges
// lead out of the workspace, returns the workspace's held view in place of that
// refusal: the root, what anchored it, every held file and the refusal itself.
// Any other failure is returned as [ResolveWithFlags] returns it, a held file
// beside it included, because a configuration that is broken in another way as
// well is not one a listing can stand beside.
//
// The held view is built by the one resolution, run again with the hold recorded
// rather than halted on, so the files it names are the files the cascade meets,
// judged by the judgement it meets them with, and nothing held is read or used.
//
// A surface asks this only for the requests that list a workspace's content —
// the entity index and the Release listing, which name what they did not read —
// and answers every other request on a held workspace with its refusal. A request
// a configuration decides that were handed the held view would run on nothing
// configured while reading as though it ran on the configuration, which is the
// substitution the hold exists to refuse.
func ResolveOrHold(start, flagRoot string, f FlagOverlay) (*ResolvedWorkspace, *HeldWorkspace, error) {
	rw, err := ResolveWithFlags(start, flagRoot, f)
	if err == nil {
		return rw, nil, nil
	}
	if !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
		return nil, nil, err
	}
	hold := &configurationHold{}
	resolved, herr := resolve(start, flagRoot, f.Lookup(nil), false, hold)
	if herr != nil {
		return nil, nil, herr
	}
	if resolved != nil || !hold.holds() {
		// The strict refusal came from no configuration file: there is nothing to
		// hold, so it stands as the answer.
		return nil, nil, err
	}
	return nil, &HeldWorkspace{root: hold.root, marker: hold.marker, files: hold.files, refusal: err}, nil
}

// configurationHold is what a resolution keeping a held view records: the
// configuration files it met leading out of the workspace, and the root and
// marker of the workspace it was resolving.
type configurationHold struct {
	files  []string
	root   string
	marker Marker
}

// record notes a configuration file the resolution passes over unread.
func (h *configurationHold) record(path string) { h.files = append(h.files, path) }

// holds reports whether the resolution recorded any file. A nil hold is a strict
// resolution, which records none.
func (h *configurationHold) holds() bool { return h != nil && len(h.files) > 0 }

// absoluteConfigPath is p made absolute against the working directory and
// cleaned.
func absoluteConfigPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return filepath.Clean(p)
}
