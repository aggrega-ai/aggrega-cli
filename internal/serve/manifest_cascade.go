// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// markerFilePath is the per-operator marker the multi-Manifest
// cascade reads at Step 2. The file lives under the workspace's
// `.aggrega/` scratch directory and is conventionally gitignored —
// operators choose their own active Release without committing the
// choice to the workspace-wide config.
const markerFilePath = ".aggrega/active-release"

// cascadeAttempt records what the disambiguation cascade tried and
// which step (if any) selected a Release. It travels back from
// runManifestCascade with the Release itself and is not retained: the
// daemon holds no Release, so it holds no account of how one was chosen
// either. Its diagnosticMessage and fullTrace methods turn it into the
// text the active-Release query reports when the cascade exhausted.
type cascadeAttempt struct {
	// ResolvedStep is 1-3 when a step succeeded; 0 when the cascade
	// exhausted into Step 4 fallback (no Release is returned).
	ResolvedStep int

	// AuthoringReleaseConfigured records whether the workspace
	// config's Build.AuthoringRelease was non-empty (Step 1 was
	// attempted).
	AuthoringReleaseConfigured bool

	// AuthoringReleaseTarget is the value of Build.AuthoringRelease as the
	// configuration holds it, before it is resolved against the workspace
	// root, so an account of the attempt can name the file the way the person
	// who configured it wrote it.
	AuthoringReleaseTarget string

	// AuthoringReleaseLoadError is non-nil when Step 1 was attempted
	// but the named file failed to load.
	AuthoringReleaseLoadError error

	// AuthoringReleasePath is the resolved path Step 1 attempted, kept
	// so a found-but-invalid authoring_release can be squiggled on the
	// file (the load error alone does not carry its own path).
	AuthoringReleasePath string

	// ReleaseCandidatesFound is the number of Release YAML files
	// discovered by the Step 3 workspace walk.
	ReleaseCandidatesFound int

	// SingleReleaseLoadError is non-nil when Step 3's single candidate
	// was found but failed to load. Symmetric with the Step 1/2 load-
	// error fields: the one-Release workspace's found-but-invalid case
	// carries a structured record here as well as a stderr warning, so
	// the common case is never reported on the console alone.
	SingleReleaseLoadError error

	// SingleReleaseCandidatePath is the path of Step 3's single
	// candidate, kept so its load failure can be squiggled on the file.
	SingleReleaseCandidatePath string

	// MarkerFilePresent records whether the .aggrega/active-release
	// marker existed. It comes from a probe that runs regardless of
	// which step resolves, so the log records what the workspace
	// declared rather than only what the winning step happened to read.
	MarkerFilePresent bool

	// MarkerFileTarget is the path the marker pointed at (when
	// present), for use in diagnostic messages. Recorded alongside
	// MarkerFilePresent, on the same probe.
	MarkerFileTarget string

	// MarkerFileLoadError is non-nil when Step 2 was attempted but
	// the marker's target file failed to load.
	MarkerFileLoadError error

	// MarkerFileOutside records that the marker's target lies outside the
	// workspace root — written as an absolute path elsewhere, climbing out of
	// the root, or reached through a link leading out — and is not a Release
	// the workspace's configuration names, so Step 2 did not load it.
	MarkerFileOutside bool

	// MarkerResolvedPath is the resolved path the marker target pointed
	// at, kept so a found-but-invalid marker target can be squiggled on
	// the file (MarkerFileTarget is the raw, possibly relative, target).
	MarkerResolvedPath string
}

// failedReleaseCandidate is a Release file the cascade found but could
// not load — a found-but-invalid candidate, distinct from a
// genuinely-absent Release.
type failedReleaseCandidate struct {
	Path string
	Err  error
}

