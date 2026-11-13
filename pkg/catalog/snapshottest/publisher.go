// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package snapshottest

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// publisherScript is where the catalog publisher's script sits in the catalog's
// repository, which this module's checkout keeps beside it.
var publisherScript = filepath.Join("aggrega-hub", "scripts", "publish-catalog-snapshot.sh")

// PublisherScript returns the path of the script the catalog's own publication
// runs to build, push and sign the catalog snapshot, in the catalog's repository
// checked out beside this module — found by walking up from the directory the test
// runs in. Where no directory above holds it the test fails naming it: the case
// exists to run that script, and a case that skipped it would certify nothing.
func PublisherScript(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("read the test's working directory: %v", err)
	}
	for {
		candidate := filepath.Join(dir, publisherScript)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no %s above the test's working directory: the catalog's repository is checked out beside "+
				"this module, and this case runs its snapshot publisher", publisherScript)
		}
		dir = parent
	}
}

// PublisherMarker is the publish marker a repository PublisherRepository lays is
// tagged with: the tag a real run of the publisher requires to name the commit it
// publishes.
const PublisherMarker = "catalog-published"

// PublisherRepository lays the tree at tree into a fresh git repository at the
// path the publisher reads a catalog from, aggrega-catalog/, commits it, tags the
// commit with PublisherMarker, and returns the repository. The commit is the
// tree's alone, so the publisher's archive of it is the archive of the tree.
func PublisherRepository(t testing.TB, tree string) string {
	t.Helper()
	repo := t.TempDir()
	dest := filepath.Join(repo, "aggrega-catalog")
	err := filepath.WalkDir(tree, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(tree, p)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, body, 0o644)
	})
	if err != nil {
		t.Fatalf("lay %s into a repository: %v", tree, err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "-A"},
		{"-c", "user.name=snapshot", "-c", "user.email=snapshot@example.invalid", "-c", "commit.gpgsign=false",
			"commit", "-q", "-m", "catalog"},
		{"-c", "tag.gpgsign=false", "tag", PublisherMarker},
	} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = publisherEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), repo, err, out)
		}
	}
	return repo
}

// RunPublisher runs the publisher's script with args and returns what it wrote to
// standard output, failing the test where it exits non-zero. It runs with no
// registry credential in its environment, so a run against a registry asking for
// none sends none.
func RunPublisher(t testing.TB, args ...string) string {
	t.Helper()
	cmd := exec.Command(PublisherScript(t), args...)
	cmd.Env = publisherEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("the snapshot publisher %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err,
			stdout.String(), stderr.String())
	}
	return stdout.String()
}

// publisherEnv is the environment the publisher and the git commands laying its
// repository run under: the test process's own, without a registry credential or
// an Aggrega workspace named, and with no git configuration beyond the system's,
// so neither a developer's credential nor their git settings reach the run.
func publisherEnv() []string {
	var env []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		switch name {
		case "REGISTRY_USERNAME", "REGISTRY_PASSWORD", "AGGREGA_REGISTRY_PASSWORD", "AGGREGA_WORKSPACE_ROOT",
			"GIT_CONFIG_GLOBAL", "GIT_DIR", "GIT_WORK_TREE":
			continue
		}
		env = append(env, entry)
	}
	return append(env, "GIT_CONFIG_GLOBAL="+os.DevNull)
}

// publisherDigest matches the manifest digest the publisher's first line names.
var publisherDigest = regexp.MustCompile(`\((sha256:[0-9a-f]{64})\)`)

// publisherFacts matches the line the publisher names its repository and media
// type on.
var publisherFacts = regexp.MustCompile(`(?m)^  repository (\S+), media type (\S+)$`)

// PublisherOutput is what the publisher's output says it published, or under
// --dry-run would publish.
type PublisherOutput struct {
	// Digest is the manifest digest.
	Digest string
	// Repository is the repository beneath the registry it publishes in.
	Repository string
	// MediaType is the artifact's and its layer's media type.
	MediaType string
}

// ReadPublisherOutput reads the publisher's output, failing the test where it
// does not name all three.
func ReadPublisherOutput(t testing.TB, out string) PublisherOutput {
	t.Helper()
	dg := publisherDigest.FindStringSubmatch(out)
	facts := publisherFacts.FindStringSubmatch(out)
	if dg == nil || facts == nil {
		t.Fatalf("the publisher's output does not name the digest, repository and media type:\n%s", out)
	}
	return PublisherOutput{Digest: dg[1], Repository: facts[1], MediaType: facts[2]}
}

// PublishThroughScript publishes the catalog snapshot of the tree at tree to the
// registry through the catalog publisher's own script — a real run, against this
// registry, with no credential and no signature — and returns what the script says
// it published. It is tagged with the commit and latest, as the catalog's
// publication tags it; a case signs it with Sign.
func (r *Registry) PublishThroughScript(t testing.TB, tree string) PublisherOutput {
	t.Helper()
	repo := PublisherRepository(t, tree)
	out := RunPublisher(t, "--registry", r.Host, "--repository", repo)
	return ReadPublisherOutput(t, out)
}
