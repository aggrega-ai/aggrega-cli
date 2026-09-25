// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package main

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// These tests hold `aggrega pull` to its promise that re-running it on an
// unchanged dependency set changes nothing. A re-pull reads every dependency the
// first pull fetched out of the content cache rather than the registry, so the
// promise holds only if a cache hit reports everything the registry read reported
// and the facts the first pull recorded about it survive. Each test drives the
// real pull command against a real OCI registry on loopback, compares the lockfile
// bytes a warm re-pull writes against the ones the cold pull wrote, and does so
// twice: with the registry up, and with it stopped.

// stoppableRegistry is an in-process OCI registry that can be stopped part way
// through a test while still recording what is sent to it. A stopped registry
// answers nothing: each request that reaches it is recorded and its connection
// dropped, which is what a client of a registry that has gone away observes. A
// closed listener would refuse the requests too, but would leave nothing to read,
// and the record is what shows whether a re-pull asked the registry about a
// dependency the content cache already holds.
type stoppableRegistry struct {
	host    string
	stopped atomic.Bool

	mu       sync.Mutex
	attempts []string
}

// attemptsUnder returns the paths of the requests a stopped registry received
// under one repository, and every path it received, for the failure message.
func (r *stoppableRegistry) attemptsUnder(repository string) (under, all []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	prefix := "/v2/" + repository + "/"
	for _, p := range r.attempts {
		if strings.HasPrefix(p, prefix) {
			under = append(under, p)
		}
	}
	return under, append([]string(nil), r.attempts...)
}

func startStoppableRegistry(t *testing.T) *stoppableRegistry {
	t.Helper()
	r := &stoppableRegistry{}
	r.host = startRegistry(t, func(up http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if !r.stopped.Load() {
				up.ServeHTTP(w, req)
				return
			}
			r.mu.Lock()
			r.attempts = append(r.attempts, req.URL.Path)
			r.mu.Unlock()
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, err := hj.Hijack(); err == nil {
					_ = conn.Close()
					return
				}
			}
			w.WriteHeader(http.StatusServiceUnavailable)
		})
	})
	return r
}

// pullOutcome is what one pull left behind: the lockfile bytes it wrote, the
// per-reference report it printed, and its diagnostic stream.
type pullOutcome struct {
	lock   []byte
	detail string
	diag   string
}

// pullWarmRepullWorkspace runs the production pull over the workspace with the
// per-reference report on, and reads back the lockfile it wrote.
func pullWarmRepullWorkspace(t *testing.T, ws, std, label string) pullOutcome {
	t.Helper()
	var out, diag bytes.Buffer
	if err := pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}),
		ReleasePath: "release.yaml",
		CatalogDirs: []string{std},
		Detail:      true,
		Out:         &out,
		DiagOut:     &diag,
	}); err != nil {
		t.Fatalf("%s: pull: %v\ndiagnostics:\n%s", label, err, diag.String())
	}
	lock, err := os.ReadFile(filepath.Join(ws, lockfile.PathFor("release.yaml")))
	if err != nil {
		t.Fatalf("%s: read the lockfile the pull wrote: %v", label, err)
	}
	return pullOutcome{lock: lock, detail: out.String(), diag: diag.String()}
}

// requireSameLockfile fails with both lockfiles when a re-pull wrote different
// bytes from the cold pull.
func requireSameLockfile(t *testing.T, label string, cold, warm pullOutcome) {
	t.Helper()
	if !bytes.Equal(cold.lock, warm.lock) {
		t.Fatalf("%s wrote a different lockfile from the cold pull; re-running pull on an unchanged dependency set "+
			"must change nothing.\n--- cold pull ---\n%s\n--- %s ---\n%s", label, cold.lock, label, warm.lock)
	}
}

// reportedSource returns the source the per-reference report names for one row,
// or "" when the report carries no row of that kind for that reference.
func reportedSource(detail, kind, uri string) string {
	for _, line := range strings.Split(detail, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[1] == kind && fields[2] == uri {
			return fields[0]
		}
	}
	return ""
}