// failedReleaseCandidates returns every found-but-invalid Release the
// cascade encountered, across all three load steps, so each can be
// surfaced as an editor diagnostic on its own file. Empty when no step
// found a file that failed to load (the genuinely-absent case).
func (a cascadeAttempt) failedReleaseCandidates() []failedReleaseCandidate {
	var out []failedReleaseCandidate
	if a.AuthoringReleaseLoadError != nil && a.AuthoringReleasePath != "" {
		out = append(out, failedReleaseCandidate{Path: a.AuthoringReleasePath, Err: a.AuthoringReleaseLoadError})
	}
	if a.MarkerFileLoadError != nil && a.MarkerResolvedPath != "" {
		out = append(out, failedReleaseCandidate{Path: a.MarkerResolvedPath, Err: a.MarkerFileLoadError})
	}
	if a.SingleReleaseLoadError != nil && a.SingleReleaseCandidatePath != "" {
		out = append(out, failedReleaseCandidate{Path: a.SingleReleaseCandidatePath, Err: a.SingleReleaseLoadError})
	}
	return out
}

// runManifestCascade executes the four-step disambiguation cascade
// against the workspace. Returns the resolved Release (nil when the
// cascade exhausts into Step 4) and the cascadeAttempt log
// describing which steps were tried.
//
// Cascade-internal load errors — a configured authoring_release, a
// marker target, or a lone walked candidate that fails to load —
// surface as stderr warnings AND record into the cascadeAttempt log;
// the cascade itself does not return an error. A misconfigured
// authoring_release: or stale marker file is non-fatal — the daemon
// proceeds with the next step rather than refusing to start.
//
// Step ordering: Step 1 (build.authoring_release config) → Step 2
// (.aggrega/active-release marker) → Step 3 (exactly one Release in
// the workspace) → Step 4 (fallback nil). The operator's explicit
// marker therefore outranks the single-Release inference: a workspace
// holding exactly one Release file and a marker naming a different
// file resolves the marker's Release. The walk runs only when no
// marker selected a loadable Release, so a marker whose target cannot
// be loaded falls through to Step 3 rather than to the fallback.
//
// The marker is held to the workspace's containment. A target lying
// outside the workspace root — an absolute path elsewhere, a path
// climbing out of the root, or a path reached through a link leading out,
// judged by the one resolution leadsOutOfWorkspace makes — is not loaded
// unless the workspace's configuration names it as a Release, and the
// cascade falls through exactly as it does for a marker whose target
// cannot be loaded. The configuration is the way to reach a Release
// outside the workspace; the marker, written by hand or by an editor from
// its selection, is not.
//
// The marker's presence and target are recorded before Step 1 runs,
// so the attempt log states what the workspace declared even when an
// earlier step wins.
func runManifestCascade(workspaceRoot string, wsCfg workspaceconfig.Config, stderr io.Writer) (*types.Release, string, cascadeAttempt) {
	var attempt cascadeAttempt

	// The marker is probed ahead of Step 1 so the attempt log carries
	// the operator's declaration regardless of which step resolves. A
	// present-but-empty marker records presence with no target; an
	// unreadable marker reads as absent.
	if data, err := os.ReadFile(filepath.Join(workspaceRoot, markerFilePath)); err == nil {
		attempt.MarkerFilePresent = true
		attempt.MarkerFileTarget = strings.TrimSpace(string(data))
	}

	// Step 1: config field
	if wsCfg.Build.AuthoringRelease != "" {
		attempt.AuthoringReleaseConfigured = true
		attempt.AuthoringReleaseTarget = wsCfg.Build.AuthoringRelease
		path := resolveCascadePath(workspaceRoot, wsCfg.Build.AuthoringRelease)
		rel, err := resolver.LoadRelease(path)
		if err == nil {
			attempt.ResolvedStep = 1
			return rel, path, attempt
		}
		attempt.AuthoringReleaseLoadError = err
		attempt.AuthoringReleasePath = path
		fmt.Fprintf(stderr, "  cascade: step 1 authoring_release=%q failed to load: %v\n", path, err)
	}

	// Step 2: the operator's marker file
	if attempt.MarkerFilePresent && attempt.MarkerFileTarget != "" {
		resolved := resolveCascadePath(workspaceRoot, attempt.MarkerFileTarget)
		if leadsOutOfWorkspace(workspaceRoot, resolved, &wsCfg) {
			attempt.MarkerFileOutside = true
			fmt.Fprintf(stderr, "  cascade: step 2 marker target %q lies outside the workspace root and is not loaded\n", resolved)
		} else {
			rel, err := resolver.LoadRelease(resolved)
			if err == nil {
				attempt.ResolvedStep = 2
				return rel, resolved, attempt
			}
			attempt.MarkerFileLoadError = err
			attempt.MarkerResolvedPath = resolved
			fmt.Fprintf(stderr, "  cascade: step 2 marker target %q failed to load: %v\n", resolved, err)
		}
	}

	// Step 3: workspace-recursive Release walk; exactly-one wins
	candidates := walkForReleases(workspaceRoot, wsCfg)
	attempt.ReleaseCandidatesFound = len(candidates)
	if len(candidates) == 1 {
		rel, err := resolver.LoadRelease(candidates[0])
		if err == nil {
			attempt.ResolvedStep = 3
			return rel, candidates[0], attempt
		}
		attempt.SingleReleaseLoadError = err
		attempt.SingleReleaseCandidatePath = candidates[0]
		fmt.Fprintf(stderr, "  cascade: step 3 single Release %q failed to load: %v\n", candidates[0], err)
	}

	// Step 4: fallback — Release path stays empty alongside the nil
	// Release object.
	attempt.ResolvedStep = 0
	return nil, "", attempt
}

