// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !clidocs

package main

func main() {
	// Wire the unknown-command install hint to the live registry only in the
	// process entry point, so the test binary keeps the non-network stub.
	pluginDiscoverable = productionPluginDiscoverable
	Execute()
}
