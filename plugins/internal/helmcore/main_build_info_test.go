// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package helmcore

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
)

// TestMain gives every test in this package's test binary — the in-package tests
// and the external helmcore_test ones alike, which share one binary and one copy
// of this package — the build information a main-package build of the provider
// carries. The Go toolchain writes a library package's test binary's build
// information before the test's imports are known, so that binary lists none of
// the modules it depends on, and every render naming no Kubernetes version would
// otherwise halt for want of the k8s.io/client-go entry its default is read from.
//
// The entry's version is the one this module's go.mod requires, which is what the
// provider's own build, made from this module alone, records. So a test here
// proves how the default is read from build information and how a module version
// maps to a Kubernetes version; that the provider's build really carries the
// entry, and that its default follows go.mod, is proved end to end through the
// embedded provider by TestHelmKubernetesVersion_TheChartRendersForTheVersionTheReleaseNames
// in internal/compiler, which reads go.mod on its own.
func TestMain(m *testing.M) {
	version, err := requiredModuleVersion(filepath.Join("..", "..", "..", "go.mod"), clientLibraryModule)
	if err != nil {
		fmt.Fprintf(os.Stderr, "helmcore tests: the build information the default Kubernetes version is read from: %v\n", err)
		os.Exit(1)
	}
	readBuildInfo = buildInfoWithModule(debug.ReadBuildInfo, clientLibraryModule, version)
	os.Exit(m.Run())
}

// buildInfoWithModule returns a reader yielding what read yields with the entry for
// module set to version: replaced where read lists the module, appended where it
// does not, and on a fresh value, so the toolchain's own is never written.
func buildInfoWithModule(read func() (*debug.BuildInfo, bool), module, version string) func() (*debug.BuildInfo, bool) {
	return func() (*debug.BuildInfo, bool) {
		info := &debug.BuildInfo{}
		if built, ok := read(); ok {
			copied := *built
			info = &copied
		}
		deps := make([]*debug.Module, 0, len(info.Deps)+1)
		for _, dep := range info.Deps {
			if dep.Path != module {
				deps = append(deps, dep)
			}
		}
		info.Deps = append(deps, &debug.Module{Path: module, Version: version})
		return info, true
	}
}

// requiredModuleVersion reads the version a go.mod file requires of module, from a
// require block or a single-line require, and refuses a file requiring it nowhere
// rather than letting the default fall to whatever the binary happens to carry.
func requiredModuleVersion(goModPath, module string) (string, error) {
	content, err := os.ReadFile(goModPath)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", goModPath, err)
	}
	inRequire := false
	for _, line := range strings.Split(string(content), "\n") {
		if comment := strings.Index(line, "//"); comment >= 0 {
			line = line[:comment]
		}
		fields := strings.Fields(line)
		switch {
		case len(fields) == 0:
			continue
		case fields[0] == ")":
			inRequire = false
			continue
		case fields[0] == "require" && len(fields) == 2 && fields[1] == "(":
			inRequire = true
			continue
		case fields[0] == "require":
			fields = fields[1:]
		case !inRequire:
			continue
		}
		if len(fields) == 2 && fields[0] == module {
			return fields[1], nil
		}
	}
	return "", fmt.Errorf("%s requires no %s", goModPath, module)
}
