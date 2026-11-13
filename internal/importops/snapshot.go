// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/opprogress"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcecache"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// catalogSnapshotRead says which published catalog snapshot an import reads.
//
// upgrade asks for the newest snapshot the registry publishes, fetched now and
// made the one the workspace reads from then on, whatever the workspace or the
// record beside the drafts holds. pinned is the manifest digest the record beside
// the drafts names — the record of an earlier import into the same directory,
// read whether or not the import is a sync — empty where there is no record or it
// names none: an import into drafts an earlier import wrote reads the catalog they
// were imported against.
type catalogSnapshotRead struct {
	upgrade bool
	pinned  string
}

// catalogSnapshot is the published catalog snapshot an import read: the directory
// it is laid out as in the content cache and its manifest digest, which the record
// beside the drafts names. Where the workspace's default registry publishes no
// snapshot both are empty, and notice says so, in words the import reports as a
// warning.
type catalogSnapshot struct {
	dir    string
	digest string
	notice string
}

// snapshotRemedySteps names, in the remedy a registry refusal carries, the steps
// an import's reader takes: running the import again, and, for a snapshot that did
// not hold together, running it with --upgrade, which fetches the newest one
// afresh. An import writes no lockfile, so no pull is the step to run.
var snapshotRemedySteps = oci.RemedySteps{
	Rerun:   "the import",
	Refresh: "the import with --upgrade, which fetches the newest catalog snapshot afresh",
}

// nameACatalogFolder is the other way out of every refusal here: a workspace that
// names a catalog folder imports against it and fetches no snapshot.
const nameACatalogFolder = "or name a catalog folder for the workspace — in AGGREGA_CATALOG_DIRS or under " +
	"workspace.catalog_dirs — to import against it instead"

// readCatalogSnapshot returns the published catalog snapshot an import in rw reads,
// laid out as a catalog folder in the workspace's content cache, for a workspace
// that names no catalog folder.
//
// Which one, in order: with read.upgrade, the newest the workspace's default
// registry publishes, fetched now; otherwise the one read.pinned names, where the
// record beside the drafts names one; otherwise the one this workspace already reads
// (oci.CatalogSnapshotCurrentPath); and where it reads none yet, the newest
// published, fetched now. A snapshot named by digest that the content cache holds
// is read from there, held to that digest, and no registry is asked anything; one
// it does not hold is fetched by that digest — a record's pin is fetched as the
// pin, never as whatever is newest, so a cleared cache does not move a re-run to
// another catalog. A snapshot fetched as the newest published becomes the one the
// workspace reads from then on; one fetched by a record's digest does not.
//
// Every fetch goes through the workspace's egress gate and the default registry's
// own sign-in, and is verified before anything is kept: the archive held to the
// manifest it came in, and the manifest's signature to the registry's trust policy
// (pullverify.VerifyKindlessArtifact). Under the offline posture nothing is
// fetched, and an import needing a fetch is refused, naming what to run.
//
// A registry that answers that it publishes no snapshot at all leaves the import
// with no catalog, which notice reports; asked for one by digest, it refuses.
func readCatalogSnapshot(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace,
	read catalogSnapshotRead) (catalogSnapshot, error) {
	if read.upgrade {
		// A refresh is a question only the registry answers, so under the offline
		// posture it is refused whatever the machine holds, as a pull's is.
		if err := egress.RefuseIfOffline(rw, "refreshing the published catalog asks the workspace's default "+
			"registry for the newest catalog snapshot it publishes"); err != nil {
			return catalogSnapshot{}, err
		}
		return fetchCatalogSnapshot(ctx, rw, "", true)
	}
	want, err := pinnedSnapshot(rw, read.pinned)
	if err != nil {
		return catalogSnapshot{}, err
	}
	if want != "" {
		dir, held, err := heldCatalogSnapshot(rw, want)
		if err != nil || held {
			return catalogSnapshot{dir: dir, digest: want}, err
		}
		return fetchCatalogSnapshot(ctx, rw, want, read.pinned == "")
	}
	return fetchCatalogSnapshot(ctx, rw, "", true)
}

