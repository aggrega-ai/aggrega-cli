// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importops

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/aggrega-ai/aggrega-cli/internal/pluginfetchverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// carriedModuleIs is what the refusal of a core importer's module names the
// digest it was held to as.
const carriedModuleIs = "the digest this compiler release publishes"

// carriedImporterCascade is the plugin cascade the module of uri, a core importer
// the import's view serves from what the binary carries, is read through: the
// binary's own tier and the workspace's content cache, with accepted — the digest
// of the one module the binary accepts for it — as the pin every tier's bytes are
// held to and as the version the content cache's slot is addressed by.
//
// It is handed no directories beside catalog entities. A core importer has no
// entity in a catalog directory, and a module laid beside a catalog entity of its
// identity belongs to that entity at whatever version it is.
//
// It is built here rather than taken from the resolver a surface supplies, and
// that is the one plugin an import resolves this way. A surface's resolver holds a
// module to the pins the workspace records and fronts it with the workspace's own
// entity of its identity, and neither is a statement about this module: the binary
// names the module, by its digest, and its slot in the content cache is addressed
// by that digest — where a fetch of it writes it — which no pin the workspace
// records addresses.
func carriedImporterCascade(rw *workspaceconfig.ResolvedWorkspace, uri, accepted string) pluginpath.Resolver {
	return pluginpath.AcceptedDigest(uri, accepted, carriedModuleIs).Resolver(pluginpath.ResolutionContext{
		WorkspaceRoot: rw.Root(),
		CacheDir:      rw.Config().ResolvedArtifactCacheDir(),
	})
}

// resolveCarriedImporter returns the module of p, a core importer the import's
// view serves from what the binary carries, held to accepted, the digest of the
// one module the binary accepts for it.
//
// It reads the module through carriedImporterCascade. Where no tier holds it, it
// acquires it (acquireCarriedImporter) and reads it again through the same
// cascade, so the module an import runs is read from the content cache whether
// this import fetched it or an earlier one did, and is held to accepted on that
// read as on every other. Any other refusal the cascade returns — a module in the
// content cache that is not the one accepted among them — is returned as it is:
// a module that is there and is wrong is refused, never fetched around.
func resolveCarriedImporter(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, p types.Plugin,
	accepted string) (pluginpath.ResolvedPlugin, error) {
	uri := p.ArtifactReference()
	cascade := carriedImporterCascade(rw, uri, accepted)
	resolved, err := cascade.ResolveContext(ctx, uri)
	if err == nil || !errors.Is(err, pluginpath.ErrPluginArtifactNotFound()) {
		return resolved, err
	}
	if err := acquireCarriedImporter(ctx, rw, p, accepted); err != nil {
		return pluginpath.ResolvedPlugin{}, err
	}
	return cascade.ResolveContext(ctx, uri)
}

// carriedImporterSource returns where the module of uri, a core importer the
// import's view serves from what the binary carries, is fetched from and its
// signature asked about: the location the one binding of a plugin identity to a
// registry names (catalog.PluginSignatureSubjectOf) under the workspace's default
// registry location, and that location's host — or false where the workspace
// names no default registry.
//
// It is the one place an import decides which registry it reaches, and it
// decides from wsCfg alone. Nothing a caller sends with an import names a
// registry, so the host is always the default registry's own. For a
// configuration the resolution accepted, that is a host
// wsCfg.IsConfiguredRegistry admits — the bound a registry read a caller asks
// for over the assistant tools' listener is held to — whichever way the default
// was written: left to the built-in one, a host, a host followed by a repository
// path, or the name of an entry under registries.
func carriedImporterSource(wsCfg workspaceconfig.Config, uri string) (catalog.SignatureSubject, bool) {
	return catalog.PluginSignatureSubjectOf(wsCfg.DefaultRegistryLocation().Prefix(), uri)
}