// resolveCascadePath joins a workspace-relative path with the root,
// or returns the path as-is when it is absolute. Mirrors the path-
// resolution convention CLI verbs use for --release.
func resolveCascadePath(workspaceRoot, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(workspaceRoot, p)
}

// walkForReleases returns the absolute paths of every Release-kind
// YAML file under workspaceRoot. Header-only YAML parse; malformed
// files and non-Release kinds are silently skipped. The canonical
// ignore-set (.git, .aggrega, node_modules, vendor, dist, .trace,
// hidden dirs) is excluded — Step 3 walks the entire workspace
// root, not a configured subdirectory, so filtering vendored copies
// and build outputs is load-bearing for correctness. wsCfg is the
// workspace's configuration, read for the Releases it names; see
// walkReleases.
func walkForReleases(workspaceRoot string, wsCfg workspaceconfig.Config) []string {
	return walkReleases(workspaceRoot, wsCfg).releases
}

// releaseWalk is what one walk of a workspace for its Releases met.
type releaseWalk struct {
	// releases are the absolute paths of the Release-kind files found.
	releases []string
	// unreadLinkedDirs are the links met that lead to a directory outside the
	// workspace root, each named relative to the root where the link itself
	// sits, in the order the walk met them.
	unreadLinkedDirs []string
	// unreadLinkedFiles are the links met that lead to a YAML file outside the
	// workspace root which the workspace's configuration does not name as a
	// Release, each named relative to the root where the link itself sits, in
	// the order the walk met them.
	unreadLinkedFiles []string
}

