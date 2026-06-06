// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// NewDefaultOCIPuller returns the production OCIPuller backed by
// pkg/oci.NewRemoteRepository per Pull-call. Returns nil when the
// workspace is configured offline so closure-walker and lazy-lookup
// Tier-3 paths surface as NotFound without attempting any network
// access.
//
// The exported function lives next to the OCIPuller interface
// definition so both CLI verbs (pull, build) and long-running
// consumers (the compiler-as-service daemon) construct pullers via
// the same canonical helper. Local copies would drift; one
// implementation prevents that.
func NewDefaultOCIPuller(wsCfg workspaceconfig.Config) OCIPuller {
	if wsCfg.Workspace.Offline {
		return nil
	}
	return DefaultOCIPuller{registry: wsCfg.Workspace.DefaultRegistry}
}

// DefaultOCIPuller is the production OCIPuller implementation. Pull constructs a
// fresh oci.RemoteRepository per call so per-call authentication + retry behavior
// matches pkg/oci's documented surface exactly. registry is the configured
// default registry, used by PullLibrary to bind a host-less library identity to a
// physical location.
type DefaultOCIPuller struct {
	registry string
}

func (DefaultOCIPuller) Pull(ctx context.Context, reference, tag string) (*oci.PullResult, error) {
	repo, err := oci.NewRemoteRepository(reference)
	if err != nil {
		return nil, err
	}
	return oci.Pull(ctx, repo, tag)
}

// PullLibrary pulls a library by its host-less identity (bare name + kind),
// binding it to a physical location through the configured default registry —
// the pull-side mirror of push's name→location (`<registry>/<kind-segment>/<name>`).
// When floorVersion is empty (a pure bare-name reference at first pull) it lists
// the registry's tags and selects the highest semver; otherwise it uses the
// asserted floor as the tag. The selected version is returned so the
// lockfile pins it. The "latest" resolution happens HERE, at the network step, and
// is pinned — the build never lists tags or fetches.
func (p DefaultOCIPuller) PullLibrary(ctx context.Context, name, kind, floorVersion string) (*oci.PullResult, string, error) {
	reference := libraryReference(p.registry, kind, name)
	repo, err := oci.NewRemoteRepository(reference)
	if err != nil {
		return nil, "", err
	}
	version := floorVersion
	if version == "" {
		tags, terr := oci.ListTags(ctx, repo)
		if terr != nil {
			return nil, "", terr
		}
		version = highestSemverTag(tags)
		if version == "" {
			return nil, "", fmt.Errorf("oci: no semver-tagged version found for %s: %w", reference, oci.ErrFetchNotFound)
		}
	}
	result, perr := oci.Pull(ctx, repo, version)
	if perr != nil {
		return nil, "", perr
	}
	return result, version, nil
}

// PullAggregate pulls a published Aggregate by its bare identity, binding it to
// a physical location through the configured default registry — the pull-side
// mirror of push's name→location (`<registry>/aggregate/<name>`). Discovery
// mirrors PullLibrary: an empty floorVersion lists tags and selects the highest
// semver; a non-empty floor is the tag. It returns the OCI tag it fetched under
// (a location/discovery coordinate); the IDENTITY pinned in the lockfile is the
// fetched body's metadata.version, which PullAndCacheAggregate reads and pins —
// so the tag is discarded and the pin is identity-stable even when a publisher
// tags `latest`/`stable` over the versioned identity.
func (p DefaultOCIPuller) PullAggregate(ctx context.Context, name, floorVersion string) (*oci.PullResult, string, error) {
	reference := p.registry + "/" + libraryPathSegment("Aggregate") + "/" + name
	repo, err := oci.NewRemoteRepository(reference)
	if err != nil {
		return nil, "", err
	}
	tag := floorVersion
	if tag == "" {
		tags, terr := oci.ListTags(ctx, repo)
		if terr != nil {
			return nil, "", terr
		}
		tag = highestSemverTag(tags)
		if tag == "" {
			return nil, "", fmt.Errorf("oci: no semver-tagged version found for %s: %w", reference, oci.ErrFetchNotFound)
		}
	}
	result, perr := oci.Pull(ctx, repo, tag)
	if perr != nil {
		return nil, "", perr
	}
	return result, tag, nil
}

// libraryReference binds a host-less library identity to a physical OCI
// reference, `<registry>/<kind-segment>/<name>` — the pull-side mirror of push's
// buildPushReference, so a library is fetched from exactly where push published
// it (the identity round-trip; pull-location == push-location).
func libraryReference(registry, kind, name string) string {
	return registry + "/" + libraryPathSegment(kind) + "/" + name
}

// libraryPathSegment mirrors the push-side kindPathSegment for library kinds, so
// pull binds a bare-name library to the same registry path push publishes it to
// (the identity round-trip). Library kinds lowercase directly
// ("SchemaLibrary" → "schemalibrary", "MappingLibrary" → "mappinglibrary").
func libraryPathSegment(kind string) string {
	return strings.ToLower(kind)
}

// highestSemverTag selects the highest dotted-numeric (MAJOR.MINOR.PATCH) tag
// from a registry's tag list — the resolver-reported version for a pure
// bare-name first pull. Tags that are not dotted-numeric semver (e.g. "latest",
// build-metadata tags) are ignored; an empty result means no usable version.
func highestSemverTag(tags []string) string {
	best := ""
	for _, t := range tags {
		if !isDottedNumeric(t) {
			continue
		}
		if best == "" || compareDottedNumeric(t, best) > 0 {
			best = t
		}
	}
	return best
}

func isDottedNumeric(v string) bool {
	if v == "" {
		return false
	}
	for _, part := range strings.Split(v, ".") {
		if _, err := strconv.Atoi(part); err != nil {
			return false
		}
	}
	return true
}

// compareDottedNumeric compares two dotted-numeric versions component-by-component
// (numeric, not lexical), returning -1, 0, +1. A shorter version compares as if
// padded with zeros (1.0 == 1.0.0). Callers pre-filter with isDottedNumeric.
func compareDottedNumeric(a, b string) int {
	ap := strings.Split(a, ".")
	bp := strings.Split(b, ".")
	n := len(ap)
	if len(bp) > n {
		n = len(bp)
	}
	for i := 0; i < n; i++ {
		var av, bv int
		if i < len(ap) {
			av, _ = strconv.Atoi(ap[i])
		}
		if i < len(bp) {
			bv, _ = strconv.Atoi(bp[i])
		}
		if av != bv {
			if av < bv {
				return -1
			}
			return 1
		}
	}
	return 0
}