// pinnedSnapshot returns the manifest digest of the snapshot an import reads where
// it fetches nothing new: the one pinned names, or else the one the workspace
// already reads, or "" where it reads none.
func pinnedSnapshot(rw *workspaceconfig.ResolvedWorkspace, pinned string) (string, error) {
	if pinned != "" {
		return pinned, nil
	}
	root, cacheDir := rw.Root(), rw.Config().ResolvedArtifactCacheDir()
	data, err := oci.ReadSlotFile(root, cacheDir, oci.CatalogSnapshotsRoot(root, cacheDir), oci.CatalogSnapshotCurrentFile)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s, which names the catalog snapshot this workspace reads: %w",
			oci.CatalogSnapshotCurrentPath(root, cacheDir), err)
	}
	// A file that names no digest is read as naming none, so the next import fetches
	// the newest snapshot and writes it afresh rather than stopping on a file the
	// workspace's content cache keeps for itself.
	if digest := strings.TrimSpace(string(data)); wellFormedDigest(digest) {
		return digest, nil
	}
	return "", nil
}

// heldCatalogSnapshot returns the directory the snapshot whose manifest digest is
// manifestDigest is laid out as, and true, where the workspace's content cache
// holds it; false, with no error, where it does not.
//
// What it holds is held to the digest first: the manifest kept in the slot must
// hash to it, and the archive kept beside it to the layer that manifest names. A
// slot that does not is refused, naming the remedy, rather than fetched around: a
// snapshot that is there and is wrong is not quietly replaced. The tree is then
// laid out from the archive just held (sourcecache.RegenerateExtracted), which
// rebuilds whatever in it does not match the archive, so what the catalog load
// reads is the archive's content and nothing a hand put there.
func heldCatalogSnapshot(rw *workspaceconfig.ResolvedWorkspace, manifestDigest string) (string, bool, error) {
	root, cacheDir := rw.Root(), rw.Config().ResolvedArtifactCacheDir()
	slot, err := oci.CatalogSnapshotSlot(root, cacheDir, manifestDigest)
	if err != nil {
		return "", false, err
	}
	// A slot missing either file is a slot a keep did not finish, which holds no
	// snapshot; the keep writes the manifest last.
	manifest, err := oci.ReadSlotFile(root, cacheDir, slot, oci.SlotManifestFile)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read the catalog snapshot kept in %s: %w", slot, err)
	}
	blob, err := oci.ReadSlotFile(root, cacheDir, slot, oci.SlotBlobFile)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read the catalog snapshot kept in %s: %w", slot, err)
	}
	if err := oci.VerifyManifestDigest(manifest, manifestDigest); err != nil {
		return "", false, heldSnapshotRefusal(slot, err)
	}
	if err := oci.VerifyContentAgainstManifest(manifest, blob, oci.MediaTypeCatalogSnapshot); err != nil {
		return "", false, heldSnapshotRefusal(slot, err)
	}
	dir, err := sourcecache.RegenerateExtracted(root, cacheDir, slot, blob)
	if err != nil {
		return "", false, fmt.Errorf("lay out the catalog snapshot %s kept in %s: %w", manifestDigest, slot, err)
	}
	return dir, true, nil
}

// heldSnapshotRefusal is the refusal of a snapshot the content cache keeps that
// does not match the digest it is kept under.
func heldSnapshotRefusal(slot string, err error) error {
	return fmt.Errorf("the catalog snapshot kept in %s does not match the digest it is kept under (%w); run "+
		"the import with --upgrade, which fetches the newest catalog snapshot afresh, or remove that directory "+
		"and run the import again", slot, err)
}