// requireWarmRepullIsServedFromTheCache runs the pull once more with the registry
// stopped and holds it to three things: the lockfile the cold pull wrote, a .wasm
// reported as served from the cache, and no request about the plugin sent to the
// registry.
//
// The request check is scoped to the plugin's own repository, which is where every
// question about the cached .wasm and its entity is addressed — a signature lookup
// and a deprecation lookup both go to the manifest they are referrers of. It is not
// a claim that the pull reaches for no network at all, and it cannot honestly be
// one: a re-pull also asks the registry, at Component and Feature locations, about
// references that are neither, and those questions are unrelated to what the cache
// holds.
func requireWarmRepullIsServedFromTheCache(t *testing.T, reg *stoppableRegistry, ws, std, artifactURI, pluginName string, cold pullOutcome) {
	t.Helper()
	reg.stopped.Store(true)
	down := pullWarmRepullWorkspace(t, ws, std, "warm re-pull, registry stopped")
	repository := catalog.OCILocation("Plugin", pipelineStepPluginType, pluginName)
	if under, all := reg.attemptsUnder(repository); len(under) != 0 {
		t.Errorf("the warm re-pull asked the stopped registry about the plugin it holds in the content cache: %v\n"+
			"every request the stopped registry received: %v\ndiagnostics:\n%s", under, all, down.diag)
	}
	if got := reportedSource(down.detail, "Plugin", artifactURI); got != string(closure.SourceCacheReuse) {
		t.Errorf("the cached .wasm %s is reported as %q; want %q.\nreport:\n%s", artifactURI, got, closure.SourceCacheReuse, down.detail)
	}
	requireSameLockfile(t, "the warm re-pull with the registry stopped", cold, down)
}

// TestIntegration_WarmRePullWritesTheColdPullsLockfile publishes a pipeline
// plugin and the catalog that uses it to a registry that requires no signature,
// pulls once cold, and then re-pulls: with the registry up, through the automatic
// fill a build performs, and with the registry stopped. Every one of them must
// write the lockfile the cold pull wrote, byte for byte.
func TestIntegration_WarmRePullWritesTheColdPullsLockfile(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")
	std := standardCatalogDir(t)
	reg := startStoppableRegistry(t)

	const (
		pluginName = "patch-server"
		version    = "1.0.0"
	)
	pluginURI := types.PluginCoordinate(pipelineStepPluginType, pluginName) + ":" + version
	artifactURI := types.PluginCoordinate(pipelineStepPluginType, pluginName)
	publishPluginEntity(t, reg.host, pluginName, version, pluginPath)
	ws := writePipelinePluginConsumerWorkspace(t, reg.host, pluginURI, nil, nil)
	// The fixture catalog and plugin are published unsigned, so the workspace asks
	// for no signature: the signature pass still runs and finds nothing to require.
	writeWorkspaceConfig(t, ws, "default_registry: \""+reg.host+"\"\nauto_pull: true\noffline: false")
	t.Chdir(ws)

	cold := pullWarmRepullWorkspace(t, ws, std, "cold pull")
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor("release.yaml"))
	if err != nil {
		t.Fatalf("load the cold pull's lockfile: %v", err)
	}
	// The comparison below is only as strong as what the cold pull recorded, so the
	// digests a warm re-pull has been seen to drop are required here first.
	requireEntityManifestDigest(t, lf, pluginURI)
	requirePluginManifestDigest(t, lf, artifactURI)

	requireSameLockfile(t, "the warm re-pull with the registry up", cold,
		pullWarmRepullWorkspace(t, ws, std, "warm re-pull, registry up"))

	// A build's automatic fill runs the same resolution over the same cache, and it
	// is the first writer after a pull in the ordinary pull-then-build loop.
	if err := dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws}), "release.yaml", []string{std}, false, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("build with automatic fill: %v", err)
	}
	filled, err := os.ReadFile(lockfile.PathFor("release.yaml"))
	if err != nil {
		t.Fatalf("read the lockfile the build's fill wrote: %v", err)
	}
	requireSameLockfile(t, "the build's automatic fill", cold, pullOutcome{lock: filled})

	requireWarmRepullIsServedFromTheCache(t, reg, ws, std, artifactURI, pluginName, cold)
}

