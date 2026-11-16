// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package wasmtest

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmfixture"
)

// TestAGoGuestIsStaleOnceAPackageItImportsChanges lays a module holding a Go
// guest at plugins/guest/, a package beside it the guest imports and one it does
// not, lists the guest's inputs as a fixture build does, and judges an artifact
// built after every file of the module:
//
//   - fresh as built, and fresh by the guest's own directory, which is all a
//     fixture's freshness watched before it watched the guest's packages;
//   - still fresh once only the package the guest does not import, or a test
//     file of the one it does, is newer;
//   - stale once a file of the package the guest imports is newer — the case a
//     change under plugins/internal/ makes, where the guest's own directory is
//     untouched;
//   - stale once a file is added to that package, which moves its directory.
//
// 🔴 PLANTED DEFECT: in listGuestModuleInputs (build.go), keep only the guest's
// own directory: the imported package's newer file is not among the inputs, the
// artifact reads as fresh, and the case fails on it.
func TestAGoGuestIsStaleOnceAPackageItImportsChanges(t *testing.T) {
	t.Parallel()
	// The directory as go list reports it, so the paths below compare equal to the
	// ones it lists wherever the temporary directory is reached through a link.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve the module's directory: %v", err)
	}
	write := func(rel, content string) string {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
		return path
	}
	write("go.mod", "module example.test/guestmodule\n\ngo 1.21\n")
	write("plugins/guest/main.go", "package main\n\nimport _ \"example.test/guestmodule/plugins/internal/used\"\n\n"+
		"func main() {}\n")
	used := write("plugins/internal/used/used.go", "package used\n")
	usedTest := write("plugins/internal/used/used_test.go", "package used\n")
	unused := write("plugins/internal/unused/unused.go", "package unused\n")

	inputs, listErr := listGuestModuleInputs(root, "guest")
	if listErr != nil {
		t.Fatalf("list the guest's inputs: %v", listErr)
	}
	listed := map[string]bool{}
	for _, p := range inputs {
		listed[p] = true
	}
	for _, want := range []string{used, filepath.Join(root, "plugins", "guest", "main.go"),
		filepath.Dir(used), filepath.Join(root, "go.mod")} {
		if !listed[want] {
			t.Errorf("the guest's inputs do not hold %s: %q", want, inputs)
		}
	}
	for _, stray := range []string{unused, filepath.Dir(unused), usedTest} {
		if listed[stray] {
			t.Errorf("the guest's inputs hold %s, which its build does not read: %q", stray, inputs)
		}
	}

	// Every path of the module is set two hours back and the artifact one hour
	// back, so a path touched now is newer than the artifact.
	past, built := time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour)
	if err := filepath.WalkDir(root, func(path string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(path, past, past)
	}); err != nil {
		t.Fatalf("set the module's times: %v", err)
	}
	artifact := filepath.Join(t.TempDir(), "guest.wasm")
	if err := os.WriteFile(artifact, []byte("built"), 0o644); err != nil {
		t.Fatalf("write the artifact: %v", err)
	}
	if err := os.Chtimes(artifact, built, built); err != nil {
		t.Fatalf("set the artifact's time: %v", err)
	}
	touch := func(path string) {
		t.Helper()
		now := time.Now()
		if err := os.Chtimes(path, now, now); err != nil {
			t.Fatalf("touch %s: %v", path, err)
		}
	}

	if !isFreshAgainst(artifact, inputs) {
		t.Fatalf("an artifact built after every file of the module reads as stale")
	}
	touch(unused)
	touch(usedTest)
	if !isFreshAgainst(artifact, inputs) {
		t.Errorf("an artifact older only than a package the guest does not import, and a test file, reads as stale")
	}
	touch(used)
	if !wasmfixture.IsFresh(artifact, filepath.Join(root, "plugins", "guest")) {
		t.Fatalf("precondition: the guest's own directory is untouched, so it alone must read as fresh")
	}
	if isFreshAgainst(artifact, inputs) {
		t.Errorf("an artifact older than a file of a package the guest imports reads as fresh")
	}

	if err := os.Chtimes(used, past, past); err != nil {
		t.Fatalf("set %s back: %v", used, err)
	}
	added := write("plugins/internal/used/added.go", "package used\n")
	if err := os.Chtimes(added, past, past); err != nil {
		t.Fatalf("set %s back: %v", added, err)
	}
	if isFreshAgainst(artifact, inputs) {
		t.Errorf("an artifact older than a file added to a package the guest imports reads as fresh")
	}
}

