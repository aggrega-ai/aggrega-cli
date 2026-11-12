// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package gitrepo implements the git-repository source-resolver
// adapter. The adapter clones a Git repository to a temporary
// directory (production path; bounded peak memory for large
// repositories), resolves the user-supplied ref (tag, branch, or
// commit SHA) to an immutable commit SHA via ResolveRevision,
// checks out at that SHA, archives the working tree (or the
// optional ModuleSubdir) into a deterministic .tar.gz, computes
// the content digest, populates the cache slot, and returns a
// ResolvedSource with the commit SHA recorded as PinnedVersion.
//
// The adapter does not interpret what the archived directory holds. It
// retrieves bytes from a repository and asserts nothing about their
// format; whichever provider consumes the archive is what knows
// whether the directory is a chart, a module, or anything else.
// That is why the kind names the transport and the location rather
// than a format.
//
// The checkout is a root of its own. A repository can commit links, and
// a link can lead anywhere on the machine running the pull, so every
// link the archive meets — the ModuleSubdir itself included — is judged
// against the checkout before anything is read through it: one leading
// inside is archived as the file or directory it leads to, and one
// leading out halts the pull with the read hold's refusal, naming the
// link and where it leads, with nothing cached. The checkout is removed
// once the pull halts, so the refusal names the link by its place in the
// repository, and the repository and revision the source names. A
// ModuleSubdir that climbs above the checkout as written is refused before
// anything is fetched, with the refusal its cache slot gives it, so no file
// beside the checkout is read.
//
// Ref normalisation is load-bearing: only commit SHAs are
// immutable; tags and branches may be re-pushed at the registry.
// The adapter resolves at lock time so the lockfile always pins a
// commit SHA. A ref that resolves to a different commit than the
// one it pinned halts the next pull instead of being adopted;
// "aggrega pull --upgrade --release <release>" adopts it.
//
// Auth: over HTTPS (and plain HTTP) the clone sends the credential the
// netrc file holds for the repository's host (see package netrc); a URL
// carrying a credential of its own is refused as its document is read and
// reaches no clone. Over SSH, go-git authenticates through the SSH agent
// and no netrc file is read. No gitconfig credential helper is read. The adapter passes
// spec.URL through to git.PlainCloneContext unchanged, so the URL shapes go-git accepts
// work (https://, http://, ssh://, git://); git+ssh:// is not one of them.
package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcekind"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/cacheslot"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/internal/archive"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/netrc"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	transportclient "github.com/go-git/go-git/v5/plumbing/transport/client"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

// Kind names the git-repository source kind, whose one declaration is
// [sourcekind.GitRepository].
const Kind = sourcekind.GitRepository

// MediatypeTarGz is the canonical media-type recorded for the archives
// this adapter builds. Generic application/gzip is correct precisely
// because the adapter does not know or care what the archived
// directory holds, so no format-specific media-type would be truthful.
const MediatypeTarGz = "application/gzip"

// cloneFunc is the seam between production go-git CloneContext and
// test-injected clone behaviour. Production wires this to a thin
// wrapper around git.PlainCloneContext (filesystem-backed) that also
// receives the network-egress client. Tests inject their own
// implementation that clones from a local fixture repo and ignores the
// client argument. auth is the credential the clone sends, nil for none.
type cloneFunc func(ctx context.Context, destDir, url string, httpClient *http.Client, auth transport.AuthMethod) (*git.Repository, error)

type adapter struct {
	clone cloneFunc
}

// NewAdapter returns a git-repository adapter wired to
// git.PlainCloneContext in production.
func NewAdapter() sourceresolver.Adapter {
	return &adapter{clone: defaultClone}
}

// defaultClone is the production clone. It refuses an offline workspace before
// touching the network (a nil client is the egress gate's offline signal), and
// for HTTP(S) remotes it wires the gate's client into go-git's HTTP transport so
// go-git's network egress crosses the same gate the rest of the compiler does.
// go-git exposes no per-clone HTTP-client option, so the transport is installed
// through the library's process-global protocol registry; the client wraps the
// gate's shared transport, so the install is deterministic and idempotent. SSH,
// git, and file remotes carry no HTTP client — go-git builds their transports
// itself — so for those schemes the nil-client refusal above is the whole egress
// guarantee.
//
// auth, where it is not nil, is the clone's credential, and it replaces any user
// and password the URL carries; go-git drops it when the remote redirects the
// clone to another host.
func defaultClone(ctx context.Context, destDir, url string, httpClient *http.Client, auth transport.AuthMethod) (*git.Repository, error) {
	if httpClient == nil {
		return nil, egress.ErrOfflineEgress()
	}
	transportclient.InstallProtocol("https", githttp.NewClient(httpClient))
	transportclient.InstallProtocol("http", githttp.NewClient(httpClient))
	return git.PlainCloneContext(ctx, destDir, false, &git.CloneOptions{
		URL:        url,
		Auth:       auth,
		NoCheckout: true,
	})
}

