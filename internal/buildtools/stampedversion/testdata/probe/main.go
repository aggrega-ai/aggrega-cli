// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Command probe is the smallest program carrying a main.version, built by the
// stamp reader's tests for each executable format and read back.
package main

// version is set with -X main.version=<version>, as the CLI's is.
var version string

func main() { println(version) }
