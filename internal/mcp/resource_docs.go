// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"errors"
	"strings"

	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/internal/docsbundle"
	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

const (
	// schemeLangref addresses a language-reference page inside the
	// documentation bundle; schemeAuthoring addresses an authoring-guide topic.
	schemeLangref   = "langref"
	schemeAuthoring = "authoring"

	// langrefPrefix and authoringPrefix are the bundle-internal directories the
	// two schemes read from. A langref:// URI names a page beneath the
	// language-reference tree; an authoring:// URI names a topic beneath the
	// authoring tree.
	langrefPrefix   = "language-reference/"
	authoringPrefix = "authoring/"
)

// docsResourceTemplates returns the templated resources the server serves: the
// language-reference and authoring-guide families, each a URI template that
// expands into a family of readable pages. They are templates rather than
// concrete resources/list entries because their content is parameterised by
// the page path or topic name.
func docsResourceTemplates() []resourceTemplate {
	return []resourceTemplate{
		{
			URITemplate: "langref://{path}",
			Name:        "language-reference",
			Description: "A language-reference page, served inline from the local documentation bundle. The path names a page within the reference (for example langref://entities/component).",
			MimeType:    "text/markdown",
		},
		{
			URITemplate: "authoring://{topic}",
			Name:        "authoring-guide",
			Description: "An authoring-guide topic, served inline from the local documentation bundle. The topic names a guide (for example authoring://getting-started).",
			MimeType:    "text/markdown",
		},
	}
}

// docsTargetFor resolves the OCI target the documentation bundle is pulled from
// and the pull-time signature check to run against it, for a session that has
// already resolved its workspace.
//
// It is deliberately the narrowest seam that removes the registry: everything
// after it — the cache-presence probe, the pull, the media-type check, the
// verification, the cache write and the read — is the production path, so a test
// substituting a target still exercises all of it. A seam one level higher, over
// the whole acquisition, would take the cache write out of the tested path and
// leave the trigger proven against nothing.
//
// It carries the resolved workspace rather than the configuration alone, which
// is what a test on this surface substitutes against; the acquisition itself
// takes the narrower seam, and the two are bridged where ensureDocsBundle drives
// it.
//
// Nil selects the shared resolution: the target the run's context states, or
// else the registry through the workspace's egress gate.
type docsTargetFor func(tw *toolWorkspace, ref, version string, warn func(string)) (oras.ReadOnlyTarget, docscache.VerifyAtPull, error)

// errNoDocsRoot is what a session answers the documentation with when it was
// built with no cache root at all, which only a server assembled outside Run and
// NewHTTPHandler can be: both settle one, or the reason none resolves.
var errNoDocsRoot = errors.New("no cache root was settled for this session's documentation bundle")

// docsCacheRoot returns the cache root this session keeps the documentation
// bundle under, or why there is none. It is the one place the session's
// acquisition and every read of the bundle take the root from, so the copy the
// acquisition fills is the copy the reads read.
func (s *server) docsCacheRoot() (string, error) {
	switch {
	case s.docsRoot != "":
		return s.docsRoot, nil
	case s.docsRootErr != nil:
		return "", s.docsRootErr
	default:
		return "", errNoDocsRoot
	}
}