// fetchCatalogSnapshot fetches a catalog snapshot from the workspace's default
// registry — the one manifestDigest names, or the newest published where it is
// empty — verifies it, keeps it in the content cache, lays it out there, and
// returns where. makeCurrent records it as the snapshot the workspace reads from
// then on.
//
// Nothing is kept until everything holds: the archive is held to the manifest it
// came in by the fetch itself (oci.Pull), the manifest to the digest asked for
// where one was, and its signature to the registry's trust policy through the
// pull-time check every fetched artifact gets, asked about an artifact of no
// catalog kind (pullverify.VerifyKindlessArtifact) — so a policy requiring
// signatures requires one of a snapshot whatever kinds it is scoped to, and the
// official hub requires one of every artifact it serves. The tree is laid out before the archive and its
// manifest are written beside it, so a slot is read as holding the snapshot only
// once all three are in place.
func fetchCatalogSnapshot(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, manifestDigest string,
	makeCurrent bool) (catalogSnapshot, error) {
	wsCfg := rw.Config()
	prefix := wsCfg.DefaultRegistryLocation().Prefix()
	if prefix == "" {
		return catalogSnapshot{}, fmt.Errorf("the workspace names no catalog folder and no default registry to " +
			"fetch the published catalog from; set default_registry, " + nameACatalogFolder)
	}
	reference := catalog.SnapshotReference(prefix)
	tag := manifestDigest
	if tag == "" {
		tag = catalog.SnapshotLatestTag
	}
	asked := reference + ":" + tag
	if manifestDigest != "" {
		asked = reference + "@" + manifestDigest
	}

	httpClient, err := egress.HTTPClient(wsCfg)
	if err != nil {
		if errors.Is(err, egress.ErrOfflineEgress()) {
			return catalogSnapshot{}, offlineSnapshotRefusal(rw, manifestDigest, err)
		}
		return catalogSnapshot{}, err
	}
	if err := rw.CacheWriteRefusal(); err != nil {
		return catalogSnapshot{}, err
	}
	repo, err := oci.NewRemoteRepository(reference, httpClient)
	if err != nil {
		return catalogSnapshot{}, err
	}

	end := opprogress.Begin(ctx, "fetching the published catalog "+asked)
	pulled, err := oci.Pull(ctx, repo, tag, oci.MediaTypeCatalogSnapshot)
	end(err)
	if err != nil {
		if manifestDigest == "" && errors.Is(err, oci.ErrFetchNotFound()) && ctx.Err() == nil {
			return catalogSnapshot{notice: fmt.Sprintf("the workspace names no catalog folder, and its default "+
				"registry %s publishes no catalog snapshot at %s, so this import reads no catalog; name a catalog "+
				"folder — in AGGREGA_CATALOG_DIRS or under workspace.catalog_dirs — to import against one",
				registryhost.Of(reference), reference)}, nil
		}
		return catalogSnapshot{}, snapshotFetchRefusal(ctx, asked, registryhost.Of(reference), err)
	}
	if len(pulled.ManifestBytes) == 0 {
		return catalogSnapshot{}, fmt.Errorf("%s: the registry served no manifest body to keep: %w", asked,
			oci.ErrFetchIntegrity())
	}
	if manifestDigest != "" && pulled.Digest != manifestDigest {
		return catalogSnapshot{}, fmt.Errorf("%s answered with the manifest %s: %w", asked, pulled.Digest,
			oci.ErrFetchIntegrity())
	}

	end = opprogress.Begin(ctx, "verifying the signature of "+reference+"@"+pulled.Digest)
	_, err = pullverify.VerifyKindlessArtifact(ctx, wsCfg, reference, pulled.Digest, repo,
		func(notice string) { fmt.Fprintf(opprogress.Beside(ctx, os.Stderr), "  verify: %s\n", notice) })
	end(err)
	if err != nil {
		return catalogSnapshot{}, fmt.Errorf("catalog snapshot %s@%s: %w", reference, pulled.Digest, err)
	}

	dir, err := keepCatalogSnapshot(rw, pulled, makeCurrent)
	if err != nil {
		return catalogSnapshot{}, fmt.Errorf("keep the catalog snapshot %s@%s: %w", reference, pulled.Digest, err)
	}
	return catalogSnapshot{dir: dir, digest: pulled.Digest}, nil
}

