// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore

import (
	"errors"
	"fmt"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"helm.sh/helm/v3/pkg/chartutil"
)

// clientLibraryModule is the module whose version helm's own build stamps into the
// SDK as the Kubernetes version `helm template` renders for. The embedded SDK is
// built without that stamp, so its built-in capabilities name a placeholder
// version; the render reads the same module's version from the binary instead.
const clientLibraryModule = "k8s.io/client-go"

// renderCapabilities returns the capabilities a chart is rendered against for one
// node: the SDK's built-in API version set and helm version, with the Kubernetes
// version the node's merged annotations name under
// rendercore.AnnotationKubernetesVersion, or, where they name none, the version
// [DefaultKubeVersion] reports.
//
// The value is parsed by the SDK's own chartutil.ParseKubeVersion, the parser
// behind `helm template --kube-version`, so the render accepts exactly the forms
// that flag accepts and writes into .Capabilities.KubeVersion exactly what that
// flag writes. An empty value is unset, as an empty flag is. A value the parser
// refuses halts the render under the value-not-representable condition, naming
// the value and the node and boundary it reached; it never falls back to the
// default, because a chart rendered for a version other than the one its author
// named would emit a shape that cluster may not run.
//
// The capabilities are a copy: the SDK's built-in set is a shared package value,
// and writing a node's version into it would leak that version into every later
// render.
func renderCapabilities(nodeCtx rendercore.NodeRenderContext) (*chartutil.Capabilities, error) {
	kubeVersion, err := renderKubeVersion(nodeCtx)
	if err != nil {
		return nil, err
	}
	caps := chartutil.DefaultCapabilities.Copy()
	caps.KubeVersion = kubeVersion
	return caps, nil
}

// renderKubeVersion resolves the Kubernetes version one node's chart renders for.
func renderKubeVersion(nodeCtx rendercore.NodeRenderContext) (chartutil.KubeVersion, error) {
	authored := rendercore.KubernetesVersionFromAnnotations(nodeCtx.Annotations)
	if authored == "" {
		return DefaultKubeVersion()
	}
	parsed, err := chartutil.ParseKubeVersion(authored)
	if err != nil {
		where := fmt.Sprintf("node %q", nodeCtx.NodeID)
		if nodeCtx.BoundaryPath != "" {
			where = fmt.Sprintf("node %q in boundary %q", nodeCtx.NodeID, nodeCtx.BoundaryPath)
		}
		return chartutil.KubeVersion{}, cerrors.Wrap(wasm.ErrPluginValueNotRepresentable(),
			cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID},
			fmt.Sprintf("helm: %s: annotation %s: %q is not a Kubernetes version a chart can be rendered for: %v; "+
				"write the version of the cluster the chart deploys to, such as \"1.31\" or \"v1.31.2\", "+
				"on the component or on its boundary",
				where, rendercore.AnnotationKubernetesVersion, authored, err))
	}
	return *parsed, nil
}

// DefaultKubeVersion returns the Kubernetes version a chart is rendered for where
// no annotation names one: the version the stock `helm template` of the same helm
// release renders for.
//
// Helm's own build writes that version into the SDK from the version of the
// k8s.io/client-go module it compiles against — the module's major version plus
// one, its minor version, and a zero patch, so client library v0.35.1 renders for
// Kubernetes v1.35.0. This binary links the same module through the same SDK, so
// the version is read from the module's entry in the binary's own build
// information rather than written down here: it moves exactly when the module
// requirement moves, and a build of the provider and its rendered output can never
// disagree about it.
//
// It fails where the binary carries no build information or no entry for the
// module. A main-package binary built from this module in module mode carries
// both, the provider itself included, so there a failure names a build made some
// other way rather than a property of any input. A library package's test binary
// lists none of the modules it depends on, so this package's own tests supply the
// entry.
func DefaultKubeVersion() (chartutil.KubeVersion, error) {
	return defaultKubeVersion()
}

// readBuildInfo reads the binary's build information. It is a variable because
// the Go toolchain writes a library package's test binary's build information
// before the test's imports are known, so that binary lists none of the modules
// it depends on; this package's tests replace it with a reader carrying the
// module entry a main-package build carries.
var readBuildInfo = debug.ReadBuildInfo

// defaultKubeVersion reads the build information once per process: it cannot
// change while the binary runs.
var defaultKubeVersion = sync.OnceValues(func() (chartutil.KubeVersion, error) {
	info, ok := readBuildInfo()
	if !ok {
		return chartutil.KubeVersion{}, errors.New("the Kubernetes version to render for is not named, " +
			"and this provider carries no build information to read the default from")
	}
	for _, dep := range info.Deps {
		if dep.Path == clientLibraryModule {
			return kubeVersionOfClientLibrary(dep.Version)
		}
	}
	return chartutil.KubeVersion{}, fmt.Errorf("the Kubernetes version to render for is not named, "+
		"and this provider was built without the %s module the default is read from", clientLibraryModule)
})

// kubeVersionOfClientLibrary maps a k8s.io/client-go module version to the
// Kubernetes version helm's build stamps for it: major plus one, the minor as it
// is, and a zero patch, written as the SDK writes its own built-in version.
func kubeVersionOfClientLibrary(moduleVersion string) (chartutil.KubeVersion, error) {
	fields := strings.Split(strings.TrimPrefix(moduleVersion, "v"), ".")
	if len(fields) < 2 {
		return chartutil.KubeVersion{}, fmt.Errorf("%s version %q carries no minor version to derive the default Kubernetes version from",
			clientLibraryModule, moduleVersion)
	}
	major, majorErr := strconv.Atoi(fields[0])
	minor, minorErr := strconv.Atoi(fields[1])
	if majorErr != nil || minorErr != nil || major < 0 || minor < 0 {
		return chartutil.KubeVersion{}, fmt.Errorf("%s version %q is not a module version the default Kubernetes version can be derived from",
			clientLibraryModule, moduleVersion)
	}
	kubeMajor, kubeMinor := strconv.Itoa(major+1), strconv.Itoa(minor)
	return chartutil.KubeVersion{
		Version: fmt.Sprintf("v%s.%s.0", kubeMajor, kubeMinor),
		Major:   kubeMajor,
		Minor:   kubeMinor,
	}, nil
}