// TestPublishArtifactCopyKeepsTheSourceAndPublishesByRename covers the
// variant used for an artifact another tool owns and keeps rewriting. The
// source must survive untouched, and the destination must still change only
// by rename.
func TestPublishArtifactCopyKeepsTheSourceAndPublishesByRename(t *testing.T) {
	t.Parallel()

	sourceDir := t.TempDir()
	destDir := t.TempDir()

	src := filepath.Join(sourceDir, "plugin_underscored.wasm")
	content := bytes.Repeat([]byte{0xCC}, 32*1024)
	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatalf("seed source: %v", err)
	}

	dst := filepath.Join(destDir, "plugin.wasm")
	previous := bytes.Repeat([]byte{0xDD}, 8*1024)
	if err := os.WriteFile(dst, previous, 0o644); err != nil {
		t.Fatalf("seed destination: %v", err)
	}

	reader, err := os.Open(dst)
	if err != nil {
		t.Fatalf("open destination: %v", err)
	}
	defer reader.Close()

	if err := publishArtifactCopy(src, dst); err != nil {
		t.Fatalf("publishArtifactCopy: %v", err)
	}

	held, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read through the open handle: %v", err)
	}
	if !bytes.Equal(held, previous) {
		t.Errorf("a reader holding the destination open saw %d bytes of altered content (want the %d bytes it opened): the destination was written in place rather than replaced by rename",
			len(held), len(previous))
	}

	published, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read published destination: %v", err)
	}
	if !bytes.Equal(published, content) {
		t.Errorf("published destination has %d bytes, want the source's %d", len(published), len(content))
	}

	survived, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read source after publish: %v", err)
	}
	if !bytes.Equal(survived, content) {
		t.Errorf("source changed across the publish: it belongs to the tool that produced it and must be left alone")
	}

	entries, err := os.ReadDir(filepath.Join(destDir, wasmfixture.StageDirName))
	if err != nil {
		t.Fatalf("read staging area: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("staging area holds %d leftover entries after publish, want none", len(entries))
	}
}

// TestAGoGuestIsStaleOnceALocallyReplacedModuleChanges lays a module holding a
// Go guest that imports a package of a second module, which the first module's
// go.mod replaces with a directory beside it, and checks that the package's file
// is among the guest's inputs, so an artifact older than it reads as stale: the
// guest is built against that directory, not a released version.
//
// 🔴 PLANTED DEFECT: in guestPackagesTemplate (build.go), keep only the main
// module's packages: the replaced module's file is not among the inputs, and the
// case fails on it.
func TestAGoGuestIsStaleOnceALocallyReplacedModuleChanges(t *testing.T) {
	t.Parallel()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve the layout's directory: %v", err)
	}
	write := func(rel, content string) string {
		t.Helper()
		path := filepath.Join(parent, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
		return path
	}
	write("kit/go.mod", "module example.test/kit\n\ngo 1.21\n")
	kitFile := write("kit/pkg/pkg.go", "package pkg\n")
	write("cli/go.mod", "module example.test/cli\n\ngo 1.21\n\nrequire example.test/kit v0.0.0\n\n"+
		"replace example.test/kit => ../kit\n")
	write("cli/plugins/guest/main.go", "package main\n\nimport _ \"example.test/kit/pkg\"\n\nfunc main() {}\n")

	inputs, err := listGuestModuleInputs(filepath.Join(parent, "cli"), "guest")
	if err != nil {
		t.Fatalf("list the guest's inputs: %v", err)
	}
	listed := false
	for _, p := range inputs {
		listed = listed || p == kitFile
	}
	if !listed {
		t.Fatalf("the guest's inputs do not hold %s, a file of the locally replaced module it imports: %q", kitFile, inputs)
	}

	artifact := filepath.Join(t.TempDir(), "guest.wasm")
	if err := os.WriteFile(artifact, []byte("built"), 0o644); err != nil {
		t.Fatalf("write the artifact: %v", err)
	}
	built := time.Now().Add(-time.Hour)
	if err := os.Chtimes(artifact, built, built); err != nil {
		t.Fatalf("set the artifact's time: %v", err)
	}
	if isFreshAgainst(artifact, inputs) {
		t.Errorf("an artifact older than a file of the locally replaced module the guest imports reads as fresh")
	}
}