// walkReleases is the walk walkForReleases answers from, keeping as well each
// link it met that leads out of the workspace root and was not read: those
// leading to a directory, and those leading to a YAML file.
//
// The walk never enters a link: filepath.WalkDir reports one as an entry of its
// own and does not descend into it. A link to a directory inside the root needs
// nothing more, because that directory is walked, or left out, where it actually
// sits. A link to a directory outside the root leads somewhere the walk does not
// go, so a Release inside it is never found. It is recorded where the walk meets
// it, so an answer can name the directories that were not read rather than leave
// its reader to wonder why a Release their editor shows is not offered.
//
// A link to a directory is judged as the directory it stands in for. One the walk
// would leave out as a directory — by where it sits or by its name — is not
// recorded, just as a directory there would not be read; one inside a directory
// the walk leaves out is never met at all. And a link is recorded only where
// resolving it succeeds: one that leads nowhere, or round a loop, has no directory
// behind it to name, and nothing further is tried to find out where it was meant
// to go. A link leading to a directory the workspace's configuration lists as a
// catalog directory is not recorded either: the configuration has reached that
// directory, and its entities are read through the catalog lookup, as every
// configured catalog directory's are, rather than as workspace documents.
//
// A link standing where the walk would read a YAML file is judged by the file it
// leads to, from the same one resolution. One leading to a file outside the root is
// not read, so what lies outside the workspace is not offered as one of its
// Releases, and it is recorded where the walk meets it, as a link to a directory
// is. The kind of that file is not known without reading it, so this holds for
// every such link, whether or not a Release is what it leads to. The exception is a
// file the workspace's configuration names as a Release, by build.authoring_release
// or by a key of workspace.releases, that declares one: the configuration has
// reached it, so it is read and listed at the link's place, and not recorded. Both
// exceptions are one judgement, configurationReaches, which the entity walk and the
// requests naming a Release ask too. A link to a file inside the root is read at
// the link's place, and its file is read where it actually sits as well. A link
// that cannot be resolved, or that leads to anything but a file, is not read as a
// file and not recorded among the files, and nothing further is tried. What a
// followed link is read through is the file its one resolution reported, so the
// bytes read are those of the file judged.
func walkReleases(workspaceRoot string, wsCfg workspaceconfig.Config) releaseWalk {
	var out releaseWalk
	scope := linkScope{root: workspaceRoot, cfg: wsCfg}
	_ = filepath.WalkDir(workspaceRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		var linked linkTarget
		link := path != workspaceRoot && d.Type()&fs.ModeSymlink != 0
		if link {
			scope.resolve()
			linked = resolveLink(path, workspaceRoot)
			if linked.dir && linked.outside && !shouldExcludeDir(d.Name(), path, workspaceRoot) && !scope.configurationReaches(linked) {
				out.unreadLinkedDirs = scope.named(out.unreadLinkedDirs, path)
			}
		}
		if d.IsDir() {
			if shouldExcludeDir(d.Name(), path, workspaceRoot) {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}
		read := path
		if link {
			if !linked.file {
				return nil
			}
			if linked.outside && !scope.configurationReaches(linked) {
				out.unreadLinkedFiles = scope.named(out.unreadLinkedFiles, path)
				return nil
			}
			read = linked.target
		}
		data, rerr := os.ReadFile(read)
		if rerr != nil {
			return nil
		}
		var hdr struct {
			Kind string `yaml:"kind"`
		}
		if uerr := yaml.Unmarshal(data, &hdr); uerr != nil {
			return nil
		}
		if hdr.Kind == "Release" {
			out.releases = append(out.releases, path)
		}
		return nil
	})
	return out
}

// linkTarget is where a link met by a workspace walk — or a path with links on the
// way to it — leads, as one resolution of it reports it.
type linkTarget struct {
	// target is the place the link leads, made absolute with every link on the
	// way resolved; empty where the link cannot be resolved.
	target string
	// dir and file say what lies there: a directory, or a regular file. Neither
	// is set where the link cannot be resolved, or leads to anything else.
	dir, file bool
	// outside says the target lies outside the workspace root.
	outside bool
}

// resolveLink reports where the link at path leads, judged against the
// workspace root by pathscope.ResolvesInside, the one definition of inside.
//
// Where the link goes is read once, by that judgement, and its reading is the
// whole of what is asked of it: both the directory a link stands in for and the
// file it stands in for are judged from it, so the two can never be judged from
// different readings of one link. A link the judgement cannot follow to anything
// — one leading nowhere, or round a loop — is not existence, so the judgement
// passes it over for the directory holding it; such a link leads to nothing the
// walk reads or names, and nothing further is tried to find out where it was
// meant to go. The root is resolved the same way, so a root reached through a
// link of its own still counts what lies under it as inside.
func resolveLink(path, root string) linkTarget {
	judged, inside := pathscope.ResolvesInside(root, path)
	if judged.Met != absolutePath(path) {
		return linkTarget{}
	}
	info, err := os.Stat(judged.LeadsTo)
	if err != nil {
		return linkTarget{}
	}
	return linkTarget{
		target:  judged.LeadsTo,
		dir:     info.IsDir(),
		file:    info.Mode().IsRegular(),
		outside: !inside,
	}
}

