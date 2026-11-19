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
// record beside the drafts holds. pinned is the manifest digest the import's
// entry in the record beside the drafts names — the entry of an earlier import
// into the same directory, read whether or not the import is a sync — empty where
// there is no such entry or it names none: an import into drafts an earlier
// import wrote reads the catalog they were imported against.
//
// sync says the import brings the drafts of the earlier import its entry records
// up to date with the source.
//
// folders says the workspace names catalog folders, which the snapshot is read
// behind. An import over folders that does not ask for the newest snapshot goes
// on over the folders alone where the snapshot cannot be read — the workspace is
// offline and this machine does not hold it, the workspace names no default
// registry, or the registry cannot be reached — rather than being refused, as an
// import with no folder is; the notice of the snapshot it read none of says why.
// A sync whose entry pins a snapshot is the exception (syncPin).
type catalogSnapshotRead struct {
	upgrade bool
	pinned  string
	sync    bool
	folders bool
}

// syncPin reports whether r is a sync whose entry in the record pins the
// snapshot it reads. Such a sync reads that snapshot or nothing: the drafts it
// judges were drafted against that catalog, and the folders alone may draft them
// otherwise — a Component the snapshot holds missing from them, say — so the sync
// would write, and remove, what the source never changed.
func (r catalogSnapshotRead) syncPin() bool { return r.sync && r.pinned != "" }

// overFoldersAlone reports whether an import reading r goes on over the
// workspace's catalog folders alone where the snapshot cannot be read. An import
// asking for the newest snapshot is refused instead, since only a fetch answers
// what it asked, and so is a sync whose entry pins the snapshot (syncPin).
func (r catalogSnapshotRead) overFoldersAlone() bool { return r.folders && !r.upgrade && !r.syncPin() }

// catalogSnapshot is the published catalog snapshot an import read: the directory
// it is laid out as in the content cache and its manifest digest. Where the import
// reads none — the workspace's default registry publishes no snapshot, or a
// workspace naming catalog folders cannot read it — both are empty, and notice
// says why, in words the import reports as a warning.
//
// kept is the pin the record beside the drafts named for an import that read the
// workspace's catalog folders alone because the snapshot it pins could not be
// read: a pin names the catalog later runs read, and a run that could not read
// it does not move it. It is empty for every other import.
type catalogSnapshot struct {
	dir    string
	digest string
	notice string
	kept   string
}

// recorded returns the manifest digest the record an import writes names: the
// snapshot it read, or, where it read none, the pin it kept — empty where it
// read none and kept none.
func (s catalogSnapshot) recorded() string {
	if s.digest != "" {
		return s.digest
	}
	return s.kept
}

// snapshotRemedySteps names, in the remedy a registry refusal carries, the steps
// an import's reader takes: running the import again, and, for a snapshot that did
// not hold together, running it with --upgrade, which fetches the newest one
// afresh. An import writes no lockfile, so no pull is the step to run.
var snapshotRemedySteps = oci.RemedySteps{
	Rerun:   "the import",
	Refresh: "the import with --upgrade, which fetches the newest catalog snapshot afresh",
}

// nameACatalogFolder is the other way out of a refusal in a workspace that names
// no catalog folder: a workspace that names one imports over it alone where the
// snapshot behind it cannot be read.
const nameACatalogFolder = "or name a catalog folder for the workspace — in AGGREGA_CATALOG_DIRS or under " +
	"workspace.catalog_dirs — to import against it instead"

// foldersAloneNotice is the warning of an import reading the workspace's catalog
// folders alone because the published catalog behind them cannot be read: why,
// what the import reads instead, and then, where something reads the snapshot,
// what.
func foldersAloneNotice(why, then string) string {
	notice := why + ", so this import reads the workspace's catalog folders alone, without the published " +
		"catalog behind them"
	if then != "" {
		notice += "; " + then
	}
	return notice
}

