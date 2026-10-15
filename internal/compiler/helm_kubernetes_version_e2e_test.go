// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// End-to-end coverage of the Kubernetes version a chart rendered from source is
// rendered for: the version the Release names under aggrega.ai/kubernetes-version,
// on a boundary or on a component, and where it names none, the version the stock
// `helm template` of the same helm release renders for. The fixture's chart writes
// the version it reads into a ConfigMap and chooses its token mechanism by it, as
// charts do: from 1.21 a projected token, below it a first-party one.

package compiler_test

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// kubernetesVersionInput compiles one Release of the Kubernetes version fixture.
func kubernetesVersionInput(release string) pipelinetest.Input {
	return pipelinetest.Input{
		ReleasePath:  "testdata/helm-kubernetes-version/" + release,
		CatalogPaths: []string{"testdata/helm-kubernetes-version/catalog"},
	}
}

// clientLibraryKubernetesVersion is the version helm's own build stamps for the
// k8s.io/client-go this module requires: the module's major version plus one, its
// minor version, a zero patch. It is read from go.mod, where helm's build reads
// it, independently of what the provider reads, so a default that stops following
// the client library fails the row that compares against it.
func clientLibraryKubernetesVersion(t *testing.T) string {
	t.Helper()
	goMod, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatalf("read the module's go.mod: %v", err)
	}
	for _, line := range strings.Split(string(goMod), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "k8s.io/client-go" {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(fields[1], "v"), ".")
		major, err := strconv.Atoi(parts[0])
		if err != nil || len(parts) < 2 {
			t.Fatalf("go.mod requires k8s.io/client-go %q, which carries no major and minor version", fields[1])
		}
		return "v" + strconv.Itoa(major+1) + "." + parts[1] + ".0"
	}
	t.Fatalf("go.mod requires no k8s.io/client-go")
	return ""
}

// renderedKubernetesVersions reads, per component, the version and token mechanism
// its ConfigMap records, by the file the component's render emitted.
func renderedKubernetesVersions(t *testing.T, artifacts map[string][]byte) map[string][2]string {
	t.Helper()
	out := map[string][2]string{}
	for p, content := range artifacts {
		base := path.Base(filepath.ToSlash(p))
		component, _, ok := strings.Cut(base, "-configmap-")
		if !ok {
			continue
		}
		var doc struct {
			Data struct {
				Version string `yaml:"version"`
				Token   string `yaml:"token"`
			} `yaml:"data"`
		}
		if err := yaml.Unmarshal(content, &doc); err != nil {
			t.Fatalf("decode %s: %v\n%s", p, err, content)
		}
		out[component] = [2]string{doc.Data.Version, doc.Data.Token}
	}
	return out
}

// TestHelmKubernetesVersion_TheChartRendersForTheVersionTheReleaseNames builds one
// chart for three components through the real pipeline and the embedded helm
// provider: plain in a boundary naming no version, legacy in a boundary naming
// 1.20, and current beside it naming v1.31.2 for itself.
func TestHelmKubernetesVersion_TheChartRendersForTheVersionTheReleaseNames(t *testing.T) {
	t.Parallel()

	defaultVersion := clientLibraryKubernetesVersion(t)
	result := pipelinetest.Build(t, kubernetesVersionInput("release.yaml"))
	got := renderedKubernetesVersions(t, result.Artifacts)

	for _, want := range []struct {
		component, why, version, token string
	}{
		{"plain", "no annotation renders for the client library's version", defaultVersion, "projected"},
		{"legacy", "the boundary's 1.20 renders for v1.20, below the chart's 1.21 branch", "v1.20", "first-party"},
		{"current", "the component's own version wins over its boundary's", "v1.31.2", "projected"},
	} {
		rendered, ok := got[want.component]
		if !ok {
			t.Errorf("%s: the build emitted no ConfigMap for %s; it emitted %v", want.why, want.component, got)
			continue
		}
		if rendered[0] != want.version || rendered[1] != want.token {
			t.Errorf("%s: %s rendered for %q with its %q branch, want %q with its %q branch",
				want.why, want.component, rendered[0], rendered[1], want.version, want.token)
		}
	}
	if len(got) != 3 {
		t.Errorf("the build emitted %d ConfigMaps, want one per component: %v", len(got), got)
	}
}

// TestHelmKubernetesVersion_AVersionTheParserRefusesHaltsTheBuild holds the halt
// against the condition it carries: the value-not-representable condition, with a
// sentence naming the value, the node and the boundary it was set on.
func TestHelmKubernetesVersion_AVersionTheParserRefusesHaltsTheBuild(t *testing.T) {
	t.Parallel()

	_, err := pipelinetest.BuildE(t, kubernetesVersionInput("release-malformed.yaml"))
	if err == nil {
		t.Fatal("a chart rendered for the unparsable version \"one.two\"; want the build to halt")
	}
	chain := cerrors.CauseChain(err)
	if !errors.Is(err, wasm.ErrPluginValueNotRepresentable()) {
		t.Fatalf("the build halted for a different reason than the unparsable version: %s", chain)
	}
	for _, want := range []string{
		`node "plain" in boundary "broken"`,
		`annotation aggrega.ai/kubernetes-version: "one.two" is not a Kubernetes version a chart can be rendered for`,
	} {
		if !strings.Contains(chain, want) {
			t.Errorf("the halt does not carry %q:\n%s", want, chain)
		}
	}
}

// TestHelmKubernetesVersion_RenderingForTheNamedVersionsIsDeterministic holds that
// two builds of the Release emit byte-identical trees.
func TestHelmKubernetesVersion_RenderingForTheNamedVersionsIsDeterministic(t *testing.T) {
	t.Parallel()

	pipelinetest.AssertDeterministic(t, kubernetesVersionInput("release.yaml"))
}