// leadsOutOfWorkspace reports whether the file or directory at path lies outside
// the workspace root by the one definition of inside, pathscope.ResolvesInside,
// and is not let through by the workspace's configuration.
//
// It is the judgement every path handed to the daemon from outside its own walk is
// held to — every path a request names, whether the request reads it or writes
// it: a document a request names through resolveWorkspacePathParam, a destination
// a request names through workspacePath, a document an insertion point is located
// in, a key file a registration names, and the Release the active-Release marker
// names. A read and a write are judged alike, because the definition already
// covers a path that is not there yet: the deepest part of it that exists is
// judged, with its links resolved, against the root resolved the same way. So a
// path spelled inside the root that leads out through a link, a path not made yet
// beneath a link leading out, an absolute path elsewhere and a path climbing out of
// the root are all outside alike, and where the whole path exists the part judged
// is the path itself. A link leading nowhere, or round a loop, is not existence: it
// is passed over for the directory holding it, and nothing is followed to find out
// where it was meant to go, so whatever reads such a path next reports the missing
// file as it always has.
//
// release is the workspace's configuration where path names a Release, and nil
// where it names anything else. A Release the configuration names — as
// build.authoring_release, or as a key of workspace.releases, judged by the file
// itself as the Release walk judges a link, and declaring a Release — is reached
// through the configuration and is let through wherever it sits. Nothing else a
// request names has such an exception.
func leadsOutOfWorkspace(root, path string, release *workspaceconfig.Config) bool {
	scope := linkScope{root: root}
	if release != nil {
		scope.cfg = *release
	}
	return scope.leadsOut(path, release != nil)
}

// leadsOut is leadsOutOfWorkspace measured against this scope, so a caller
// judging several paths against one root works the configuration's names out
// once. release says whether path names a Release, which the scope's
// configuration may let through.
//
// A scope with no root holds nothing, as a write with no root holds nothing: there
// is nothing for a path to lie inside.
func (l *linkScope) leadsOut(path string, release bool) bool {
	if l.root == "" {
		return false
	}
	if _, inside := pathscope.ResolvesInside(l.root, path); inside {
		return false
	}
	if !release {
		return true
	}
	l.resolve()
	linked := resolveLink(path, l.root)
	return !linked.file || !l.configurationReaches(linked)
}

// LeadsOutOfWorkspace reports whether the file or directory at path lies outside
// root by the one judgement the language server holds every path a request names
// to, whether the request reads it or writes it (leadsOutOfWorkspace). A surface
// confining a path it was sent to one workspace asks it, so the language server
// and the assistant tools judge a path by one rule.
func LeadsOutOfWorkspace(root, path string) bool {
	return leadsOutOfWorkspace(root, path, nil)
}

// ReleaseLeadsOutOfWorkspace is LeadsOutOfWorkspace for a path naming a Release:
// a Release cfg names — as build.authoring_release, or as a key of
// workspace.releases — is reached through the workspace's configuration and does
// not lead out, wherever it sits, as the language server judges a Release a
// request names.
func ReleaseLeadsOutOfWorkspace(root string, cfg workspaceconfig.Config, path string) bool {
	return leadsOutOfWorkspace(root, path, &cfg)
}

// linkScope is what judging a link met by a workspace walk, or a path the daemon
// is handed, is measured against: the workspace root — judged against by
// pathscope.ResolvesInside, which resolves it the way it resolves the path — the
// root made absolute, which is the coordinate a link is named in, and what the
// workspace's configuration names outside it: its Releases and its catalog
// directories. The derived coordinates are worked out once, the first time
// something is judged, so a walk meeting no link pays nothing.
type linkScope struct {
	root string
	cfg  workspaceconfig.Config

	resolved bool
	// absRoot is the root made absolute, the coordinate a link is named in.
	absRoot string
	// releaseFiles are the files the configuration names as Releases: the one
	// build.authoring_release names, resolved against the root as the
	// active-Release cascade's first step resolves it, and every key of
	// workspace.releases, each already the path of the file it names.
	releaseFiles []string
	// catalogDirs are the directories the configuration lists as catalog
	// directories — the workspace's own, and those it gives a Release under
	// workspace.releases — as the configuration holds them once loaded.
	catalogDirs []string
}

