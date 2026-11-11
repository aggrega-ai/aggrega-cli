// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginops

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/clisurface"
	"github.com/aggrega-ai/aggrega-cli/internal/opprogress"
	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/internal/registryops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/ociref"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// pluginQuery is what an addition's query asks for: a plugin name, or a part of
// one to match, and the version to pin where the query names one.
type pluginQuery struct {
	// name is the query with any version removed.
	name string
	// version is the version the query names after a colon, or empty where it
	// names none and the latest published version is taken.
	version string
}

// exactName reports whether name can be a plugin's own name as a registry
// publishes it: one repository path component in the registry's grammar
// (registryhost.IsRepositoryPath) and nothing more. Only such a name is looked up
// directly; anything else can only be a part of a name to match.
func (q pluginQuery) exactName() bool {
	return q.name != "" && !strings.Contains(q.name, "/") && registryhost.IsRepositoryPath(q.name)
}

// parsePluginQuery reads an addition's query: a plugin name, optionally followed
// by a colon and the version to pin (<name>:<version>).
//
// It reaches no registry, so a query that cannot name a plugin is refused before
// anything is fetched. A version is held to the strict SemVer reading every
// published version tag is selected by (oci.HasAnySemverTag), and a pre-release is
// a version like any other: naming one is how it is pinned, since the latest is
// never taken from pre-releases. A version is pinned to a plugin's exact name only,
// because a part of a name could match several plugins and the version says which
// one was meant no better than the part does.
func parsePluginQuery(command, query string) (pluginQuery, error) {
	name, version, pinned := ociref.SplitVersionTag(query)
	if !pinned {
		return pluginQuery{name: query}, nil
	}
	q := pluginQuery{name: name, version: version}
	if !oci.HasAnySemverTag([]string{version}) {
		return q, fmt.Errorf("%s: %q names the version %q, which is not a version: write <name>:<version>, "+
			"with the version as MAJOR.MINOR.PATCH (for example 1.2.0)", command, query, version)
	}
	if !q.exactName() {
		return q, fmt.Errorf("%s: %q pins a version to %q, which is not a plugin name: a version is pinned to a "+
			"plugin's exact name, written in lowercase letters, digits and separators", command, query, name)
	}
	return q, nil
}

// locatePlugin finds the repository of the plugin a query names, beneath the
// location the addition reads, and returns the versions it found it with.
//
// The plugin's own name is looked up first. The local discovery index answers
// where it carries the repository that name is published as — and, for a query
// naming a version, carries that version too — so a plugin the index knows is
// found with no registry round trip, and its version is then chosen from the index
// as well. Otherwise one tag listing of that repository answers: a listing holding
// a tag finds the plugin, and its tags are returned so the version is chosen from
// them without listing them again.
//
// Only a query that finds no plugin that way is matched as a case-insensitive
// substring against the names of every plugin of the type, which walks the
// registry's whole catalog of repositories (registryops.EnumerateRepositories); the
// match then has to name exactly one plugin (ResolveSingleRepo). A query naming a
// version is never matched that way: it pins one plugin by its exact name, so a
// name no listing finds is refused as it stands.
//
// A listing that names no tag and a listing the registry answers as not found both
// mean the name is not a plugin, because a registry may answer a listing of a
// repository it does not hold either way. Any other failure of the listing is
// returned, naming the registry, rather than read as a miss — a fault is not an
// answer about the name.
func locatePlugin(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, reg registryops.RepoLister, host string, under registryhost.Location, socket string, q pluginQuery) (repoPath string, tags []string, err error) {
	const command = "aggrega plugin add"
	if q.exactName() {
		exact := under.Repository(catalog.KindPathSegment("Plugin") + "/" + socket + "/" + q.name)
		record, indexed, ierr := indexRecord(rw, host, exact)
		if ierr != nil {
			return "", nil, fmt.Errorf("%s: %w", command, ierr)
		}
		if indexed && (q.version == "" || slices.Contains(record.Versions, q.version)) {
			return exact, record.Versions, nil
		}
		end := opprogress.Begin(ctx, "looking up "+socket+" plugin "+q.name+" on "+host)
		listed, lerr := RepoTagLister(ctx, rw.Config(), host, exact)
		if errors.Is(lerr, oci.ErrFetchNotFound()) {
			// A name the registry holds nothing under is an answer, not a failure.
			end(nil)
		} else {
			end(lerr)
		}
		switch {
		case lerr == nil && len(listed) > 0:
			return exact, listed, nil
		case lerr != nil && !errors.Is(lerr, oci.ErrFetchNotFound()):
			return "", nil, fmt.Errorf("%s: %s: list versions for %s: %w", command, host, exact, lerr)
		}
	}
	if q.version != "" {
		return "", nil, fmt.Errorf("%s: no %s plugin named %q is published in %s", command, socket, q.name, host)
	}
	end := opprogress.Begin(ctx, "searching every repository on "+host+" for "+socket+" plugins matching \""+q.name+"\"")
	matches, err := registryops.EnumerateRepositories(ctx, reg, q.name, socketScope(under, socket))
	end(err)
	if err != nil {
		// The enumeration's refusal is about one registry's listing, so it names
		// that registry: a reader with several configured cannot otherwise tell
		// which one to look at. A listing stopped for running too long says what
		// needs none: a plugin named exactly is looked up without it.
		if errors.Is(err, registryops.ErrRepositoryListingTooLong()) {
			return "", nil, fmt.Errorf("%s: %s: no %s plugin is named %q, and searching every repository for a name "+
				"containing it did not finish; name the plugin exactly, as aggrega plugin add %s <name>: %w",
				command, host, socket, q.name, socket, err)
		}
		return "", nil, fmt.Errorf("%s: %s: %w", command, host, err)
	}
	repoPath, err = ResolveSingleRepo(matches, q.name, socket)
	return repoPath, nil, err
}

