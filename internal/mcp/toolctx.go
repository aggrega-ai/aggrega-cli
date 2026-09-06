// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"fmt"
	"path/filepath"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// toolWorkspace is the per-call resolved workspace context the closure-driven
// tools build their compiler-operations inputs from. Every field is derived
// once per tool call from the call's workspace_root and catalog_dirs arguments
// (or the process working directory when absent); the server holds no cached
// workspace, so two concurrent calls never share resolution state.
type toolWorkspace struct {
	rw       *workspaceconfig.ResolvedWorkspace
	root     string
	cfg      workspaceconfig.Config
	cacheDir string
	dirs     []string
}

// resolveToolWorkspace resolves the workspace a tool call operates in. An
// explicit rootArg pins the workspace root; otherwise resolution starts from
// the server's configured start directory, which is the process working
// directory in production. catalogDirs are the call's catalog search
// directories, prepended (absolute) to the workspace's configured stack; an
// empty result falls back to the workspace root so a catalog beside the release
// still resolves.
func (s *server) resolveToolWorkspace(rootArg string, catalogDirs []string) (*toolWorkspace, error) {
	start := rootArg
	flagRoot := rootArg
	if start == "" {
		start = s.startDir
	}
	rw, err := workspaceconfig.Resolve(start, flagRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace: %w", err)
	}
	cfg := rw.Config()
	root := rw.Root()
	tw := &toolWorkspace{
		rw:       rw,
		root:     root,
		cfg:      cfg,
		cacheDir: cfg.ResolvedArtifactCacheDir(),
		dirs:     workspaceconfig.ResolveCatalogDirs(catalogDirs, cfg, root),
	}
	return tw, nil
}

// newView builds the request-scoped catalog lookup for a tool call: a lazy
// lookup over the workspace's catalog search stack with a nil puller, so the
// build never networks — a cache miss is a loud halt rather than a registry
// fetch. When a lockfile is present its pins are threaded so every resolution
// is verified-on-read against the pinned digests, matching the non-daemon build
// front door.
func (tw *toolWorkspace) newView(lf *lockfile.Lockfile) *catalog.LazyLookup {
	v := catalog.NewLazyLookup(tw.root, tw.cacheDir, tw.dirs, nil)
	if lf != nil {
		v.SetPins(catalog.PinsFromLockfile(lf))
	}
	return v
}

// absWorkspacePath resolves a path a tool call named to an absolute path. A
// relative path is taken against the workspace root, so a call can name its
// subject by a workspace-relative path or an absolute one interchangeably.
//
// It is the one home of that contract for every path argument on this surface —
// a Release, an entity document, a plugin executable, an artifact file, a batch
// file. The contract is deliberately the same for all of them: a caller here
// names the workspace it means on the call itself, so a path outside that root is
// something it can address rather than something it has strayed into, and a rule
// that admitted an absolute Release while refusing an absolute entity would be
// one no caller could predict from the rest of this surface.
func (tw *toolWorkspace) absWorkspacePath(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(tw.root, p)
}

// loadReleaseLockfile loads the per-Release lockfile beside releasePath,
// best-effort: an absent or unreadable lockfile yields nil, and the caller
// builds a view with no pins. The MCP tools never auto-pull — a build or
// validate resolves against whatever pins exist, hermetically.
func loadReleaseLockfile(releasePath string) *lockfile.Lockfile {
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		return nil
	}
	return lf
}