// TestARustCrateIsStaleOnceACrateItReachesByPathChanges lays a crate that
// depends by path on a crate beside it and checks that cargoLocalPackageDirs
// lists both directories, so a change in the path dependency moves an artifact
// stale while the crate's own directory is untouched. It skips without cargo.
//
// 🔴 PLANTED DEFECT: in cargoLocalPackageDirs (build.go), keep only the first
// local package: the path dependency's directory is not listed, the artifact
// reads as fresh, and the case fails on it.
func TestARustCrateIsStaleOnceACrateItReachesByPathChanges(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("cargo"); err != nil {
		t.Skipf("cargo is not on PATH: %v", err)
	}
	parent := t.TempDir()
	write := func(rel, content string) string {
		t.Helper()
		path := filepath.Join(parent, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
		return path
	}
	write("core/Cargo.toml", "[package]\nname = \"core-dep\"\nversion = \"0.1.0\"\nedition = \"2021\"\n\n[workspace]\n")
	coreFile := write("core/src/lib.rs", "pub fn f() {}\n")
	manifest := write("guest/Cargo.toml", "[package]\nname = \"guest\"\nversion = \"0.1.0\"\nedition = \"2021\"\n\n"+
		"[workspace]\n\n[dependencies]\ncore-dep = { path = \"../core\" }\n")
	write("guest/src/lib.rs", "pub fn g() { core_dep::f() }\n")
	lock := exec.Command("cargo", "generate-lockfile", "--offline", "--manifest-path", manifest)
	if out, err := lock.CombinedOutput(); err != nil {
		t.Fatalf("generate the crate's lockfile: %v\n%s", err, out)
	}

	dirs, err := cargoLocalPackageDirs(manifest)
	if err != nil {
		t.Fatalf("list the crate's local packages: %v", err)
	}
	past, built := time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour)
	if err := filepath.WalkDir(parent, func(path string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(path, past, past)
	}); err != nil {
		t.Fatalf("set the layout's times: %v", err)
	}
	artifact := filepath.Join(t.TempDir(), "guest.wasm")
	if err := os.WriteFile(artifact, []byte("built"), 0o644); err != nil {
		t.Fatalf("write the artifact: %v", err)
	}
	if err := os.Chtimes(artifact, built, built); err != nil {
		t.Fatalf("set the artifact's time: %v", err)
	}
	if !wasmfixture.IsFresh(artifact, dirs...) {
		t.Fatalf("an artifact built after every file of both crates reads as stale (listed %q)", dirs)
	}
	now := time.Now()
	if err := os.Chtimes(coreFile, now, now); err != nil {
		t.Fatalf("touch %s: %v", coreFile, err)
	}
	if !wasmfixture.IsFresh(artifact, filepath.Join(parent, "guest")) {
		t.Fatalf("precondition: the crate's own directory is untouched, so it alone must read as fresh")
	}
	if wasmfixture.IsFresh(artifact, dirs...) {
		t.Errorf("an artifact older than a file of the crate reached by path reads as fresh (listed %q)", dirs)
	}
}