// offlineSnapshotRefusal is the refusal of an import that needs a catalog snapshot
// fetched while the workspace is offline: the offline condition, naming the layer
// that set the posture (egress.RefuseIfOffline), followed by what puts a snapshot
// on the machine. manifestDigest is the snapshot asked for, empty for the newest
// published; offline is the refusal the egress gate returned, kept where the
// posture cannot be attributed.
func offlineSnapshotRefusal(rw *workspaceconfig.ResolvedWorkspace, manifestDigest string, offline error) error {
	act := "the workspace names no catalog folder and this machine holds no catalog snapshot for it, so the " +
		"import would fetch the newest one its default registry publishes"
	if manifestDigest != "" {
		act = "the workspace names no catalog folder, and the catalog snapshot " + manifestDigest + " the import " +
			"reads is not on this machine, so the import would fetch it from the workspace's default registry"
	}
	refusal := egress.RefuseIfOffline(rw, act)
	if refusal == nil {
		refusal = fmt.Errorf("%s: %w", act, offline)
	}
	return fmt.Errorf("%w\n  to import with no network, run the import once with the network available, which "+
		"fetches the catalog snapshot and keeps it on this machine for every later import, %s", refusal,
		nameACatalogFolder)
}

// snapshotFetchRefusal is the refusal of a fetch of the catalog snapshot asked for
// from host that failed with err: a registry that answered says why, with the
// remedy its condition names; anything else is a registry that could not be
// reached, which a run with the network available gets past.
func snapshotFetchRefusal(ctx context.Context, asked, host string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("fetch the catalog snapshot %s: %w", asked, ctxErr)
	}
	refusal := fmt.Errorf("fetch the catalog snapshot %s: %w", asked, err)
	if _, coded := cerrors.CodeOf(err); !coded {
		return fmt.Errorf("%w; the registry %s could not be reached: run the import again with the network "+
			"available, %s", refusal, host, nameACatalogFolder)
	}
	if remedy := oci.RemediationNaming(err, host, snapshotRemedySteps); remedy != "" {
		return fmt.Errorf("%w\n  %s", refusal, remedy)
	}
	return refusal
}

// keepCatalogSnapshot writes a fetched, verified snapshot into its slot in the
// workspace's content cache — the tree laid out first, then the archive, then the
// manifest — makes it the one the workspace reads where makeCurrent says so, and
// returns the directory the tree is laid out in.
//
// Every write goes through the cache's one slot write (oci.WriteSlotFile), which
// holds it, before it is made, to the root the cache directory picks, so a link on
// the way into the cache that leads out of it is refused rather than written
// through.
func keepCatalogSnapshot(rw *workspaceconfig.ResolvedWorkspace, pulled *oci.PullResult, makeCurrent bool) (string, error) {
	root, cacheDir := rw.Root(), rw.Config().ResolvedArtifactCacheDir()
	slot, err := oci.CatalogSnapshotSlot(root, cacheDir, pulled.Digest)
	if err != nil {
		return "", err
	}
	dir, err := sourcecache.RegenerateExtracted(root, cacheDir, slot, pulled.Bytes)
	if err != nil {
		return "", err
	}
	if err := oci.WriteSlotFile(root, cacheDir, slot, oci.SlotBlobFile, pulled.Bytes); err != nil {
		return "", err
	}
	if err := oci.WriteSlotFile(root, cacheDir, slot, oci.SlotManifestFile, pulled.ManifestBytes); err != nil {
		return "", err
	}
	if makeCurrent {
		if err := oci.WriteSlotFile(root, cacheDir, oci.CatalogSnapshotsRoot(root, cacheDir), oci.CatalogSnapshotCurrentFile,
			[]byte(pulled.Digest+"\n")); err != nil {
			return "", err
		}
	}
	return dir, nil
}