// TestIntegration_WarmRePullKeepsTheSignatureFact is the same drive against a
// registry that requires signatures, with the plugin and the catalog published
// signed under a keyed signer the workspace pins. The cold pull verifies every
// registry-resolved reference and records the verified fact on its row; a warm
// re-pull verifies nothing, because a cache hit does not touch the registry, so
// the facts survive only if they are carried forward from the lockfile the cold
// pull wrote. The registry-stopped re-pull is the sharp case: a signature that is
// required and cannot be looked up halts a pull, so a re-pull that still sent the
// cached .wasm to the signature pass would fail outright.
func TestIntegration_WarmRePullKeepsTheSignatureFact(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")
	std := standardCatalogDir(t)
	privPEM, pubPEM, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	reg := startStoppableRegistry(t)

	const (
		pluginName = "patch-server"
		version    = "1.0.0"
	)
	pluginURI := types.PluginCoordinate(pipelineStepPluginType, pluginName) + ":" + version
	artifactURI := types.PluginCoordinate(pipelineStepPluginType, pluginName)
	publishSignedPluginEntity(t, reg.host, pluginName, version, pluginPath, signer)
	ws := writePipelinePluginConsumerWorkspace(t, reg.host, pluginURI, pubPEM, signer)
	t.Chdir(ws)

	cold := pullWarmRepullWorkspace(t, ws, std, "cold pull")
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor("release.yaml"))
	if err != nil {
		t.Fatalf("load the cold pull's lockfile: %v", err)
	}
	requireEntityManifestDigest(t, lf, pluginURI)
	requirePluginManifestDigest(t, lf, artifactURI)
	// The rows a warm re-pull serves from the cache — the plugin entity and its
	// .wasm — carry the fact the cold pull verified; a comparison of lockfiles that
	// carried none would hold nothing to account.
	if !entityIsSigned(lf, pluginURI) {
		t.Fatalf("the cold pull recorded no verified signature on entity %s; lockfile:\n%s", pluginURI, cold.lock)
	}
	if !pluginIsSigned(lf, artifactURI) {
		t.Fatalf("the cold pull recorded no verified signature on plugin %s; lockfile:\n%s", artifactURI, cold.lock)
	}

	requireSameLockfile(t, "the warm re-pull with the registry up", cold,
		pullWarmRepullWorkspace(t, ws, std, "warm re-pull, registry up"))
	requireWarmRepullIsServedFromTheCache(t, reg, ws, std, artifactURI, pluginName, cold)
}

func requireEntityManifestDigest(t *testing.T, lf *lockfile.Lockfile, uri string) {
	t.Helper()
	for _, e := range lf.Entities {
		if e.URI == uri {
			if e.ManifestDigest == "" {
				t.Fatalf("the cold pull recorded no manifest digest on entity %s", uri)
			}
			return
		}
	}
	t.Fatalf("entity %s is absent from the cold pull's lockfile", uri)
}

func requirePluginManifestDigest(t *testing.T, lf *lockfile.Lockfile, uri string) {
	t.Helper()
	for _, p := range lf.Plugins {
		if p.URI == uri {
			if p.ManifestDigest == "" {
				t.Fatalf("the cold pull recorded no manifest digest on plugin %s", uri)
			}
			return
		}
	}
	t.Fatalf("plugin %s is absent from the cold pull's lockfile", uri)
}

func entityIsSigned(lf *lockfile.Lockfile, uri string) bool {
	for _, e := range lf.Entities {
		if e.URI == uri {
			return e.Signature != nil
		}
	}
	return false
}

func pluginIsSigned(lf *lockfile.Lockfile, uri string) bool {
	for _, p := range lf.Plugins {
		if p.URI == uri {
			return p.Signature != nil
		}
	}
	return false
}
