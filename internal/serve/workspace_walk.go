// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/sourcewalk"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// walkWorkspaceYAML invokes visit once per YAML file under the
// workspace root, honoring the standard exclusion list and any
// `.gitignore` files found along the ancestor chain (root + nested).
//
// The standard exclusion list skips:
//   - any path component whose name begins with '.' (covers .git,
//     .aggrega, .trace, hidden tooling directories)
//   - node_modules, vendor, dist
//
// `.gitignore` semantics: every `.gitignore` from the file's
// containing directory up to the workspace root is consulted, in
// deepest-first order (the closest file wins per gitignore precedence
// rules — including negation patterns within each file). Per-directory
// matchers are cached across the walk to avoid re-parsing.
//
// Errors returned by visit halt the walk and propagate.
// filepath.WalkDir does not follow symlinks by default, matching the
// IDE's expectation that the workspace tree is a finite acyclic
// search domain.
//
// visit is handed two paths for each file: path, the file's place in the
// workspace, which is how it is named; and read, the file whose bytes are
// read for it. They differ only for a link to a file inside the root, which
// is read through the file its one resolution reported — see
// walkWorkspaceYAMLIn for what the walk does with a link.
//
// The walk checks ctx.Err() at each directory entry so a shutdown
// cancellation interrupts a long workspace traversal between files
// rather than running it to completion.
func (s *Server) walkWorkspaceYAML(ctx context.Context, visit func(path, read string) error) error {
	_, err := walkWorkspaceYAMLIn(ctx, s.workspaceRoot, s.wsConfig, visit)
	return err
}

// unreadLinks are the links a walk of the workspace met that lead out of the
// workspace root and that it therefore did not read, each named relative to the
// root where the link itself sits — never by the place it leads to — in the order
// the walk met them.
type unreadLinks struct {
	// dirs are the links leading to a directory outside the root.
	dirs []string
	// files are the links named like a YAML file leading to a file outside the
	// root other than a Release the workspace's configuration names.
	files []string
}

// walkWorkspaceYAMLIn is the whole of walkWorkspaceYAML with the root and the
// workspace's configuration supplied rather than read off a session, together
// with the links leading out of the workspace the walk met and did not read.
//
// The split is what lets a caller holding no session run the same traversal.
// The session-bound form above is a one-line delegation passing its own root and
// configuration, so there is one walk rather than two that agree until one of
// them learns about an exclusion the other does not.
//
// cfg is read for the Releases it names, and for nothing else.
//
// # A link is judged where the walk meets it
//
// The walk never enters a link. A link is judged by the one resolution
// resolveLink makes of it, against the root resolved the same way — the same
// judgement the Release walk applies — and nothing outside the workspace root is
// read through one but a Release the workspace's configuration names:
//
//   - A link to a directory outside the root is not searched, and is recorded,
//     unless a directory there would be left out — by its name or by a
//     .gitignore — in which case it is not, just as a directory there would not be
//     read. A link to a directory inside the root needs nothing more, because
//     that directory is walked, or left out, where it actually sits.
//   - A link named like a YAML file that leads to a file outside the root is not
//     read, and is recorded, unless a file there would be left out by a
//     .gitignore. The one exception is a Release the workspace's configuration
//     names — see readsConfiguredRelease — which the configuration has reached:
//     it is read at the link's place, through the file the link's resolution
//     reported, and not recorded, as the Release walk reads it. Every other file
//     outside the root, whatever it declares, is not read through a link; the
//     configuration's catalog directories are how other entities outside the
//     workspace are read.
//   - A link named like a YAML file that leads to a file inside the root is read
//     at the link's place, through the file its resolution reported, so the bytes
//     read are those of the file judged; that file is read where it actually sits
//     as well.
//   - A link that cannot be resolved — leading nowhere, or round a loop — or that
//     leads to anything but a directory or a regular file, is neither read nor
//     recorded, and nothing further is tried to find out where it was meant to go.
//
// A link inside a directory the walk leaves out is never met at all.
func walkWorkspaceYAMLIn(ctx context.Context, root string, cfg workspaceconfig.Config, visit func(path, read string) error) (unreadLinks, error) {
	w := newGitignoreWalker(root)
	scope := linkScope{root: root, cfg: cfg}
	var unread unreadLinks
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if path == root {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if excludedDirName(name) {
				return filepath.SkipDir
			}
			if w.matchesAny(path, true) {
				return filepath.SkipDir
			}
			return nil
		}
		var linked linkTarget
		link := d.Type()&fs.ModeSymlink != 0
		if link {
			scope.resolve()
			linked = resolveLink(path, scope.root)
			if linked.dir {
				if linked.outside && !excludedDirName(name) && !w.matchesAny(path, true) {
					unread.dirs = scope.named(unread.dirs, path)
				}
				return nil
			}
		}
		if !isYAMLFile(name) {
			return nil
		}
		if w.matchesAny(path, false) {
			return nil
		}
		read := path
		if link {
			if !linked.file {
				return nil
			}
			if linked.outside && !readsConfiguredRelease(&scope, linked.target) {
				unread.files = scope.named(unread.files, path)
				return nil
			}
			read = linked.target
		}
		return visit(path, read)
	})
	return unread, err
}

