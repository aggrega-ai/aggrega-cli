// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// A listener serves one workspace, so whether there is a workspace to serve is
// settled before the address opens. The rows below drive ListenAndServe itself —
// the entry point the command layer calls — rather than the check inside it,
// because what is under test is that nothing binds, and only the real entry
// point can show that.
//
// A Unix socket is the address throughout: the socket is a file, so its absence
// after a refusal is a direct reading of "nothing bound" rather than an
// inference from an error having been returned.

// socketAddress returns a listen address and the filesystem path behind it.
func socketAddress(t *testing.T) (address, path string) {
	t.Helper()
	path = filepath.Join(shortSocketDir(t), "mcp.sock")
	return unixAddressPrefix + path, path
}

// aDirectoryNoWorkspaceResolvesFrom returns a directory that anchors no
// workspace, and proves it does not.
//
// The proof is a precondition rather than a courtesy: every ancestor is walked
// during resolution, so a marker anywhere above the system temporary directory
// would make this directory resolve and leave the refusal rows passing for a
// reason that has nothing to do with the code under test.
func aDirectoryNoWorkspaceResolvesFrom(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	rw, err := workspaceconfig.Resolve(dir, "")
	if err != nil {
		t.Fatalf("resolve %s: %v", dir, err)
	}
	if rw.Marker() != workspaceconfig.MarkerCWD {
		t.Fatalf("a workspace resolves from %s, anchored by %v at %s, so this fixture cannot show what a "+
			"directory holding no workspace does", dir, rw.Marker(), rw.Root())
	}
	return dir
}

// TestAListenerWithNoWorkspaceToServeIsRefusedBeforeItBinds drives the startup
// refusal. Each row fixes what the process was told about its workspace and
// reads what ListenAndServe did with it.
func TestAListenerWithNoWorkspaceToServeIsRefusedBeforeItBinds(t *testing.T) {
	for _, tc := range []struct {
		name string
		// startedIn is the directory the process runs in; namedRoot is what the
		// launch supplied, which is empty when the launch named nothing.
		startedIn func(*testing.T) string
		namedRoot func(*testing.T) string
		want      []string
	}{
		{
			name:      "started in a directory no workspace resolves from, naming none",
			startedIn: aDirectoryNoWorkspaceResolvesFrom,
			namedRoot: func(*testing.T) string { return "" },
			want: []string{
				"--workspace-root",
				"AGGREGA_WORKSPACE_ROOT",
				"no workspace resolves from",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(tc.startedIn(t))
			address, path := socketAddress(t)

			// The launch runs on a goroutine of its own, under a context this test
			// can end, because a refusal that did not happen does not fail — it
			// serves, and a launch called in line would hang until the whole package
			// timed out. Read on the channel, the same defect is a fast failure
			// naming what went wrong.
			ctx, endRun := context.WithCancel(context.Background())
			t.Cleanup(endRun)
			launched := make(chan error, 1)
			go func() {
				launched <- ListenAndServe(ctx,
					Config{WorkspaceRoot: tc.namedRoot(t)},
					defaultTransportConfig(),
					ListenConfig{Address: address})
			}()

			var err error
			select {
			case err = <-launched:
			case <-time.After(runReturnCeiling):
				t.Fatal("the launch neither refused nor returned within the liveness ceiling, so it is " +
					"serving a directory holding no workspace")
			}
			if err == nil {
				t.Fatal("the listener served a directory holding no workspace")
			}
			for _, fragment := range tc.want {
				if !strings.Contains(err.Error(), fragment) {
					t.Errorf("the refusal does not name %q, so an operator cannot act on it; it reads %q",
						fragment, err)
				}
			}
			if _, statErr := os.Stat(path); statErr == nil {
				t.Errorf("%s exists, so the address was bound before the refusal", path)
			}
		})
	}
}

// TestAListenerWithAWorkspaceToServeBinds is the positive control the table
// above needs: without it, every refusal row would pass against a listener that
// refused every launch it was given.
//
// Each row drives the launch to a bound, serving address and then ends the run,
// so what it proves is that the address opened rather than that a function
// returned no error.
func TestAListenerWithAWorkspaceToServeBinds(t *testing.T) {
	for _, tc := range []struct {
		name string
		// startedIn is where the process runs; namedRoot is what the launch
		// supplied. The two rows are the two ways a workspace is settled: the
		// launch names one, or discovery finds one where the process stands.
		startedIn func(*testing.T) string
		namedRoot func(*testing.T) string
	}{
		{
			name:      "the launch names the workspace",
			startedIn: aDirectoryNoWorkspaceResolvesFrom,
			namedRoot: rootedWorkspace,
		},
		{
			name:      "the launch names none and discovery finds one",
			startedIn: rootedWorkspace,
			namedRoot: func(*testing.T) string { return "" },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(tc.startedIn(t))
			address, path := socketAddress(t)

			ctx, endRun := context.WithCancel(context.Background())
			served := make(chan error, 1)
			go func() {
				served <- ListenAndServe(ctx,
					Config{WorkspaceRoot: tc.namedRoot(t)},
					defaultTransportConfig(),
					ListenConfig{Address: address})
			}()

			awaitSocket(t, path, served)
			endRun()

			select {
			case err := <-served:
				if err != nil {
					t.Errorf("serving returned %v, want a clean end", err)
				}
			case <-time.After(runReturnCeiling):
				t.Fatal("the listener never stopped serving within the liveness ceiling")
			}
		})
	}
}

// awaitSocket waits for the listener to bind, and fails if the launch returned
// instead. The bound socket is the signal rather than a sleep, so the wait is
// over an observable and the ceiling only separates "bound" from "never bound".
func awaitSocket(t *testing.T, path string, served <-chan error) {
	t.Helper()
	deadline := time.Now().Add(runReturnCeiling)
	for {
		select {
		case err := <-served:
			t.Fatalf("the launch returned before the address was bound: %v", err)
		default:
		}
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s was never bound within the liveness ceiling", path)
		}
		time.Sleep(time.Millisecond)
	}
}