// resolve works out the scope's coordinates, once.
func (l *linkScope) resolve() {
	if l.resolved {
		return
	}
	l.resolved = true
	l.absRoot = absolutePath(l.root)
	if l.cfg.Build.AuthoringRelease != "" {
		l.releaseFiles = append(l.releaseFiles, resolveCascadePath(l.root, l.cfg.Build.AuthoringRelease))
	}
	l.catalogDirs = append([]string{}, l.cfg.Workspace.CatalogDirs...)
	for file, entry := range l.cfg.Workspace.Releases.All() {
		l.releaseFiles = append(l.releaseFiles, file)
		l.catalogDirs = append(append(l.catalogDirs, entry.CatalogDirs...), entry.CatalogDirsAppend...)
	}
}

// named appends to names the link at path, by its own place relative to the
// root — never by the place it leads to.
func (l *linkScope) named(names []string, path string) []string {
	if rel, ok := pathscope.Relative(l.absRoot, absolutePath(path)); ok {
		return append(names, rel)
	}
	return names
}

// configurationReaches reports whether the workspace's configuration reaches the
// place a link leads out of the workspace to, and so whether what lies there is
// read, or let through, wherever it sits: a file the configuration names as a
// Release — as build.authoring_release, or as a key of workspace.releases — that
// declares one, its kind read as every workspace-entity projection reads a
// document's kind; or a directory the configuration lists as a catalog directory,
// for the workspace or for a Release.
//
// It is the language server's answer to the configuration's exception to the
// workspace boundary, for every walk of the workspace and every request naming a
// Release, and it compares nothing itself: whether the configuration reaches the
// place is workspaceconfig.Reaches, the one comparison every surface asks — the
// catalog search stack deciding whether to append the conventional directory and
// the root a catalog directory is held to among them — so a Release file and a
// catalog directory are judged alike, by the place the link leads, and a setting
// naming a link and one naming the place it leads to name the same thing. A
// directory beneath a listed catalog directory is reached by it, as the catalog
// walk of that directory reads it.
//
// The configuration is asked first, and the file is opened only where it names
// it. The settings that name a Release are a Release's, so a file named there that
// declares anything else is not reached through them.
func (l *linkScope) configurationReaches(linked linkTarget) bool {
	l.resolve()
	switch {
	case linked.dir:
		return workspaceconfig.Reaches(l.catalogDirs, linked.target)
	case linked.file:
		if !workspaceconfig.Reaches(l.releaseFiles, linked.target) {
			return false
		}
		content, err := os.ReadFile(linked.target)
		if err != nil {
			return false
		}
		var doc yaml.Node
		if yaml.Unmarshal(content, &doc) != nil {
			return false
		}
		return scalarAtPathOnDoc(&doc, []string{"kind"}) == "Release"
	}
	return false
}

// absolutePath is p made absolute and cleaned, or p cleaned where the working
// directory it would be anchored to cannot be read.
func absolutePath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return filepath.Clean(p)
}

// shouldExcludeDir filters out the canonical ignore-set plus any
// hidden directories. The workspace root itself is never excluded
// (its own name might start with `.` legitimately, e.g., a `.local`
// workspace dir).
func shouldExcludeDir(name, path, workspaceRoot string) bool {
	if path == workspaceRoot {
		return false
	}
	switch name {
	case ".git", ".aggrega", "node_modules", "vendor", "dist", ".trace":
		return true
	}
	if strings.HasPrefix(name, ".") {
		return true
	}
	return false
}

