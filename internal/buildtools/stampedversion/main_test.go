// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testStamp is the version the probe is stamped with: deliberately not shaped
// like a release, so cutting a release never touches this file, and long
// enough to occur exactly once in a built probe.
const testStamp = "0.0.0-stamped-version-test"

// TestReadStampedVersion builds the probe program for each executable format a
// release carries and reads it back: stamped as the build script stamps, it
// reads the stamp exactly; unstamped, or stamped with an empty value, it is
// refused; and with one byte of the stamp changed in a copy, it reads the
// changed bytes, so the reader is reading the stamp itself rather than
// something that happens to agree with it. Real executables are built rather
// than hand-assembled, because a hand-assembled file would prove only that the
// reader agrees with its author's idea of the format.
func TestReadStampedVersion(t *testing.T) {
	tests := []struct {
		name, goos, goarch string
	}{
		{"ELF 64-bit", "linux", "amd64"},
		{"ELF 32-bit", "linux", "386"},
		{"PE", "windows", "amd64"},
		{"Mach-O arm64", "darwin", "arm64"},
		{"Mach-O amd64", "darwin", "amd64"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stamped := buildProbe(t, tc.goos, tc.goarch, stampFlag)
			got, err := readFile(stamped)
			if err != nil {
				t.Fatalf("stamped %s/%s probe: %v", tc.goos, tc.goarch, err)
			}
			if got != testStamp {
				t.Fatalf("stamped %s/%s probe reads %q, want %q", tc.goos, tc.goarch, got, testStamp)
			}

			unstamped := buildProbe(t, tc.goos, tc.goarch, "")
			got, err = readFile(unstamped)
			if err == nil || !strings.Contains(err.Error(), "carries no stamped version") {
				t.Fatalf("unstamped %s/%s probe: got %q, %v; want the refusal naming no stamped version", tc.goos, tc.goarch, got, err)
			}

			empty := buildProbe(t, tc.goos, tc.goarch, "-X main.version=")
			got, err = readFile(empty)
			if err == nil || !strings.Contains(err.Error(), "carries no stamped version: it was stamped with an empty value") {
				t.Fatalf("%s/%s probe stamped empty: got %q, %v; want the refusal naming no stamped version and the empty value", tc.goos, tc.goarch, got, err)
			}

			data, err := os.ReadFile(stamped)
			if err != nil {
				t.Fatal(err)
			}
			if n := bytes.Count(data, []byte(testStamp)); n != 1 {
				t.Fatalf("the stamp occurs %d times in the %s/%s probe; one byte of it can be changed unambiguously only when it occurs once", n, tc.goos, tc.goarch)
			}
			i := bytes.Index(data, []byte(testStamp))
			data[i+len(testStamp)-1] = 'X'
			changed := filepath.Join(t.TempDir(), "changed")
			if err := os.WriteFile(changed, data, 0o644); err != nil {
				t.Fatal(err)
			}
			want := testStamp[:len(testStamp)-1] + "X"
			if got, err := readFile(changed); err != nil || got != want {
				t.Fatalf("%s/%s probe with one stamp byte changed reads %q, %v; want %q", tc.goos, tc.goarch, got, err, want)
			}
		})
	}
}

// TestReadRefusesWhatIsNotOneExecutable holds the refusals for input that is
// not a single executable of a format the reader reads: each is an error
// naming the reason, and none is a panic or an empty version.
func TestReadRefusesWhatIsNotOneExecutable(t *testing.T) {
	stamped := buildProbe(t, "linux", "amd64", stampFlag)
	data, err := os.ReadFile(stamped)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		input []byte
		want  string
	}{
		{"text", []byte("not an executable at all\n"), "not an ELF, PE or Mach-O executable"},
		{"shorter than a header", []byte("MZ"), "read the file's first bytes"},
		{"universal macOS binary", []byte{0xca, 0xfe, 0xba, 0xbe, 0, 0, 0, 2}, "universal macOS binary"},
		{"truncated ELF", data[:4096], "ELF"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := read(bytes.NewReader(tc.input))
			if err == nil {
				t.Fatalf("read %s: got %q and no error; want a refusal containing %q", tc.name, got, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("read %s: error %q does not contain %q", tc.name, err, tc.want)
			}
		})
	}
}

// stampFlag is the linker flag stamping the probe as the build script stamps.
const stampFlag = "-X main.version=" + testStamp

// buildProbe builds testdata/probe for goos/goarch with the build script's
// flags — trimmed paths and a cleared build id — and, unless xflag is empty,
// the -X flag it names, returning the executable's path.
func buildProbe(t *testing.T, goos, goarch, xflag string) string {
	t.Helper()

	ldflags := "-buildid="
	if xflag != "" {
		ldflags = xflag + " " + ldflags
	}
	out := filepath.Join(t.TempDir(), "probe")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-ldflags", ldflags, "-o", out, "./testdata/probe")
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build the %s/%s probe: %v\n%s", goos, goarch, err, b)
	}
	return out
}