// readsConfiguredRelease reports whether the entity walk reads the file at target —
// a file a link leads to outside the workspace root, absolute with its links
// resolved — all the same: the workspace's configuration names it as a Release,
// by configurationNames, the one judgement the Release walk makes of the same
// link, and the file declares a Release, its kind read as every workspace-entity
// projection reads a document's kind.
//
// Both halves are asked, the configuration first. The configuration naming a file
// is what reaches it, so a file it does not name is never opened to find out what
// it declares; and the settings that name it are a Release's, so a file named
// there that declares anything else is not one of the workspace's documents
// either, and is recorded as any other file outside the root is.
func readsConfiguredRelease(scope *linkScope, target string) bool {
	if !scope.configurationNames(target) {
		return false
	}
	content, err := os.ReadFile(target)
	if err != nil {
		return false
	}
	var doc yaml.Node
	if yaml.Unmarshal(content, &doc) != nil {
		return false
	}
	return scalarAtPathOnDoc(&doc, []string{"kind"}) == "Release"
}

// gitignoreWalker carries the per-directory matcher cache used during
// a single workspace walk. The walker constructs matchers lazily —
// each directory's `.gitignore` is parsed at most once and reused for
// every descendant file or sub-directory.
//
// Cache shape: dir absolute path → matcher (or nil sentinel for "no
// .gitignore here, but we checked"). The nil-sentinel keeps subsequent
// ancestor walks from re-stat'ing the same path.
type gitignoreWalker struct {
	root  string
	cache map[string]gitignore.Matcher
}

// newGitignoreWalker constructs a walker rooted at root.
func newGitignoreWalker(root string) *gitignoreWalker {
	return &gitignoreWalker{
		root:  root,
		cache: map[string]gitignore.Matcher{},
	}
}

// matchesAny tests path against every `.gitignore` matcher from the
// path's containing directory up to the walker's root, deepest-first.
// Returns true if any matcher's Match reports the path as ignored.
//
// Per gitignore precedence rules, the deepest matcher takes priority
// when patterns conflict. The first ignore-match across the ancestor
// chain short-circuits and returns true. Negation patterns
// (`!pattern`) are resolved inside each per-directory matcher; this
// walker doesn't synthesize cross-file negation.
//
// isDir is forwarded to each matcher so directory-only patterns
// (`dirname/`) take effect: directory entries are matched with
// isDir=true. The candidate path is split into root-relative
// components because the matcher operates on path-component slices
// rather than on a single string.
func (w *gitignoreWalker) matchesAny(path string, isDir bool) bool {
	parts := w.relComponents(path)
	if parts == nil {
		return false
	}
	for _, dir := range w.ancestorsToRoot(path) {
		matcher := w.matcherFor(dir)
		if matcher == nil {
			continue
		}
		if matcher.Match(parts, isDir) {
			return true
		}
	}
	return false
}

// relComponents splits path into its components relative to the
// walker's root. Returns nil for a path equal to or outside the root.
// Each per-directory matcher is scoped to a domain expressed in these
// same root-relative components, so candidate paths and pattern
// domains share one coordinate system.
//
// Containment is the shared predicate's, judged on where the relative
// path lands. A directory whose name merely begins with two dots — a
// `..cache` beside the workspace files — is an ordinary directory and
// is walked; only one that climbs above the root is dropped.
func (w *gitignoreWalker) relComponents(path string) []string {
	rel, ok := pathscope.Relative(w.root, path)
	if !ok {
		return nil
	}
	return strings.Split(rel, "/")
}