// diagnosticMessage returns the reason the active-Release query reports
// alongside an empty path: why the workspace names no default Release, in
// sentences a person reads and acts on. A client shows it as it stands —
// the editor puts it in the Release row's hover, after a sentence of its
// own — so it is written as whole sentences on one line, each opening with
// a capital and closing with a full stop.
//
// It says what was looked for in the terms the reader writes: the
// build.authoring_release setting and the .aggrega/active-release marker,
// then how many Releases the workspace holds. Where the setting and the
// marker are both simply absent, which is the ordinary case, the two share
// one sentence with the count, so the commonest answer reads as one
// sentence rather than as a list; otherwise each has a sentence of its own,
// in that order, and the count closes. Where one of them names a file, the
// file is named as it was written; where a named file, or the workspace's
// only Release, does not load, its load error follows, so the reader learns
// which file to repair; and where the marker names a file outside the
// workspace that the configuration does not name, the sentence says it is not
// read for that reason. The workspace's only Release being the very file
// the setting or the marker already reported is said as that rather than
// with the same error twice.
//
// The account of every attempt, step by step, is fullTrace's; this is its
// short form and leaves the cascade's own vocabulary out.
//
// It hangs off the attempt log rather than off the daemon because the
// attempt is what the cascade returns: the daemon keeps no Release and no
// record of one, so there is no stored attempt for a method to read.
//
// Returns the empty string when a Release resolved, as fullTrace does:
// there is nothing to explain, and the answer omits the reason then.
func (a cascadeAttempt) diagnosticMessage() string {
	if a.ResolvedStep != 0 {
		return ""
	}

	count := a.releaseCountPhrase()
	if !a.AuthoringReleaseConfigured && !a.MarkerFilePresent {
		return "Neither the build.authoring_release setting nor an " + markerFilePath +
			" marker names a default Release, and the workspace holds " + count + "."
	}

	sentences := make([]string, 0, 3)
	switch {
	case !a.AuthoringReleaseConfigured:
		sentences = append(sentences, "The build.authoring_release setting is not set.")
	case a.AuthoringReleaseLoadError != nil:
		sentences = append(sentences, fmt.Sprintf("The build.authoring_release setting names %q, which does not load: %s.",
			a.AuthoringReleaseTarget, loadFailure(a.AuthoringReleaseLoadError)))
	default:
		sentences = append(sentences, fmt.Sprintf("The build.authoring_release setting names %q.", a.AuthoringReleaseTarget))
	}

	switch {
	case !a.MarkerFilePresent:
		sentences = append(sentences, "There is no "+markerFilePath+" marker.")
	case a.MarkerFileTarget == "":
		sentences = append(sentences, "The "+markerFilePath+" marker is empty, so it names no Release.")
	case a.MarkerFileOutside:
		sentences = append(sentences, fmt.Sprintf("The %s marker names %q, which lies outside the workspace and is not "+
			"a Release the workspace configuration names, so it is not read.", markerFilePath, a.MarkerFileTarget))
	case a.MarkerFileLoadError != nil:
		sentences = append(sentences, fmt.Sprintf("The %s marker names %q, which does not load: %s.",
			markerFilePath, a.MarkerFileTarget, loadFailure(a.MarkerFileLoadError)))
	default:
		sentences = append(sentences, fmt.Sprintf("The %s marker names %q.", markerFilePath, a.MarkerFileTarget))
	}

	sentences = append(sentences, "The workspace holds "+count+".")
	return strings.Join(sentences, " ")
}

// releaseCountPhrase is how many Releases the workspace walk found, as the
// object of "the workspace holds": "no Release", "one Release" or "N
// Releases". The workspace's only Release that does not load carries its
// load error, or, where it is the file the setting or the marker names and
// that failure is already reported, says so instead.
func (a cascadeAttempt) releaseCountPhrase() string {
	switch a.ReleaseCandidatesFound {
	case 0:
		return "no Release"
	case 1:
		if a.SingleReleaseLoadError == nil {
			return "one Release"
		}
		single := a.SingleReleaseCandidatePath
		switch {
		case single != "" && a.AuthoringReleaseLoadError != nil && single == a.AuthoringReleasePath:
			return "one Release, the file the build.authoring_release setting names"
		case single != "" && a.MarkerFileLoadError != nil && single == a.MarkerResolvedPath:
			return "one Release, the file the " + markerFilePath + " marker names"
		}
		return "one Release, which does not load: " + loadFailure(a.SingleReleaseLoadError)
	default:
		return fmt.Sprintf("%d Releases", a.ReleaseCandidatesFound)
	}
}

