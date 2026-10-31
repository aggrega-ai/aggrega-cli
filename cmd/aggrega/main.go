// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !clidocs

package main

func main() {
	// Wire the seams that read outside the process to their live implementations
	// only here, in the process entry point, so the test binary and the command
	// reference generator both keep the inert defaults: neither the unknown-command
	// hint nor command-tree construction resolves a workspace from whatever
	// directory it was started in, so the hint reads no index and the tree carries
	// no configured CLI plugin commands.
	unknownCommandWorkspace = workingDirectoryWorkspace
	cliPluginRegistrations = productionCLIPluginRegistrations
	Execute()
}