// readCatalogSnapshot returns the published catalog snapshot an import in rw reads,
// laid out as a catalog folder in the workspace's content cache: the whole catalog
// of a workspace that names no catalog folder, and the catalog read behind the
// folders of one that names some.
//
// An import over the folders alone that the record beside the drafts pins a
// snapshot for keeps that pin (catalogSnapshot.kept), so the record it writes
// names the snapshot the next run that can read it reads.
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
// fetched, and an import needing a fetch is refused, naming what to run. A sync
// whose entry pins a snapshot this machine does not hold is refused wherever that
// snapshot cannot be fetched, folders or not, naming the pin (pinnedSyncRefusal).
//
// A registry that answers that it publishes no snapshot at all leaves the import
// with no snapshot, which notice reports; asked for one by digest, it refuses.
//
// An import over catalog folders that does not ask for the newest snapshot, and is
// not a sync whose entry pins one, is not refused for a snapshot it cannot read for
// want of the network or a registry — under the offline posture with the snapshot
// not on this machine, with no default registry named, or with the registry out of
// reach: it reads no snapshot, and notice says why and what reads one
// (read.overFoldersAlone). A sync whose entry pins a snapshot it cannot read so is
// refused, naming the pin and what reads it (read.syncPin). A snapshot that is
// read and does not verify, and a registry that answers with a refusal, refuse it
// as they refuse any import.
func readCatalogSnapshot(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace,
	read catalogSnapshotRead) (catalogSnapshot, error) {
	if read.upgrade {
		// A refresh is a question only the registry answers, so under the offline
		// posture it is refused whatever the machine holds, as a pull's is.
		if err := egress.RefuseIfOffline(rw, "refreshing the published catalog asks the workspace's default "+
			"registry for the newest catalog snapshot it publishes"); err != nil {
			return catalogSnapshot{}, err
		}
		return fetchCatalogSnapshot(ctx, rw, read, "", true)
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
		fetched, err := fetchCatalogSnapshot(ctx, rw, read, want, read.pinned == "")
		if err == nil && fetched.dir == "" {
			// A snapshot asked for by digest is never answered as unpublished, so
			// reading none here is an import over the folders alone, which keeps the
			// record's pin.
			fetched.kept = read.pinned
		}
		return fetched, err
	}
	return fetchCatalogSnapshot(ctx, rw, read, "", true)
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
// then on. read says whether the import goes on over the workspace's catalog
// folders alone where the snapshot cannot be fetched, and how a snapshot that is
// not published is reported.
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
func fetchCatalogSnapshot(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, read catalogSnapshotRead,
	manifestDigest string, makeCurrent bool) (catalogSnapshot, error) {
	wsCfg := rw.Config()
	prefix := wsCfg.DefaultRegistryLocation().Prefix()
	if prefix == "" {
		if read.overFoldersAlone() {
			return catalogSnapshot{notice: noDefaultRegistryNotice()}, nil
		}
		if read.syncPin() {
			return catalogSnapshot{}, pinnedSyncRefusal(manifestDigest, "the workspace names no default registry "+
				"to fetch it from", "set default_registry to the registry that publishes it and run the sync where "+
				"that registry can be reached", nil)
		}
		if read.folders {
			return catalogSnapshot{}, fmt.Errorf("the workspace names no default registry to fetch the newest " +
				"published catalog from; set default_registry")
		}
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
			if read.overFoldersAlone() {
				return catalogSnapshot{notice: offlineSnapshotNotice(rw, manifestDigest)}, nil
			}
			if read.syncPin() {
				return catalogSnapshot{}, offlinePinnedSyncRefusal(rw, manifestDigest, err)
			}
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
		host := registryhost.Of(reference)
		if manifestDigest == "" && errors.Is(err, oci.ErrFetchNotFound()) && ctx.Err() == nil {
			return catalogSnapshot{notice: unpublishedSnapshotNotice(read, host, reference)}, nil
		}
		if read.overFoldersAlone() && ctx.Err() == nil && unreached(err) {
			return catalogSnapshot{notice: foldersAloneNotice(fmt.Sprintf("the workspace's default registry %s "+
				"could not be reached for the catalog snapshot %s (%v)", host, asked, err), "run the import again "+
				"with the network available, which fetches the catalog snapshot and keeps it on this machine for "+
				"every later import")}, nil
		}
		if read.syncPin() && ctx.Err() == nil && unreached(err) {
			return catalogSnapshot{}, pinnedSyncRefusal(manifestDigest, fmt.Sprintf("the workspace's default "+
				"registry %s could not be reached for it", host), "run the sync where that registry can be reached, "+
				"which fetches the snapshot and keeps it on this machine for every later sync", err)
		}
		return catalogSnapshot{}, snapshotFetchRefusal(ctx, asked, host, read.folders, err)
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

// noDefaultRegistryNotice is the warning of an import over catalog folders in a
// workspace that names no default registry: it reads the folders alone, and
// naming one lets an import run online read the published catalog behind them.
func noDefaultRegistryNotice() string {
	return foldersAloneNotice("the workspace names no default registry to fetch the published catalog from",
		"set default_registry, and an import run online reads the published catalog behind the folders")
}

// unpublishedSnapshotNotice is the warning of an import whose workspace's default
// registry, at host, publishes no catalog snapshot at reference: the import reads
// no catalog where the workspace names no catalog folder, and its folders alone
// where it names some.
func unpublishedSnapshotNotice(read catalogSnapshotRead, host, reference string) string {
	if read.folders {
		return foldersAloneNotice(fmt.Sprintf("the workspace's default registry %s publishes no catalog snapshot "+
			"at %s", host, reference), "")
	}
	return fmt.Sprintf("the workspace names no catalog folder, and its default registry %s publishes no catalog "+
		"snapshot at %s, so this import reads no catalog; name a catalog folder — in AGGREGA_CATALOG_DIRS or "+
		"under workspace.catalog_dirs — to import against one", host, reference)
}

// offlineSnapshotNotice is the warning of an import over catalog folders that
// would fetch a catalog snapshot while the workspace is offline: why, naming the
// layer that set the posture (offlineWords), then that the import reads the
// folders alone, and that a run with the network available reads the snapshot
// behind them. manifestDigest is the snapshot asked for, empty for the newest
// published.
func offlineSnapshotNotice(rw *workspaceconfig.ResolvedWorkspace, manifestDigest string) string {
	act := "this machine holds no catalog snapshot for the workspace, which only its default registry can give"
	then := "run the import once with the network available, which fetches the catalog snapshot its default " +
		"registry publishes and keeps it on this machine for every later import"
	if manifestDigest != "" {
		act = "the catalog snapshot " + manifestDigest + " the import reads is not on this machine, which only the " +
			"workspace's default registry can give"
		then = "run the import once with the network available, which fetches it and keeps it on this machine for " +
			"every later import"
	}
	return foldersAloneNotice(offlineWords(rw, act), then)
}

// offlineWords is act followed by the layer that set the offline posture, in the
// words the offline refusal names it with (egress.RefuseIfOffline), without the
// condition that refusal wraps: a warning reports the posture, it does not refuse
// under it.
func offlineWords(rw *workspaceconfig.ResolvedWorkspace, act string) string {
	unattributed := act + ", and the workspace is offline"
	refusal := egress.RefuseIfOffline(rw, act)
	if refusal == nil {
		return unattributed
	}
	cause := errors.Unwrap(refusal)
	if cause == nil {
		return unattributed
	}
	if words, ok := strings.CutSuffix(refusal.Error(), ": "+cause.Error()); ok {
		return words
	}
	return unattributed
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

// pinnedSyncRefusal is the refusal of a sync whose entry in the record beside the
// drafts pins the catalog snapshot pin, which this machine does not hold and
// cannot fetch: why says why it cannot, remedy what reads it, and cause, where
// set, is the failure it wraps. The sync is refused rather than run over the
// workspace's catalog folders alone (catalogSnapshotRead.syncPin).
func pinnedSyncRefusal(pin, why, remedy string, cause error) error {
	refusal := fmt.Sprintf("the import's entry in the record beside the drafts pins the catalog snapshot %s, "+
		"which this machine does not hold, and %s; a sync reads the catalog its drafts were imported against, "+
		"never the workspace's catalog folders alone, so %s", pin, why, remedy)
	if cause == nil {
		return errors.New(refusal)
	}
	return fmt.Errorf("%s: %w", refusal, cause)
}

// offlinePinnedSyncRefusal is pinnedSyncRefusal under the offline posture: the
// offline condition, naming the layer that set the posture
// (egress.RefuseIfOffline), followed by what reads the pinned snapshot. offline is
// the refusal the egress gate returned, kept where the posture cannot be
// attributed.
func offlinePinnedSyncRefusal(rw *workspaceconfig.ResolvedWorkspace, pin string, offline error) error {
	act := "the import's entry in the record beside the drafts pins the catalog snapshot " + pin + ", which this " +
		"machine does not hold, so the sync would fetch it from the workspace's default registry"
	refusal := egress.RefuseIfOffline(rw, act)
	if refusal == nil {
		refusal = fmt.Errorf("%s: %w", act, offline)
	}
	return fmt.Errorf("%w\n  a sync reads the catalog its drafts were imported against, never the workspace's "+
		"catalog folders alone: run the sync once with the network available, where the workspace's default "+
		"registry can be reached, which fetches the snapshot and keeps it on this machine for every later sync",
		refusal)
}

// unreached reports whether err, the failure of a fetch from a registry, is a
// registry that could not be reached rather than one that answered: a registry
// that answered says why under a condition of its own.
func unreached(err error) bool {
	_, coded := cerrors.CodeOf(err)
	return !coded
}

// snapshotFetchRefusal is the refusal of a fetch of the catalog snapshot asked for
// from host that failed with err: a registry that answered says why, with the
// remedy its condition names; anything else is a registry that could not be
// reached, which a run with the network available gets past. folders says the
// workspace names catalog folders, so naming one is no way out.
func snapshotFetchRefusal(ctx context.Context, asked, host string, folders bool, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("fetch the catalog snapshot %s: %w", asked, ctxErr)
	}
	refusal := fmt.Errorf("fetch the catalog snapshot %s: %w", asked, err)
	if unreached(err) {
		rerun := "run the import again with the network available"
		if !folders {
			rerun += ", " + nameACatalogFolder
		}
		return fmt.Errorf("%w; the registry %s could not be reached: %s", refusal, host, rerun)
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
