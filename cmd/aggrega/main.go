// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !clidocs

package main

func main() {
	// Wire the seams that read outside the process to their live implementations
	// only here, in the process entry point, so the test binary and the command
	// reference generator both keep the inert defaults: the install hint keeps
	// the non-network stub, and command-tree construction contributes no
	// configured CLI plugin commands rather than resolving a workspace from
	// whatever directory it was started in.
	pluginDiscoverable = productionPluginDiscoverable
	cliPluginRegistrations = productionCLIPluginRegistrations
	Execute()
}
