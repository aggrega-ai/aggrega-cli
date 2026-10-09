// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/artifactout"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// registryReach stands in for a workspace's default registry and counts every
// connection made to it. It counts at the connection rather than at the request,
// because the offline posture is a promise about dialling: a client that dialled
// and then gave up before sending anything would be missed by a request count.
type registryReach struct {
	server *httptest.Server
	dials  atomic.Int64
}

func newRegistryReach(t *testing.T) *registryReach {
	t.Helper()
	r := &registryReach{}
	r.server = httptest.NewUnstartedServer(http.NotFoundHandler())
	r.server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			r.dials.Add(1)
		}
	}
	r.server.Start()
	t.Cleanup(r.server.Close)
	return r
}

// host is the registry host a workspace configuration names.
func (r *registryReach) host() string { return r.server.Listener.Addr().String() }

// offlineFillWorkspace lays down the local-catalog fixture in a fresh root, moves
// the working directory into it, and writes a workspace configuration that lists
// the fixture's catalog, names reach as the default registry, asks for no fill,
// and sets the offline posture where offline is true. It returns the root and the
// Release.
func offlineFillWorkspace(t *testing.T, reach *registryReach, offline bool) (root, release string) {
	t.Helper()
	root = t.TempDir()
	release = authoringFixture(t, root)
	posture := "false"
	if offline {
		posture = "true"
	}
	writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename),
		"workspace:\n  catalog_dirs:\n    - catalog\ndefault_registry: \""+reach.host()+"\"\noffline: "+posture+"\nauto_pull: false\n")
	t.Chdir(root)
	return root, release
}

// referenceNothingHolds rewrites the fixture's one component to name a Component
// that nothing on this machine holds — not its catalog, not its cache — and maps
// that type to a Recipe the catalog does hold: a copy of the fixture's own Recipe,
// renamed and declaring that type as the one it implements. It returns the
// reference.
//
// The mapping is made consistent so that the reference stays the input's only
// fault. The mapping table is judged ahead of the dependency resolution, over what
// this machine holds, and a mapping onto a Recipe that is held but implements
// another type halts every command there — before the resolution runs, and before
// any registry could be asked. A Recipe nothing held would pass that judgement,
// but would put a second unresolvable reference in front of the resolution.
//
// The reference shares no name with any entity the fixture does hold: the walk
// keys what it resolves by reference alone, so a name a local Recipe also carries
// would be answered by that Recipe rather than reported as held by nothing. The
// Recipe's name sorts ahead of the reference, and the resolution takes references
// in that order, so a Recipe the catalog failed to answer would be the reference
// a refusal names, which the assertion on the named reference catches.
func referenceNothingHolds(t *testing.T, root string) string {
	t.Helper()
	const (
		ref    = "example/datastore/absent:1.0.0"
		recipe = "bound-recipe"
	)
	// rewrite writes the file at from to the file at to with each swap applied
	// once, failing where the fixture no longer carries the text a swap replaces.
	rewrite := func(from, to string, swaps ...[2]string) {
		t.Helper()
		body, err := os.ReadFile(from)
		if err != nil {
			t.Fatalf("read %s: %v", filepath.Base(from), err)
		}
		text := string(body)
		for _, swap := range swaps {
			if !strings.Contains(text, swap[0]) {
				t.Fatalf("%s no longer carries %q, so the reference cannot be swapped in", filepath.Base(from), swap[0])
			}
			text = strings.Replace(text, swap[0], swap[1], 1)
		}
		writeFixture(t, to, text)
	}
	held := filepath.Join(root, "catalog", "recipes", "server.yaml")
	rewrite(held, filepath.Join(filepath.Dir(held), recipe+".yaml"),
		[2]string{"  name: server\n", "  name: " + recipe + "\n"},
		[2]string{"  implements: server\n", "  implements: " + ref + "\n"})
	aggregate := filepath.Join(root, "aggregate.yaml")
	rewrite(aggregate, aggregate, [2]string{"      type: server\n", "      type: " + ref + "\n"})
	manifest := filepath.Join(root, "manifest.yaml")
	rewrite(manifest, manifest, [2]string{"        server: server\n", "        " + ref + ": " + recipe + "\n"})
	return ref
}

// readLockfile returns the bytes of the lockfile beside release, failing the test
// where there is none.
func readLockfile(t *testing.T, release string) string {
	t.Helper()
	b, err := os.ReadFile(lockfile.PathFor(release))
	if err != nil {
		t.Fatalf("no lockfile beside %s: %v", filepath.Base(release), err)
	}
	return string(b)
}

// assertNoLockfile fails the test where a lockfile exists beside release.
func assertNoLockfile(t *testing.T, release, after string) {
	t.Helper()
	if _, err := os.Stat(lockfile.PathFor(release)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s left a lockfile beside %s (stat: %v); a refused run writes none", after, filepath.Base(release), err)
	}
}