// loadFailure is a load error as it reads at the end of a sentence: its own
// words on one line, less a closing full stop, so the sentence's own stop is
// the only one. A decoder's error can run over several indented lines, and
// the reason is a one-line summary; the error's own layout stays in the trace.
func loadFailure(err error) string {
	return strings.TrimSuffix(strings.Join(strings.Fields(err.Error()), " "), ".")
}

// fullTrace returns the multi-line trace of what each cascade step
// attempted and why it failed (or was skipped). It travels with the
// active-Release query's answer so a client can log the whole account
// where the operator can read it, while the one-line summary from
// diagnosticMessage is what a compact surface displays.
//
// Each step appears on its own line so the rendering remains scannable.
// Returns the empty string when the cascade resolved a Release (the trace
// is informative only on the exhausted-cascade path).
func (a cascadeAttempt) fullTrace() string {
	if a.ResolvedStep != 0 {
		return ""
	}

	lines := []string{"Aggrega: release-not-resolved cascade trace"}

	// Step 1 — config-based authoring_release
	switch {
	case !a.AuthoringReleaseConfigured:
		lines = append(lines, "  step 1 (build.authoring_release in .aggrega-config.yaml): not configured")
	case a.AuthoringReleaseLoadError != nil:
		lines = append(lines, fmt.Sprintf("  step 1 (build.authoring_release): configured but failed to load — %v", a.AuthoringReleaseLoadError))
	default:
		lines = append(lines, "  step 1 (build.authoring_release): configured and loaded")
	}

	// Step 2 — .aggrega/active-release marker
	switch {
	case !a.MarkerFilePresent:
		lines = append(lines, "  step 2 (.aggrega/active-release marker): not present")
	case a.MarkerFileTarget == "":
		lines = append(lines, "  step 2 (.aggrega/active-release marker): present but empty")
	case a.MarkerFileOutside:
		lines = append(lines, fmt.Sprintf("  step 2 (.aggrega/active-release marker): target %q lies outside the workspace root "+
			"and the workspace configuration does not name it as a Release, so it was not loaded", a.MarkerFileTarget))
	case a.MarkerFileLoadError != nil:
		lines = append(lines, fmt.Sprintf("  step 2 (.aggrega/active-release marker): target %q failed to load — %v", a.MarkerFileTarget, a.MarkerFileLoadError))
	default:
		lines = append(lines, fmt.Sprintf("  step 2 (.aggrega/active-release marker): target %q resolved", a.MarkerFileTarget))
	}

	// Step 3 — single-Release heuristic
	switch a.ReleaseCandidatesFound {
	case 0:
		lines = append(lines, "  step 3 (single-Release heuristic): zero Release files found in workspace")
	case 1:
		if a.SingleReleaseLoadError != nil {
			lines = append(lines, fmt.Sprintf("  step 3 (single-Release heuristic): one Release file found at %s but it failed to load — %v", a.SingleReleaseCandidatePath, a.SingleReleaseLoadError))
		} else {
			lines = append(lines, "  step 3 (single-Release heuristic): one Release file found")
		}
	default:
		lines = append(lines, fmt.Sprintf("  step 3 (single-Release heuristic): ambiguous — %d Release files found, no marker selected one", a.ReleaseCandidatesFound))
	}

	// Step 4 — Aggregate-only fallback (always exhausted at this point;
	// reaching here means none of the prior steps selected a Release)
	lines = append(lines, "  step 4 (Aggregate-only fallback): operation requires Release context; fallback not applicable")

	return strings.Join(lines, "\n")
}