// ancestorsToRoot returns the directory chain from the file's
// containing directory up to (and including) the walker's root, in
// deepest-first order. For a path equal to or outside the root, the
// returned slice is empty.
func (w *gitignoreWalker) ancestorsToRoot(path string) []string {
	dir := filepath.Dir(path)
	// Guard: only traverse paths under root. Paths outside the root
	// return an empty ancestor list (no .gitignore consultation).
	// A directory whose name begins with two dots is inside the root
	// like any other, and its .gitignore is read like any other.
	rel, err := filepath.Rel(w.root, dir)
	if err != nil {
		return nil
	}
	if _, ok := pathscope.Resolve(w.root, rel); !ok {
		return nil
	}
	var out []string
	for {
		out = append(out, dir)
		if dir == w.root {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return out
}

// matcherFor returns the matcher for dir's `.gitignore`, parsing it
// lazily and caching the result. Returns nil when no `.gitignore`
// exists or cannot be read.
//
// Each pattern is scoped to a domain of dir's components relative to
// the walker's root, so a directory's patterns only match paths within
// that directory's subtree. The root `.gitignore` carries an empty
// domain and therefore applies tree-wide. Comment lines (`#`) and blank
// lines are skipped, matching gitignore file conventions, by the one reading
// of an ignore file's lines the entity walk reads too.
func (w *gitignoreWalker) matcherFor(dir string) gitignore.Matcher {
	if cached, ok := w.cache[dir]; ok {
		return cached
	}
	path := filepath.Join(dir, ".gitignore")
	f, err := os.Open(path)
	if err != nil {
		w.cache[dir] = nil
		return nil
	}
	defer f.Close()

	domain := w.relComponents(dir)
	lines, err := sourcewalk.PatternLines(f)
	if err != nil {
		w.cache[dir] = nil
		return nil
	}
	patterns := make([]gitignore.Pattern, 0, len(lines))
	for _, line := range lines {
		patterns = append(patterns, gitignore.ParsePattern(line, domain))
	}
	matcher := gitignore.NewMatcher(patterns)
	w.cache[dir] = matcher
	return matcher
}

// workspaceDoc is one parsed workspace YAML document delivered by the
// workspace-document traversal, paired with the cleaned absolute path it
// was read from.
//
// The node is the same pointer the parse cache holds for a cached
// document, so a visitor reads it and never mutates it.
type workspaceDoc struct {
	path string
	node *yaml.Node
}

// rangeWorkspaceDocs invokes visit once per workspace YAML document, for
// a caller holding a session: it snapshots the session's parse cache as
// the overlay and runs the shared traversal below over it. Returning
// false from visit stops the traversal.
//
// The traversal itself is single-sourced in that shared form, which is
// what every workspace-wide document projection is built on, whether it
// reaches it from a session or without one.
//
// Two passes, in this order:
//
//   - The parse cache, holding whatever content the editor last sent —
//     which may be an unsaved buffer, and is therefore newer than the
//     bytes on disk.
//   - The on-disk workspace walk, honoring the exclusion list and
//     `.gitignore` chain walkWorkspaceYAML applies.
//
// A path the cache pass already delivered is skipped by the walk pass,
// so each file is visited at most once and the fresher document is the
// one a visitor sees. Path identity is what makes that de-duplication
// exact: it models "the same file reached twice" directly, where
// de-duplicating on the entity's declared name would report a
// mid-rename buffer and its own on-disk predecessor as two separate
// entities, only one of which exists.
//
// Paths are compared after filepath.Clean and nothing further. Cleaning
// is a pure text operation, so the traversal reads no ambient process
// state; the cost is that a client addressing one file under two
// spellings supplies two identities the traversal cannot tell apart.
//
// Delivery order is deterministic: the cache pass is sorted by path
// before delivery, because the cache's own iteration is map-ordered,
// and the walk pass visits lexically.
//
// Neither pass delivers a document whose place in the workspace leads out
// of it but a Release the workspace configuration names. The walk pass reads
// nothing else through a link leading out of the root, as walkWorkspaceYAMLIn
// describes. The cache pass holds the same line for a document the editor
// has open: one at a path inside the root
// that leads out of it once its links are resolved — a link to a file
// outside, or a file inside a linked directory outside — is not delivered,
// so a document the walk refuses to read is not offered either because
// its editor happens to have it open. The judgement is leadsOutOfWorkspace's,
// the one resolution every other path is held to. Such a document is left to
// the walk pass, which reads it only where the walk reads the file it stands
// for — a Release the workspace configuration names, read from disk — so the
// two passes never judge one place two ways. A document the cache
// holds at a path outside the root is delivered as before, for each visitor
// to judge: it is not the workspace's at all, and the visitors that report
// workspace documents already leave it out.
//
// Cancellation is honored between files during the walk pass. The cache
// pass snapshots under the cache lock and invokes visit after releasing
// it, so a visitor is never running while the lock is held and may be
// as expensive as it needs to be.
func (s *Server) rangeWorkspaceDocs(ctx context.Context, visit func(doc workspaceDoc) bool) {
	rangeWorkspaceDocsIn(ctx, s.workspaceRoot, s.wsConfig, s.overlayDocs(), visit)
}

// overlayDocs snapshots the session's parse cache as the fresher-than-disk
// overlay the shared traversal takes.
//
// It is the one place the snapshot is taken, so every projection that reaches
// the traversal from a session supplies the same overlay. A second snapshot
// would agree with this one and would be free to stop agreeing — a different
// path cleaning, a different iteration — and the divergence would show up as two
// surfaces disagreeing about which version of a file they were reading.
func (s *Server) overlayDocs() []workspaceDoc {
	var out []workspaceDoc
	s.parseCache.Range(func(file string, node *yaml.Node, _ []byte) bool {
		out = append(out, workspaceDoc{path: filepath.Clean(file), node: node})
		return true
	})
	return out
}

// rangeWorkspaceDocsIn is the whole of rangeWorkspaceDocs with the root, the
// workspace's configuration and the fresher-than-disk overlay supplied rather
// than read off a session.
//
// The overlay is the first pass and the on-disk walk the second, exactly as
// described above. A caller holding no editor supplies no overlay, and the
// traversal is then the walk alone — which is the same answer the session-bound
// form gives when nothing is open, rather than a second reading of what counts
// as a workspace document. cfg is read for the Releases it names, as
// walkWorkspaceYAMLIn reads it.
//
// It returns the links leading out of the workspace the walk pass met and did
// not read. A traversal a visitor stopped early returns what the walk had met
// by then, which is none where it stopped in the cache pass.
func rangeWorkspaceDocsIn(ctx context.Context, root string, cfg workspaceconfig.Config, overlay []workspaceDoc, visit func(doc workspaceDoc) bool) unreadLinks {
	seen := map[string]bool{}
	scope := linkScope{root: root, cfg: cfg}

	cached := append([]workspaceDoc(nil), overlay...)
	sort.Slice(cached, func(i, j int) bool { return cached[i].path < cached[j].path })
	for _, doc := range cached {
		if seen[doc.path] {
			continue
		}
		// Left unmarked, so the walk below meets the file and judges it as it
		// judges every link: what it reads is the file on disk, never the editor's
		// copy of a document leading out.
		if _, inside := pathscope.Relative(root, doc.path); inside && scope.leadsOut(doc.path, false) {
			continue
		}
		seen[doc.path] = true
		if !visit(doc) {
			return unreadLinks{}
		}
	}

	stopped := false
	unread, _ := walkWorkspaceYAMLIn(ctx, root, cfg, func(path, read string) error {
		if stopped {
			return filepath.SkipAll
		}
		clean := filepath.Clean(path)
		if seen[clean] {
			return nil
		}
		seen[clean] = true
		content, err := os.ReadFile(read)
		if err != nil {
			return nil
		}
		var doc yaml.Node
		if uerr := yaml.Unmarshal(content, &doc); uerr != nil {
			return nil
		}
		if !visit(workspaceDoc{path: clean, node: &doc}) {
			stopped = true
			return filepath.SkipAll
		}
		return nil
	})
	return unread
}

// entityOrderLess is the total order every workspace-entity projection
// reports its entities in: kind, then name, then source file.
//
// The source file breaks every remaining tie, because the traversal
// yields at most one entity per file. That is what makes the order total,
// and a total order is what makes a projection's marshalled bytes a
// function of the workspace's content rather than of the sequence the
// traversal delivered it in — the parse-cache pass and the on-disk walk
// deliver in path order, which is not this order.
//
// It sits beside the traversal, and there is one of it, so two
// projections a client joins against each other cannot order the same
// entities differently.
func entityOrderLess(aKind, aName, aFile, bKind, bName, bFile string) bool {
	if aKind != bKind {
		return aKind < bKind
	}
	if aName != bName {
		return aName < bName
	}
	return aFile < bFile
}

// excludedDirName matches directory names the workspace walker
// unconditionally skips.
func excludedDirName(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "node_modules", "vendor", "dist":
		return true
	}
	return false
}

// isYAMLFile reports whether name has a .yaml or .yml extension.
func isYAMLFile(name string) bool {
	return strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")
}