// netrcAuth returns the credential a clone of the source sends — the one the
// netrc file spec.Netrc names holds for the URL's host — or nil for none. Only a
// remote reached over http or https is answered; every other remote reads no
// netrc file at all, so an SSH clone is untouched by one that does not parse. A
// file that exists and cannot be read or does not parse is the error.
//
// The nil it returns for no credential is an untyped one. A nil *BasicAuth held
// in the interface would read as present to go-git, which would then send an
// empty basic credential on every request.
func netrcAuth(spec sourceresolver.SourceSpec) (transport.AuthMethod, error) {
	if _, ok := netrc.HostFor(spec.URL); !ok {
		return nil, nil
	}
	credentials, err := spec.Netrc.Load()
	if err != nil {
		return nil, err
	}
	cred, ok := credentials.ForURL(spec.URL)
	if !ok {
		return nil, nil
	}
	return &githttp.BasicAuth{Username: cred.Login, Password: cred.Password}, nil
}

// ReadsFromThisHost reports whether a repository URL names a repository this
// adapter reads from the filesystem of the host it runs on, rather than one it
// reaches over the network. Two spellings do: a `file://` URL, and a path written
// with no scheme at all, which the clone reads as a local repository just the same.
//
// It asks the clone's own reading of the URL — the endpoint go-git selects the
// clone's transport by — rather than reading the URL's text a second way, so the
// answer is the transport the clone used and cannot come to disagree with it. A
// URL that reading refuses is one no clone reached, so a record carrying one
// cannot be placed; it reports true, the answer that tells a reader less about
// where things sit on this host.
func ReadsFromThisHost(url string) bool {
	endpoint, err := transport.NewEndpoint(url)
	if err != nil {
		return true
	}
	return endpoint.Protocol == "file"
}

// Kind returns the git-repository kind string.
func (a *adapter) Kind() string { return Kind }

// Resolve clones the repository at spec.URL into a temp directory,
// resolves spec.Ref to a commit SHA, checks out at that SHA,
// archives the working tree (or spec.ModuleSubdir if set), and
// populates the cache slot. The temp directory is removed before
// return regardless of outcome.
//
// PinnedVersion in the returned ResolvedSource is the resolved
// commit SHA, not the original ref. The original ref is preserved
// in OriginalSource for lockfile-side `requested_ref:` recording.
//
// spec.ModuleSubdir selects which directory of the repository is the artifact,
// so it is carried into the returned ResolvedSource and into the cache slot
// alongside the URL and the commit: two directories of one repository at one
// commit are two artifacts, and keying them on the URL and commit alone would
// resolve both to whichever was fetched first.
//
// ctx reaches the clone itself, which is the long operation here: go-git
// abandons an in-flight transfer when it ends, so a caller that stops
// waiting also stops the download. The temp directory is removed on the
// way out in that case as in every other, so a clone that ends part way
// leaves nothing behind it.
func (a *adapter) Resolve(ctx context.Context, spec sourceresolver.SourceSpec) (*sourceresolver.ResolvedSource, error) {
	// A module subdirectory climbing above the checkout would have the archive read
	// the files beside it, so it is refused before anything is fetched.
	if err := cacheslot.CheckModuleSubdir(Kind, spec.URL, spec.ModuleSubdir); err != nil {
		return nil, fmt.Errorf("git-repository: %w", err)
	}

	auth, err := netrcAuth(spec)
	if err != nil {
		return nil, err
	}

	cloneDir, err := os.MkdirTemp("", "aggrega-git-repo-*")
	if err != nil {
		return nil, fmt.Errorf("git-repository: tempdir: %w", err)
	}
	defer os.RemoveAll(cloneDir)

	repo, err := a.clone(ctx, cloneDir, spec.URL, spec.Client, auth)
	if err != nil {
		// Preserve the egress refusal as the specific offline sentinel; the
		// generic fetch-error mapping would flatten it into a network-unreachable
		// message and lose the errors.Is match.
		if errors.Is(err, egress.ErrOfflineEgress()) {
			return nil, err
		}
		return nil, mapGitError(spec, err)
	}

	ref := spec.Ref
	if ref == "" {
		ref = "HEAD"
	}
	hash, err := repo.ResolveRevision(plumbing.Revision(ref))
	if err != nil {
		return nil, sourceresolver.Wrap(spec, sourceresolver.ErrFetchNotFound(), err)
	}

	wt, err := repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("git-repository: worktree: %w", err)
	}
	if err := wt.Checkout(&git.CheckoutOptions{Hash: *hash}); err != nil {
		return nil, fmt.Errorf("git-repository: checkout %s: %w", hash.String(), err)
	}

	walkRoot := cloneDir
	if spec.ModuleSubdir != "" {
		walkRoot = filepath.Join(cloneDir, spec.ModuleSubdir)
	}
	commitSHA := hash.String()
	// The checkout is a root of its own: a link the repository carries is archived
	// as what it leads to only where that lies inside the checkout, and one leading
	// out of it halts the pull with nothing cached.
	entries, err := walkExcludingGit(cloneDir, checkoutName(spec.URL, spec.Ref, commitSHA), walkRoot)
	if err != nil {
		return nil, fmt.Errorf("git-repository: walk: %w", err)
	}

	archiveBytes, err := encodeTarGz(entries)
	if err != nil {
		return nil, fmt.Errorf("git-repository: archive: %w", err)
	}
	digest := archive.Digest(archiveBytes)
	slot, err := cacheslot.Slot(spec.WorkspaceRoot, spec.CacheDir, Kind, spec.URL, "", spec.ModuleSubdir, commitSHA, digest)
	if err != nil {
		return nil, fmt.Errorf("git-repository: %w", err)
	}
	// A git-cloned working tree has no OCI manifest; the archive is verified
	// against the recorded content digest alone.
	if err := cacheslot.WriteBlobAndMetadata(spec.WorkspaceRoot, spec.CacheDir, slot, archiveBytes,
		Kind, spec.URL, commitSHA, digest, MediatypeTarGz, "", nil); err != nil {
		return nil, fmt.Errorf("git-repository: %w", err)
	}

	return &sourceresolver.ResolvedSource{
		Kind:           Kind,
		URL:            spec.URL,
		ModuleSubdir:   spec.ModuleSubdir,
		PinnedVersion:  commitSHA,
		Digest:         digest,
		Mediatype:      MediatypeTarGz,
		CachePath:      slot + "/blob.tgz",
		OriginalSource: spec.OriginalSource,
	}, nil
}

