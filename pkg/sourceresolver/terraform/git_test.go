// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package terraform

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/metadata"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func TestTerraformGitResolveHappyPath(t *testing.T) {
	t.Parallel()
	fixtureDir, _ := buildFixtureRepo(t, map[string]string{
		"main.tf":      `resource "null_resource" "x" {}`,
		"variables.tf": `variable "name" { type = string }`,
	})

	a := &adapter{clone: cloneFromFixture(fixtureDir)}
	root := t.TempDir()
	got, err := a.Resolve(sourceresolver.SourceSpec{
		Kind:          Kind,
		URL:           "file://" + fixtureDir,
		Ref:           "HEAD",
		WorkspaceRoot: root,
		CacheDir:      ".aggrega/cache",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got.PinnedVersion) != 40 {
		t.Errorf("PinnedVersion should be a 40-char SHA; got %q", got.PinnedVersion)
	}
	if _, err := os.Stat(got.CachePath); err != nil {
		t.Errorf("cache blob missing: %v", err)
	}
	m, err := metadata.Read(filepath.Join(filepath.Dir(got.CachePath), "metadata.yaml"))
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if m.Version != got.PinnedVersion {
		t.Errorf("metadata Version = %q; want %q", m.Version, got.PinnedVersion)
	}
	if m.Mediatype != MediatypeTarGz {
		t.Errorf("metadata Mediatype = %q; want %s", m.Mediatype, MediatypeTarGz)
	}
}

func TestTerraformGitResolveTagToSHA(t *testing.T) {
	t.Parallel()
	fixtureDir, headSHA := buildFixtureRepoWithTag(t, "v1.0.0", map[string]string{
		"main.tf": `resource "null_resource" "x" {}`,
	})

	a := &adapter{clone: cloneFromFixture(fixtureDir)}
	got, err := a.Resolve(sourceresolver.SourceSpec{
		Kind:          Kind,
		URL:           "file://" + fixtureDir,
		Ref:           "v1.0.0",
		WorkspaceRoot: t.TempDir(),
		CacheDir:      ".aggrega/cache",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.PinnedVersion != headSHA {
		t.Errorf("PinnedVersion = %s; want tagged SHA %s", got.PinnedVersion, headSHA)
	}
}

func TestTerraformGitResolveMissingRefWrapsNotFound(t *testing.T) {
	t.Parallel()
	fixtureDir, _ := buildFixtureRepo(t, map[string]string{
		"main.tf": `resource "null_resource" "x" {}`,
	})

	a := &adapter{clone: cloneFromFixture(fixtureDir)}
	_, err := a.Resolve(sourceresolver.SourceSpec{
		Kind:          Kind,
		URL:           "file://" + fixtureDir,
		Ref:           "nonexistent-branch",
		WorkspaceRoot: t.TempDir(),
		CacheDir:      ".aggrega/cache",
	})
	if !errors.Is(err, sourceresolver.ErrFetchNotFound) {
		t.Errorf("error should wrap ErrFetchNotFound; got %v", err)
	}
}

func TestTerraformGitResolveDeterministicArchive(t *testing.T) {
	t.Parallel()
	fixtureDir, _ := buildFixtureRepo(t, map[string]string{
		"main.tf":      `resource "null_resource" "x" {}`,
		"variables.tf": `variable "y" { type = number }`,
	})

	a := &adapter{clone: cloneFromFixture(fixtureDir)}
	spec := sourceresolver.SourceSpec{
		Kind:          Kind,
		URL:           "file://" + fixtureDir,
		Ref:           "HEAD",
		WorkspaceRoot: t.TempDir(),
		CacheDir:      ".aggrega/cache",
	}
	one, err := a.Resolve(spec)
	if err != nil {
		t.Fatalf("Resolve 1: %v", err)
	}
	spec.WorkspaceRoot = t.TempDir()
	two, err := a.Resolve(spec)
	if err != nil {
		t.Fatalf("Resolve 2: %v", err)
	}
	if one.Digest != two.Digest {
		t.Errorf("digest not deterministic: %s vs %s", one.Digest, two.Digest)
	}
}

func TestTerraformGitResolveModuleSubdir(t *testing.T) {
	t.Parallel()
	fixtureDir, _ := buildFixtureRepo(t, map[string]string{
		"root.tf":             `# root`,
		"modules/api/main.tf": `resource "null_resource" "api" {}`,
		"modules/api/vars.tf": `variable "n" { type = string }`,
		"modules/db/main.tf":  `resource "null_resource" "db" {}`,
	})

	a := &adapter{clone: cloneFromFixture(fixtureDir)}
	got, err := a.Resolve(sourceresolver.SourceSpec{
		Kind:          Kind,
		URL:           "file://" + fixtureDir,
		Ref:           "HEAD",
		ModuleSubdir:  "modules/api",
		WorkspaceRoot: t.TempDir(),
		CacheDir:      ".aggrega/cache",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if _, err := os.Stat(got.CachePath); err != nil {
		t.Errorf("cache blob missing: %v", err)
	}
}

func buildFixtureRepo(t *testing.T, files map[string]string) (dir, sha string) {
	t.Helper()
	dir = t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	for name, body := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write fixture file: %v", err)
		}
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	if err := wt.AddGlob("."); err != nil {
		t.Fatalf("AddGlob: %v", err)
	}
	commit, err := wt.Commit("fixture", &git.CommitOptions{
		Author: &object.Signature{
			Name:  "Test",
			Email: "t@example.com",
			When:  time.Unix(0, 0).UTC(),
		},
	})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	return dir, commit.String()
}

func buildFixtureRepoWithTag(t *testing.T, tag string, files map[string]string) (dir, sha string) {
	t.Helper()
	dir, sha = buildFixtureRepo(t, files)
	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatalf("PlainOpen: %v", err)
	}
	head, err := repo.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	_, err = repo.CreateTag(tag, head.Hash(), &git.CreateTagOptions{
		Tagger: &object.Signature{
			Name:  "Test",
			Email: "t@example.com",
			When:  time.Unix(0, 0).UTC(),
		},
		Message: "tag fixture",
	})
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}
	_ = config.NewConfig // keep import healthy if API surface shifts
	return dir, sha
}

func cloneFromFixture(fixtureDir string) cloneFunc {
	return func(ctx context.Context, destDir, url string, _ *http.Client) (*git.Repository, error) {
		return git.PlainCloneContext(ctx, destDir, false, &git.CloneOptions{
			URL:        fixtureDir,
			NoCheckout: true,
		})
	}
}