// ensureDocsBundle runs the acquisition of the documentation bundle: once per
// serving process, before the first message is answered, and never on a resource
// read.
//
// Siting it ahead of the message loop rather than on the read is the whole of
// the offline contract. A read takes no target and cannot reach a registry;
// folding a fetch into the read would put a registry round-trip behind every
// documentation request and make a served page's availability depend on
// connectivity at the moment it is asked for. Acquiring once, ahead of any read,
// keeps the read off the registry and the answer warm.
//
// The bundle is kept in the machine-wide cache, so a copy another surface or
// another session already acquired is used as it is when this workspace's own
// signature check admits it, and nothing is downloaded. One the check refuses is
// fetched again under that check. Which version is acquired is the choice every
// documentation surface shares (docsbundle.EnsureCachedAtOrBelow): the one asked
// for where it is published, otherwise the newest earlier one, which the
// session's reads then read (docsBundleCoords).
//
// It never fails the run. An offline workspace, a registry that does not serve
// the bundle, a signature that does not verify, and a machine with no cache root
// each leave the cache exactly as it was; the reason goes to the log stream and
// the documentation resources answer resource-not-found, which is a poorer answer
// than a page and a far better one than a refused session.
//
// ctx is the run's own, carried into the acquisition — a registry round trip —
// so ending the run ends the fetch instead of leaving the server waiting on a
// registry nobody is still interested in.
func (s *server) ensureDocsBundle(ctx context.Context) {
	s.docsBundleOnce.Do(func() {
		tw, err := s.resolveToolWorkspace("", nil)
		if err != nil {
			s.logLifecycle(
				lifecycleAudit{
					Event:   eventDocsBundle,
					Outcome: bundleNotAcquired,
					Detail:  err.Error(),
				},
				"mcp: documentation bundle not acquired: %v", err)
			return
		}
		ref, version := s.docsBundleCoords(tw)
		cacheRoot, err := s.docsCacheRoot()
		if err != nil {
			s.logLifecycle(
				lifecycleAudit{
					Event:     eventDocsBundle,
					Reference: ref + "@" + version,
					Outcome:   bundleNotAcquired,
					Detail:    err.Error(),
				},
				"mcp: documentation bundle %s@%s not acquired: %v", ref, version, err)
			return
		}
		warn := func(notice string) {
			s.logLifecycle(
				lifecycleAudit{
					Event:     eventDocsBundle,
					Reference: ref + "@" + version,
					Outcome:   bundleNotice,
					Detail:    notice,
				},
				"mcp: %s", notice)
		}
		var target docsbundle.TargetFor
		if s.docsTarget != nil {
			target = func(_ workspaceconfig.Config, ref, version string, warn func(string)) (oras.ReadOnlyTarget, docscache.VerifyAtPull, error) {
				return s.docsTarget(tw, ref, version, warn)
			}
		}
		held, acquired, err := docsbundle.EnsureCachedAtOrBelow(ctx, tw.cfg, cacheRoot, ref, version, target, warn)
		if err != nil {
			s.docsAcquireErr = err
			s.logLifecycle(
				lifecycleAudit{
					Event:     eventDocsBundle,
					Reference: ref + "@" + version,
					Outcome:   bundleNotAcquired,
					Detail:    err.Error(),
				},
				"mcp: documentation bundle %s@%s not acquired: %v", ref, version, err)
			return
		}
		s.docsHeldVersion = held
		if acquired {
			s.logLifecycle(
				lifecycleAudit{
					Event:     eventDocsBundle,
					Reference: ref + "@" + held,
					Outcome:   bundleAcquired,
				},
				"mcp: documentation bundle %s@%s acquired", ref, held)
		}
	})
}

// readDocsResource serves a documentation resource URI as inline content read
// from the machine-wide documentation-bundle cache. It maps the URI scheme to the
// bundle-internal page path, resolves the workspace and the bundle coordinates,
// and reads the page without reaching a registry, held to the workspace's own
// signature check — a cache miss, a copy that check refuses, an absent page and a
// machine with no cache root are each reported as a well-formed
// resource-not-found error, an integrity or read failure as an internal error.
//
// The check runs silently here: what it has to report about the copy was said on
// the log stream when the session acquired it. It runs under ctx, the request's
// own, with what the run stated about the bundle stated on it again.
func (s *server) readDocsResource(ctx context.Context, uri string) (readResourceResult, *rpcError) {
	page, ok := bundlePageForURI(uri)
	if !ok {
		return readResourceResult{}, resourceNotFound(uri)
	}
	tw, err := s.resolveToolWorkspace("", nil)
	if err != nil {
		return readResourceResult{}, &rpcError{Code: codeInternalError, Message: err.Error()}
	}
	ref, version := s.docsBundleCoords(tw)
	cacheRoot, err := s.docsCacheRoot()
	if err != nil {
		return readResourceResult{}, resourceNotFound(uri)
	}
	data, mimeType, rerr := docscache.ReadPage(cacheRoot, ref, version, page,
		docsbundle.Admission(s.docsDecl.Onto(ctx), tw.cfg, ref, nil))
	if rerr != nil {
		if errors.Is(rerr, docscache.ErrFetchNotFound()) || errors.Is(rerr, docscache.ErrPageNotFound()) {
			return readResourceResult{}, resourceNotFound(uri)
		}
		return readResourceResult{}, &rpcError{Code: codeInternalError, Message: "read documentation resource: " + rerr.Error()}
	}
	return readResourceResult{
		Contents: []resourceContents{{URI: uri, MimeType: mimeType, Text: string(data)}},
	}, nil
}

