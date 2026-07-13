// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package helm implements the source-resolver adapters for Helm
// chart sources: helm-oci-chart (OCI-distributed charts via ORAS)
// and helm-https-chart (HTTPS-distributed charts via Helm's getter
// machinery, with index.yaml resolution when the URL is a repo
// root).
//
// Both adapters consume Helm's native auth surfaces — the OCI
// adapter delegates credential lookup to oras-go's credential store
// (which reads ~/.docker/config.json, $XDG_RUNTIME_DIR/containers/
// auth.json, and ~/.config/helm/registry/config.json, including
// credential helpers); the HTTPS adapter constructs a minimal
// programmatic cli.EnvSettings to keep the helm getter from reading
// ambient environment variables.
//
// The OCI adapter pins the upstream archive verbatim (no
// normalisation) and catches drift via ErrFetchIntegrity on the
// next pull. The HTTPS adapter behaves the same way when the URL
// resolves to a direct .tgz, and normalises only when index.yaml
// resolution materialises a new archive — at which point the
// strict deterministic .tar.gz writer in internal/archive applies.
package helm

import (
	"context"
	"errors"
	"fmt"
	"strings"

	ocierr "github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/cacheslot"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/internal/archive"
	"oras.land/oras-go/v2"
)

// KindOCI names the helm-oci-chart source kind. The constant is the
// canonical wire value; adapters must not duplicate the literal in
// their Resolve implementations.
const KindOCI = "helm-oci-chart"

// MediatypeHelmChart is the canonical OCI mediatype for Helm chart
// artifacts. Recorded on every cache slot's metadata.yaml so cache
// inspection surfaces the same mediatype the registry advertised.
const MediatypeHelmChart = "application/vnd.cncf.helm.chart.content.v1.tar+gzip"

// ociTargetFactory builds an OCI target from a repository reference.
// Production wires this to oci.NewRemoteRepository; tests inject a
// memory CAS implementing oras.ReadOnlyTarget so the adapter is
// exercised end-to-end without network access.
type ociTargetFactory func(reference string) (oras.ReadOnlyTarget, error)

type ociAdapter struct {
	newTarget ociTargetFactory
}

// NewOCIAdapter returns a helm-oci-chart adapter that pulls via
// oci.NewRemoteRepository in production. Tests construct their own
// adapter via the unexported newOCIAdapter helper with an injected
// target factory.
func NewOCIAdapter() sourceresolver.Adapter {
	return &ociAdapter{newTarget: defaultOCITargetFactory}
}

func defaultOCITargetFactory(reference string) (oras.ReadOnlyTarget, error) {
	repo, err := ocierr.NewRemoteRepository(reference)
	if err != nil {
		return nil, err
	}
	return repo, nil
}

// Kind returns the helm-oci-chart kind string.
func (a *ociAdapter) Kind() string { return KindOCI }

// Resolve pulls the chart referenced by spec via ORAS, computes the
// content digest, populates the cache slot, and returns a
// ResolvedSource for the lockfile writer.
//
// spec.URL is the OCI repository reference (e.g.,
// "oci://hub.aggrega.ai/charts/mariadb" or
// "hub.aggrega.ai/charts/mariadb" — the oci:// prefix is stripped
// before passing to ORAS). spec.Version is the tag.
//
// Errors wrap one of the four fetch sentinels with adapter-specific
// context via sourceresolver.Wrap.
func (a *ociAdapter) Resolve(spec sourceresolver.SourceSpec) (*sourceresolver.ResolvedSource, error) {
	reference := strings.TrimPrefix(spec.URL, "oci://")
	target, err := a.newTarget(reference)
	if err != nil {
		return nil, sourceresolver.Wrap(spec, sourceresolver.ErrFetchNetworkUnreachable, err)
	}

	ctx := context.Background()
	// Select the chart content layer by media type: a signed chart's manifest
	// carries a detached provenance layer beside the content, and an index-based
	// selection could return the provenance blob. Passing the content media type
	// keeps the pull (and the content-digest verification inside it) anchored to
	// the chart content regardless of layer order.
	result, err := ocierr.Pull(ctx, target, spec.Version, MediatypeHelmChart)
	if err != nil {
		return nil, mapOCIError(spec, err)
	}

	digest := archive.Digest(result.Bytes)
	slot, err := cacheslot.Slot(spec.WorkspaceRoot, spec.CacheDir, KindOCI, spec.URL, "", spec.Version, digest)
	if err != nil {
		return nil, fmt.Errorf("helm-oci-chart: %w", err)
	}
	mediatype := result.MediaType
	if mediatype == "" {
		mediatype = MediatypeHelmChart
	}
	if err := cacheslot.WriteBlobAndMetadata(slot, result.Bytes,
		KindOCI, spec.URL, spec.Version, digest, mediatype, "", result.ManifestBytes); err != nil {
		return nil, fmt.Errorf("helm-oci-chart: %w", err)
	}

	return &sourceresolver.ResolvedSource{
		Kind:           KindOCI,
		URL:            spec.URL,
		PinnedVersion:  spec.Version,
		Digest:         digest,
		ManifestDigest: result.Digest,
		Mediatype:      mediatype,
		CachePath:      slot + "/blob.tgz",
		OriginalSource: spec.OriginalSource,
	}, nil
}

// mapOCIError maps a raw pkg/oci error to a sourceresolver-wrapped
// sentinel. The mapping is centralised so the kind→sentinel
// translation is auditable in one place; if the underlying error
// chain carries one of the four ErrFetch* sentinels, that sentinel
// is preserved through the wrap.
func mapOCIError(spec sourceresolver.SourceSpec, cause error) error {
	switch {
	case errors.Is(cause, ocierr.ErrFetchNotFound):
		return sourceresolver.Wrap(spec, sourceresolver.ErrFetchNotFound, cause)
	case errors.Is(cause, ocierr.ErrFetchUnauthorized):
		return sourceresolver.Wrap(spec, sourceresolver.ErrFetchUnauthorized, cause)
	case errors.Is(cause, ocierr.ErrFetchNetworkUnreachable):
		return sourceresolver.Wrap(spec, sourceresolver.ErrFetchNetworkUnreachable, cause)
	case errors.Is(cause, ocierr.ErrFetchIntegrity):
		return sourceresolver.Wrap(spec, sourceresolver.ErrFetchIntegrity, cause)
	default:
		return sourceresolver.Wrap(spec, sourceresolver.ErrFetchNetworkUnreachable, cause)
	}
}

func init() {
	sourceresolver.Register(NewOCIAdapter())
}