// assertNothingHolds pins that err is the refusal the dependency resolution raises
// for a reference it cannot resolve from anything the machine holds, naming ref.
// It is matched through the condition's accessor and the carried reference, never
// through the wording, and it is told apart from a fetch that found nothing, which
// is what a refusal taken before the resolution ran would have carried instead.
func assertNothingHolds(t *testing.T, command string, err error, ref string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s succeeded over a reference nothing on this machine holds", command)
	}
	if !errors.Is(err, closure.ErrUnresolvableURI()) {
		t.Fatalf("%s refused, but not as the dependency resolution refuses a reference it cannot resolve: %v", command, err)
	}
	var unresolved *closure.UnresolvedEntityError
	if !errors.As(err, &unresolved) {
		t.Fatalf("%s's refusal carries no reference: %v", command, err)
	}
	if unresolved.URI != ref {
		t.Errorf("%s's refusal names %q; want the reference nothing holds, %q", command, unresolved.URI, ref)
	}
	if errors.Is(err, oci.ErrFetchNotFound()) {
		t.Errorf("%s's refusal also carries a fetch that found nothing, so something other than the resolution answered: %v", command, err)
	}
}

// TestAnOfflineBuildAskedToFillDoesWhatAnOfflinePullDoes is the invocation proof
// that a build asked to fill its lockfile reads one decision about whether the
// fill runs — the one the daemon's build and the tool protocol's build read — and
// that the offline posture is not a second one. Under the posture the fill is the
// same pull `aggrega pull` runs, so the build resolves what the machine already
// holds, refuses with the pull's own words what it does not, and dials nothing.
//
// Every command runs through the real command tree, in a workspace whose default
// registry is a listener counting dials. The rows that resolve offline assert it
// was never dialled; the last row asks the same input with the posture lifted and
// asserts it WAS, so a zero above is the posture's doing rather than a fixture that
// never needed a registry.
func TestAnOfflineBuildAskedToFillDoesWhatAnOfflinePullDoes(t *testing.T) {
	// Not parallel: every command runs from its workspace, which moves the process
	// working directory, and the process environment is pinned below.
	for _, name := range []string{"AGGREGA_OFFLINE", "AGGREGA_AUTO_PULL", "AGGREGA_DEFAULT_REGISTRY", "AGGREGA_CATALOG_DIRS"} {
		t.Setenv(name, "")
	}

	// The two ways the posture is set, so neither is the one the build reads.
	for _, posture := range []struct {
		name   string
		config bool
		flag   []string
	}{
		{name: "set by the configuration file", config: true},
		{name: "set by the flag", flag: []string{"--offline"}},
	} {
		t.Run("a build asked to fill over a local catalog fills the lockfile, which a verify and a second pull accept as written, with the posture "+posture.name, func(t *testing.T) {
			reach := newRegistryReach(t)
			root, release := offlineFillWorkspace(t, reach, posture.config)
			run := func(args ...string) (string, error) { return runVerb(t, root, append(args, posture.flag...)...) }

			if stderr, err := run("build", "--release", release, "--auto-pull"); err != nil {
				t.Fatalf("the build refused to fill offline over a catalog that holds everything: %v\nstderr:\n%s", err, stderr)
			}
			written := readLockfile(t, release)
			renderedTree(t, filepath.Join(root, artifactout.DefaultOutputDir))

			if stderr, err := run("pull", "--verify", "--release", release); err != nil {
				t.Errorf("a verify refused the lockfile the build wrote: %v\nstderr:\n%s", err, stderr)
			}
			if stderr, err := run("pull", "--release", release); err != nil {
				t.Fatalf("a pull refused over the lockfile the build wrote: %v\nstderr:\n%s", err, stderr)
			}
			if again := readLockfile(t, release); again != written {
				t.Errorf("a pull rewrote the lockfile the build had just written, so the two resolved differently\nbuild:\n%s\npull:\n%s", written, again)
			}
			if n := reach.dials.Load(); n != 0 {
				t.Errorf("the default registry was dialled %d time(s) under the offline posture", n)
			}
		})
	}

	t.Run("a build asked to fill a reference nothing on this machine holds halts with the pull's own refusal naming it, and writes no lockfile", func(t *testing.T) {
		reach := newRegistryReach(t)
		root, release := offlineFillWorkspace(t, reach, true)
		ref := referenceNothingHolds(t, root)

		_, pullErr := runVerb(t, root, "pull", "--release", release)
		assertNothingHolds(t, "the pull", pullErr, ref)
		assertNoLockfile(t, release, "the pull")

		_, buildErr := runVerb(t, root, "build", "--release", release, "--auto-pull")
		assertNothingHolds(t, "the build", buildErr, ref)
		assertNoLockfile(t, release, "the build")

		if n := reach.dials.Load(); n != 0 {
			t.Errorf("the default registry was dialled %d time(s) under the offline posture", n)
		}
	})

	t.Run("a build asked for no fill, with no lockfile, halts naming the pull that would fill it, and writes nothing", func(t *testing.T) {
		reach := newRegistryReach(t)
		root, release := offlineFillWorkspace(t, reach, true)

		_, err := runVerb(t, root, "build", "--release", release)
		if err == nil {
			t.Fatal("a build with no lockfile and no fill asked for succeeded; there was nothing pinned to render against")
		}
		for _, want := range []string{
			"missing; run `aggrega pull --release ",
			filepath.Base(release) + "` first to create it, or pass --auto-pull (or enable auto_pull) to fill it during the build",
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the halt does not carry %q: %v", want, err)
			}
		}
		if errors.Is(err, oci.ErrFetchNotFound()) {
			t.Errorf("the halt carries a fetch that found nothing, but no fill ran and nothing was fetched: %v", err)
		}
		assertNoLockfile(t, release, "the build")
		if n := reach.dials.Load(); n != 0 {
			t.Errorf("the default registry was dialled %d time(s) under the offline posture", n)
		}
	})

	t.Run("a build asked to fill over a lockfile its Release no longer matches refills it from what the machine holds", func(t *testing.T) {
		reach := newRegistryReach(t)
		root, release := offlineFillWorkspace(t, reach, true)
		if stderr, err := runVerb(t, root, "pull", "--release", release); err != nil {
			t.Fatalf("seed pull: %v\nstderr:\n%s", err, stderr)
		}
		seeded := readLockfile(t, release)
		body, err := os.ReadFile(release)
		if err != nil {
			t.Fatalf("read the Release: %v", err)
		}
		writeFixture(t, release, string(body)+"# edited after the pull\n")

		// The control: asked for no fill, the build holds the edited Release to the
		// lockfile as it stands and refuses it as out of date.
		if _, err := runVerb(t, root, "build", "--release", release); !errors.Is(err, lockfile.ErrLockfileDrift()) {
			t.Fatalf("a build asked for no fill did not refuse the out-of-date lockfile as drift: %v", err)
		}

		if stderr, err := runVerb(t, root, "build", "--release", release, "--auto-pull"); err != nil {
			t.Fatalf("the build refused to refill the lockfile offline over a catalog that holds everything: %v\nstderr:\n%s", err, stderr)
		}
		if readLockfile(t, release) == seeded {
			t.Error("the build reported success and left the out-of-date lockfile as it was")
		}
		if stderr, err := runVerb(t, root, "pull", "--verify", "--release", release); err != nil {
			t.Errorf("a verify refused the lockfile the build refilled: %v\nstderr:\n%s", err, stderr)
		}
		if n := reach.dials.Load(); n != 0 {
			t.Errorf("the default registry was dialled %d time(s) under the offline posture", n)
		}
	})

	t.Run("a build asked to refresh every dependency is refused by the pull it runs, naming the posture, and leaves the lockfile as it was", func(t *testing.T) {
		reach := newRegistryReach(t)
		root, release := offlineFillWorkspace(t, reach, true)
		if stderr, err := runVerb(t, root, "pull", "--release", release); err != nil {
			t.Fatalf("seed pull: %v\nstderr:\n%s", err, stderr)
		}
		seeded := readLockfile(t, release)

		// --yes answers the refresh's confirmation in advance, which sits ahead of
		// everything else by design; without it this row would stop there.
		_, err := runVerb(t, root, "build", "--release", release, "--upgrade", "--yes")
		if err == nil {
			t.Fatal("--upgrade succeeded under the offline posture; a full refresh cannot learn what a registry currently offers")
		}
		assertOfflineRefusal(t, err, workspaceconfig.ConfigFilename)
		if !strings.Contains(err.Error(), "highest version") {
			t.Errorf("the refusal does not say what about --upgrade cannot be done offline: %v", err)
		}
		if readLockfile(t, release) != seeded {
			t.Error("a refused refresh rewrote the lockfile")
		}
		if n := reach.dials.Load(); n != 0 {
			t.Errorf("the default registry was dialled %d time(s) under the offline posture", n)
		}
	})

	t.Run("the same reference with the posture lifted dials the default registry, so a zero above is the posture's doing", func(t *testing.T) {
		reach := newRegistryReach(t)
		root, release := offlineFillWorkspace(t, reach, false)
		referenceNothingHolds(t, root)

		// The pull is expected to fail, since nothing serves the reference; what is
		// asserted is only that, allowed the network, it went looking.
		_, _ = runVerb(t, root, "pull", "--release", release)
		if reach.dials.Load() == 0 {
			t.Fatal("a pull allowed the network never dialled the default registry for a reference nothing local holds, so the zero the offline rows assert measures nothing")
		}
	})
}