// bundlePageForURI maps a documentation resource URI to its bundle-internal
// page path. It reports ok=false for an unrecognised scheme, an empty body, or
// a body that climbs out of the tree its scheme names, so those surface as a
// clean resource-not-found rather than a malformed lookup. A body with no file
// extension gains a ".md" suffix, so both langref://entities/component and
// langref://entities/component.md resolve to the same markdown page.
//
// Containment is pathscope's question, asked in slash arithmetic about the body
// rather than about the prefixed page path. A scheme addresses one tree of the
// bundle, and a body confined to its own tree is confined to the bundle as well,
// while the reverse does not hold: "../authoring/getting-started" under the
// language-reference prefix cleans to a page inside the bundle but outside the
// tree its scheme names. So a body carrying two dots is an ordinary page name
// and resolves — "entities/foo..bar", "..config", and an interior "a/../b" that
// resolves to "b" — and only a cleaned form that climbs above the tree is
// refused.
func bundlePageForURI(uri string) (string, bool) {
	scheme, body, ok := splitScheme(uri)
	if !ok {
		return "", false
	}
	body = strings.Trim(body, "/")
	// An empty body names no page at all, which is a separate refusal from
	// containment and keeps its own reason.
	if body == "" {
		return "", false
	}
	if !pathscope.IsLocalSlash(body) {
		return "", false
	}
	var prefix string
	switch scheme {
	case schemeLangref:
		prefix = langrefPrefix
	case schemeAuthoring:
		prefix = authoringPrefix
	default:
		return "", false
	}
	return prefix + ensureMarkdownExt(body), true
}

// splitScheme splits a "scheme://body" URI into its scheme and body. It reports
// ok=false when the "://" separator is absent, so a bare path or an opaque URI
// is treated as unrecognised.
func splitScheme(uri string) (scheme, body string, ok bool) {
	i := strings.Index(uri, "://")
	if i <= 0 {
		return "", "", false
	}
	return uri[:i], uri[i+len("://"):], true
}

// ensureMarkdownExt appends ".md" to a page body whose final segment carries no
// extension, so a bare topic or reference name resolves to its markdown page.
func ensureMarkdownExt(body string) string {
	last := body
	if i := strings.LastIndex(body, "/"); i >= 0 {
		last = body[i+1:]
	}
	if strings.Contains(last, ".") {
		return body
	}
	return body + ".md"
}

// docsBundleCoords returns the OCI reference and version of the documentation
// bundle the server reads pages from. Explicit server overrides win (a test
// pins a fixture bundle); otherwise the reference defaults to the workspace's
// registry under the reserved docs namespace. The version is the one the
// session's acquisition holds — an earlier version's where none is published for
// the version asked for — and, before that acquisition has held one, the version
// asked for (docsRequestedVersion).
func (s *server) docsBundleCoords(tw *toolWorkspace) (ref, version string) {
	ref = s.docsBundleRef
	if ref == "" {
		ref = docsbundle.Ref(tw.cfg.DefaultRegistryLocation().Prefix())
	}
	if s.docsHeldVersion != "" {
		return ref, s.docsHeldVersion
	}
	return ref, s.docsRequestedVersion()
}

// docsRequestedVersion returns the version of the documentation bundle this
// session asks for: the pinned one where a test pins it, otherwise the server's
// own build identity.
func (s *server) docsRequestedVersion() string {
	if s.docsBundleVersion != "" {
		return s.docsBundleVersion
	}
	return s.serverVersion
}