// pluginVersion returns the version an addition pins: the one the query names,
// or else the latest published one (resolveLatestVersion). listed is the versions
// locatePlugin found the plugin with, or nil where it matched the plugin instead.
//
// A named version must be one the repository publishes, and the refusal of one it
// does not publish lists the versions it does. A named version carrying a recorded
// withdrawal is pinned all the same — naming a version is the override, as it is
// for every reference that names one — and the withdrawal is announced
// (announceWithdrawnPin) rather than passed over quietly.
func pluginVersion(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, host, repoPath, named string, listed []string, streams noticeStreams, rec *retractionRecorder) (string, error) {
	if named == "" {
		return resolveLatestVersion(ctx, rw, host, repoPath, listed, streams, rec)
	}
	if !slices.Contains(listed, named) {
		published := oci.SortedSemverTags(listed)
		if len(published) == 0 {
			return "", fmt.Errorf("%s is not published at version %s, and publishes no version", repoPath, named)
		}
		return "", fmt.Errorf("%s is not published at version %s; its published versions are %s",
			repoPath, named, strings.Join(published, ", "))
	}
	announceWithdrawnPin(ctx, rw, host, repoPath, named, streams)
	return named, nil
}

// announceWithdrawnPin states, on the writer the marker account goes to, that the
// version a query named carries a withdrawal recorded against it, and states
// nothing where it carries none.
//
// The withdrawal facts come from where the version was found, through the same
// screens the latest version is chosen through: the local discovery index's
// (registryindex.NewScreen) where the index carries that version, so a plugin the
// index knows is screened with no registry round trip, and otherwise the markers
// read for that one version from the registry (liveRetractionScreen). The sentence
// is the screen's own (catalog.RetractionScreen.ReportPinned). It goes to the
// marker account's writer rather than the substitution's, because there is no
// substitution: the version pinned is the one named. With no writer at all there
// is no one to tell, and nothing is read.
func announceWithdrawnPin(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, host, repoPath, version string, streams noticeStreams) {
	out := streams.notices
	if out == nil {
		return
	}
	reference := host + "/" + repoPath
	var screen catalog.RetractionScreen
	if record, ok, err := indexRecord(rw, host, repoPath); err == nil && ok && slices.Contains(record.Versions, version) {
		indexed, serr := registryindex.NewScreen(rw, host, out)
		if serr != nil {
			return
		}
		screen = indexed
	} else {
		screen = liveRetractionScreen(ctx, rw.Config(), host, repoPath, reference, []string{version}, noticeStreams{notices: out, retractions: out}, nil)
	}
	if screen != nil {
		screen.ReportPinned(reference, version)
	}
}

// noReleasedVersion is the refusal of an addition that found its plugin and no
// version to take as the latest. verb is what the addition does with a version —
// attach it, or install it.
//
// Where the tags the plugin was found with hold pre-release versions and nothing
// else, the refusal says so and names the newest of them as one to pin: the latest
// is never taken from pre-releases, and a reader told only that nothing is
// published would be told something untrue. Otherwise nothing selectable is
// published, and the refusal says that.
func noReleasedVersion(command, repoPath, name string, listed []string, verb string) error {
	if pre := oci.SortedSemverTags(listed); len(pre) > 0 && oci.SelectLatestVersion(listed, false) == "" {
		return fmt.Errorf("%s: %s publishes only pre-release versions (%s), and the latest version is never taken "+
			"from those; name one to %s it, as in %s:%s", command, repoPath, strings.Join(pre, ", "), verb, name, pre[len(pre)-1])
	}
	return fmt.Errorf("%s: %s has no published version tag to %s; publish a version first", command, repoPath, verb)
}

// refuseWorkspaceFileOutsideAWorkspace refuses an addition whose write would land
// in the workspace configuration file of a folder no workspace was found from
// while nothing named one ([workspaceconfig.ResolvedWorkspace.FoundNoWorkspace]),
// and returns nil for every other addition.
//
// Such a folder's root is only where the command was run, so writing the
// workspace's own configuration file there would make the folder a workspace as a
// side effect of adding a plugin. The refusal is reached before any registry, with
// the two ways out: make the folder a workspace, in the words every surface uses
// for that (clisurface.NoWorkspaceWarning), or add the plugin to the configuration
// every workspace shares. An addition whose write goes to that shared file —
// machineWide, read off the resolved write target — needs no workspace and is
// never refused here.
func refuseWorkspaceFileOutsideAWorkspace(command string, rw *workspaceconfig.ResolvedWorkspace, configPath string, machineWide bool) error {
	if machineWide || !rw.FoundNoWorkspace() {
		return nil
	}
	return fmt.Errorf("%s: nothing was added, because it would be written to %s, a workspace's own configuration, "+
		"and %s; or pass --global to add it to the configuration every workspace shares",
		command, configPath, clisurface.NoWorkspaceWarning(rw.Root()))
}