// walkExcludingGit walks dir, beneath the checkout at cloneDir, via
// internal/archive.WalkDirectory, which holds every link it meets to the
// checkout, and filters out the .git/ subtree so the archive carries only the
// repository's working-tree content. checkout is how a refusal names the
// checkout ([checkoutName]).
func walkExcludingGit(cloneDir, checkout, dir string) ([]archive.Entry, error) {
	all, err := archive.WalkDirectory(cloneDir, checkout, dir)
	if err != nil {
		return nil, err
	}
	filtered := all[:0]
	for _, e := range all {
		if strings.HasPrefix(e.RelPath, ".git/") || e.RelPath == ".git/" {
			continue
		}
		filtered = append(filtered, e)
	}
	return filtered, nil
}

// checkoutName is how a refusal names the checkout of the repository at url,
// checked out at commit for ref, the revision the source names (empty where it
// names none). The checkout is a temporary directory removed once the pull halts,
// so a reader is pointed at the repository and the revision instead, where the
// link a refusal names is found and changed.
func checkoutName(url, ref, commit string) string {
	if ref == "" || ref == commit {
		return fmt.Sprintf("the repository %s at commit %s", url, commit)
	}
	return fmt.Sprintf("the repository %s at %s (commit %s)", url, ref, commit)
}

// encodeTarGz wraps internal/archive.WriteTarGz in a buffer so the
// adapter has a single-call helper for archive materialisation.
func encodeTarGz(entries []archive.Entry) ([]byte, error) {
	var buf tarBuf
	if err := archive.WriteTarGz(&buf, entries); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type tarBuf struct {
	b []byte
}

func (t *tarBuf) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	return len(p), nil
}

func (t *tarBuf) Bytes() []byte { return t.b }

func mapGitError(spec sourceresolver.SourceSpec, err error) error {
	if errors.Is(err, transport.ErrAuthenticationRequired) || errors.Is(err, transport.ErrAuthorizationFailed) {
		return sourceresolver.Wrap(spec, sourceresolver.ErrFetchUnauthorized(), err)
	}
	if errors.Is(err, transport.ErrRepositoryNotFound) {
		return sourceresolver.Wrap(spec, sourceresolver.ErrFetchNotFound(), err)
	}
	msg := err.Error()
	if strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "i/o timeout") {
		return sourceresolver.Wrap(spec, sourceresolver.ErrFetchNetworkUnreachable(), err)
	}
	return sourceresolver.Wrap(spec, sourceresolver.ErrFetchNetworkUnreachable(), err)
}

func init() {
	sourceresolver.Register(NewAdapter())
}