// acquireCarriedImporter fetches the module of p, a core importer no tier of its
// cascade holds, from the workspace's default registry, and writes it where that
// cascade's content-cache tier reads it — or refuses, writing nothing.
//
// The compiler's release publishes each core importer to its registry under the
// importer's identity and the release's version, and the workspace's default
// registry is that registry, so the module is fetched from the location
// carriedImporterSource names, at p's version. Every step that reaches the
// network goes through the workspace's egress gate, so under the offline posture
// nothing is dialled.
//
// The fetched bytes are held to accepted first, through the cascade's own
// comparison (pluginpath.HoldToDigest), then verified through the one
// verification every fetched plugin passes (pluginfetchverify.Verify): the
// registry's signature on the manifest they came in, under that registry's trust
// policy, and the host functions the module imports against the ones p's entity
// records. Only bytes that pass both are written, as the content and the manifest
// of the content cache's slot for the importer at accepted — the shape a pull
// writes a plugin it fetched in. That write is the only one: nothing reaches the
// workspace's lockfile or the record of the plugins it trusts, because an import
// pins nothing.
//
// Where the module cannot be fetched — the workspace offline, no default registry,
// no registry answering, or the registry holding no such module — it refuses
// under the plugin-artifact-not-found condition, naming the importer, its version
// and what puts the module on the machine. A registry that answers and refuses
// the request, or serves something that does not verify, is refused as that.
func acquireCarriedImporter(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, p types.Plugin,
	accepted string) error {
	uri := p.ArtifactReference()
	version := p.Metadata.Version
	wsCfg := rw.Config()

	subject, ok := carriedImporterSource(wsCfg, uri)
	if !ok {
		return carriedImporterNotOnMachine(p, "the workspace names no default registry to fetch it from", false)
	}
	httpClient, err := egress.HTTPClient(wsCfg)
	if err != nil {
		if errors.Is(err, egress.ErrOfflineEgress()) {
			return carriedImporterNotOnMachine(p, "the workspace is offline, so its registry "+subject.Host+
				" is not asked for it", true)
		}
		return err
	}
	if err := rw.CacheWriteRefusal(); err != nil {
		return err
	}
	puller := catalog.NewDefaultOCIPuller(wsCfg)
	if puller == nil {
		return carriedImporterNotOnMachine(p, "the workspace is offline, so its registry "+subject.Host+
			" is not asked for it", true)
	}

	fetched, err := puller.Pull(ctx, subject.Location, version)
	if err != nil {
		return carriedImporterFetchRefusal(ctx, p, subject, err)
	}
	from := subject.Location + ":" + version
	held := pluginpath.ResolvedPlugin{Bytes: fetched.Bytes, Tier: pluginpath.TierRegistry, URI: uri}
	if err := pluginpath.HoldToDigest(held, accepted, carriedModuleIs); err != nil {
		return fmt.Errorf("core importer %q fetched from %s: %w", p.Coordinate(), from, err)
	}
	entity := p
	if _, err := pluginfetchverify.Verify(ctx, pluginfetchverify.Fetched{
		URI:            uri,
		ManifestDigest: fetched.Digest,
		Host:           subject.Host,
		Location:       subject.Location,
		Kind:           subject.Kind,
		Entity:         &entity,
		EntityURI:      uri,
		ArtifactURI:    from,
		WasmBytes:      fetched.Bytes,
	}, wsCfg, pluginfetchverify.RemoteTargetFor(httpClient), os.Stderr); err != nil {
		return fmt.Errorf("core importer %q fetched from %s: %w", p.Coordinate(), from, err)
	}

	root, cacheDir := rw.Root(), wsCfg.ResolvedArtifactCacheDir()
	if err := oci.WriteCached(root, cacheDir, uri, accepted, fetched.Bytes); err != nil {
		return fmt.Errorf("cache core importer %q fetched from %s: %w", p.Coordinate(), from, err)
	}
	if len(fetched.ManifestBytes) > 0 {
		if err := oci.WriteCachedManifest(root, cacheDir, uri, accepted, fetched.ManifestBytes); err != nil {
			return fmt.Errorf("cache core importer %q fetched from %s: %w", p.Coordinate(), from, err)
		}
	}
	return nil
}

// importRemedySteps names, in the remedy a registry refusal carries, the steps an
// import's reader takes: running the import again, which asks the registry once
// more and, for a module that did not hold together, fetches it afresh. An import
// writes no lockfile, so no pull is the step to run.
var importRemedySteps = oci.RemedySteps{
	Rerun:   "the import",
	Refresh: "the import again, which fetches the module afresh",
}

// carriedImporterFetchRefusal is the refusal of a fetch of p's module from
// subject that failed with err.
//
// A registry that answered says why, and that is the refusal: one holding no
// such module is a module not on this machine that no run can fetch, and one that
// refused the request or served something that does not hold together is
// returned as its own condition, with the remedy that condition names, naming the
// import as the step to run again (importRemedySteps). Anything else is a
// registry that did not answer, which a run with the network available gets
// past.
func carriedImporterFetchRefusal(ctx context.Context, p types.Plugin, subject catalog.SignatureSubject, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("fetch core importer %q: %w", p.Coordinate(), ctxErr)
	}
	if errors.Is(err, oci.ErrFetchNotFound()) {
		return carriedImporterNotOnMachine(p, "its registry "+subject.Host+" holds no "+subject.Location+":"+
			p.Metadata.Version, false)
	}
	if _, coded := cerrors.CodeOf(err); coded && !errors.Is(err, oci.ErrFetchNetworkUnreachable()) {
		refusal := fmt.Errorf("fetch core importer %q from %s:%s: %w", p.Coordinate(), subject.Location,
			p.Metadata.Version, err)
		if remedy := oci.RemediationNaming(err, subject.Host, importRemedySteps); remedy != "" {
			return fmt.Errorf("%w\n  %s", refusal, remedy)
		}
		return refusal
	}
	return carriedImporterNotOnMachine(p, fmt.Sprintf("its registry %s could not be reached (%v)", subject.Host, err), true)
}

// carriedImporterNotOnMachine is the refusal of p, a core importer whose module
// no tier holds and no registry supplied on this run, for the reason given:
// the plugin-artifact-not-found condition, naming the importer, its version and
// what puts its module on the machine. viaNetwork says whether a run with the
// network available would fetch it; where the registry answered that it holds no
// such module, or the workspace names no registry to ask, it would not, and the
// one remedy left is placing the module.
func carriedImporterNotOnMachine(p types.Plugin, reason string, viaNetwork bool) error {
	place := fmt.Sprintf("place its module, named %s, beside a Plugin entity of %s at %s in a catalog folder",
		pluginpath.PluginLeaf(p.ArtifactReference()), p.Coordinate(), p.Metadata.Version)
	remedy := place
	if viaNetwork {
		remedy = "run the import once with the network available, which fetches it from the workspace's default " +
			"registry, verifies it and keeps it in the content cache for every later import, or " + place
	}
	return fmt.Errorf("core importer %q at version %s is not on this machine, and %s; %s: %w",
		p.Coordinate(), p.Metadata.Version, reason, remedy, pluginpath.ErrPluginArtifactNotFound())
}
