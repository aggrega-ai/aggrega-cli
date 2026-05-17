// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestCopyTree_PreservesExecutableBit pins the exec-bit contract: a
// source file with the executable bit set must land in the destination
// with the executable bit also set, so wrapper scripts and similar
// remain runnable in the scratch copy.
func TestCopyTree_PreservesExecutableBit(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "out")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("mkdir dst: %v", err)
	}

	exec := filepath.Join(src, "script.sh")
	if err := os.WriteFile(exec, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatalf("write src: %v", err)
	}
	plain := filepath.Join(src, "plain.txt")
	if err := os.WriteFile(plain, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write plain: %v", err)
	}

	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copyTree: %v", err)
	}

	info, err := os.Stat(filepath.Join(dst, "script.sh"))
	if err != nil {
		t.Fatalf("stat dst script: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("executable bit lost on script.sh: mode=%s", info.Mode())
	}

	infoPlain, err := os.Stat(filepath.Join(dst, "plain.txt"))
	if err != nil {
		t.Fatalf("stat dst plain: %v", err)
	}
	if infoPlain.Mode().Perm()&0o111 != 0 {
		t.Errorf("plain.txt picked up executable bit: mode=%s", infoPlain.Mode())
	}
}

// TestCopyTree_FailsFastOnSymlink pins the symlink contract: a symbolic
// link anywhere under the source tree must cause copyTree to return an
// error rather than copy or follow the link.
func TestCopyTree_FailsFastOnSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink test requires unix-style filesystem semantics")
	}
	t.Parallel()
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "out")

	target := filepath.Join(src, "real.txt")
	if err := os.WriteFile(target, []byte("real"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(src, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	err := copyTree(src, dst)
	if err == nil {
		t.Fatalf("copyTree succeeded; want failure on symlink")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error does not mention symlink: %v", err)
	}
}

// TestCopyTree_SkipsExcludedDirs pins that .git and similar build-output
// directory names are not copied to the scratch destination.
func TestCopyTree_SkipsExcludedDirs(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "out")

	gitDir := filepath.Join(src, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte("[core]\n"), 0o644); err != nil {
		t.Fatalf("write .git/config: %v", err)
	}
	binDir := filepath.Join(src, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	keep := filepath.Join(src, "keep.txt")
	if err := os.WriteFile(keep, []byte("kept"), 0o644); err != nil {
		t.Fatalf("write keep: %v", err)
	}

	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copyTree: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dst, ".git")); !os.IsNotExist(err) {
		t.Errorf(".git directory present in dst; want excluded (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "bin")); !os.IsNotExist(err) {
		t.Errorf("bin directory present in dst; want excluded (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "keep.txt")); err != nil {
		t.Errorf("keep.txt missing from dst: %v", err)
	}
}

// TestSetupScratch_CreatesFakeHome pins that setupScratch produces the
// scratch root with the expected layout: copy of src plus an empty
// .fake-home directory.
func TestSetupScratch_CreatesFakeHome(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module test\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	scratch, err := setupScratch(src)
	if err != nil {
		t.Fatalf("setupScratch: %v", err)
	}
	defer os.RemoveAll(scratch)

	if _, err := os.Stat(filepath.Join(scratch, "go.mod")); err != nil {
		t.Errorf("go.mod not in scratch: %v", err)
	}
	info, err := os.Stat(filepath.Join(scratch, ".fake-home"))
	if err != nil {
		t.Fatalf(".fake-home not in scratch: %v", err)
	}
	if !info.IsDir() {
		t.Errorf(".fake-home is not a directory: mode=%s", info.Mode())
	}
}

// TestGoTestEnv_OverridesHomeAndGoflags pins that goTestEnv injects the
// scratch-local HOME and read-only GOFLAGS, regardless of what the
// parent process inherited.
func TestGoTestEnv_OverridesHomeAndGoflags(t *testing.T) {
	t.Parallel()
	scratch := t.TempDir()
	env := goTestEnv(scratch)

	want := map[string]string{
		"HOME":    filepath.Join(scratch, ".fake-home"),
		"GOFLAGS": "-mod=readonly",
	}
	got := map[string]string{}
	for _, e := range env {
		for k := range want {
			if strings.HasPrefix(e, k+"=") {
				got[k] = strings.TrimPrefix(e, k+"=")
			}
		}
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("env[%s] = %q; want %q", k, got[k], v)
		}
	}
}
