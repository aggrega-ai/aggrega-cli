// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

// End-to-end proofs of the attach_egress_allowlist importer setting through the
// real Import, over the catalog-folder workspaces the platform layout's own
// proofs use, with the standard library's Components copied in from the
// aggrega-hub repository checked out beside this one:
//
//   - a committed real Langflow export imported with the setting true drafts
//     core/egress-allowlist attached to the platform, with nothing of its
//     infrastructure set, and the import names the attachment;
//   - built on helm, the draft halts until the Release affirms the cluster
//     enforces network policy, lists an address range for each connection whose
//     target the cluster does not run and gives the platform's workloads a
//     namespace — and, given all three, renders a network policy admitting the
//     platform's own pods, the cluster's DNS and the drafted connections' targets,
//     and nothing else;
//   - with the setting absent, false or not a boolean, every committed source is
//     drafted exactly as with no setting at all.
//
// These tests read catalog entities from the aggrega-hub repository checked out
// beside this one. Without that checkout they skip, naming it.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// egressAttachSettings returns settings with attach_egress_allowlist true added.
func egressAttachSettings(settings map[string]any) map[string]any {
	out := map[string]any{"attach_egress_allowlist": true}
	for k, v := range settings {
		out[k] = v
	}
	return out
}

// egressBuildPaths are the catalog paths a drafted platform carrying the
// attachment is built against: paths, the Feature and its Kubernetes
// realization.
func egressBuildPaths(t testing.TB, paths []string) []string {
	return append(append([]string{}, paths...),
		platformHub(t)+"/feature/core/egress-allowlist/1.0.0/feature.yaml",
		platformHub(t)+"/feature-recipe/helm/egress-allowlist/1.0.0/recipe.yaml")
}

// egressNamespace is the namespace the Releases below give the boundary holding
// the platform, and egressModelRange the address range they list for the
// connection to a hosted model.
const (
	egressNamespace  = "flows"
	egressModelRange = "203.0.113.0/24"
)

// egressAttachment is what a drafted Aggregate's platform instance carries under
// features, read as the compiler reads it.
type egressAttachment struct {
	Spec struct {
		Components map[string]struct {
			Features map[string]map[string]any `yaml:"features"`
		} `yaml:"components"`
	} `yaml:"spec"`
}

// featuresOf returns the features the drafted Aggregate text declares on the
// instance keyed key.
func featuresOf(t *testing.T, text, key string) map[string]map[string]any {
	t.Helper()
	var agg egressAttachment
	if err := yaml.Unmarshal([]byte(text), &agg); err != nil {
		t.Fatalf("the drafted Aggregate is not YAML: %v", err)
	}
	return agg.Spec.Components[key].Features
}

// egressRelease is how a Release written beside a drafted platform sets the
// attachment the draft declares: whether it affirms the cluster enforces network
// policy, the address range of each connection it lists, by target, and whether
// it gives the boundary holding the platform a namespace. It attaches nothing new:
// it sets the declared attachment's infra, under its key, and names no type.
type egressRelease struct {
	affirm    bool
	ranges    map[string]string
	namespace bool
}

// withEgress rewrites the Manifest and the Release writeRelease wrote in dir —
// for the platform instance keyed platform — so the Manifest realizes the Feature
// on helm and the Release sets the attachment as e says. It fails the test where
// the written files do not read as expected, so a rewrite never silently does
// nothing.
func withEgress(t *testing.T, dir, releasePath, platform string, e egressRelease) string {
	t.Helper()
	edit := func(path, old, replacement string) {
		t.Helper()
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !strings.Contains(string(body), old) {
			t.Fatalf("%s does not hold %q", path, old)
		}
		if err := os.WriteFile(path, []byte(strings.Replace(string(body), old, replacement, 1)), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	edit(filepath.Join(dir, "manifest.yaml"), "    features:\n      type:\n",
		"    features:\n      type:\n        core/egress-allowlist:1.0.0: helm/egress-allowlist:1.0.0\n")

	var attach strings.Builder
	if e.affirm || len(e.ranges) > 0 {
		attach.WriteString("            egress-allowlist:\n              infra:\n")
		if e.affirm {
			attach.WriteString("                control_plane_present: true\n")
		}
		if len(e.ranges) > 0 {
			attach.WriteString("                admitted_destinations:\n")
			targets := make([]string, 0, len(e.ranges))
			for target := range e.ranges {
				targets = append(targets, target)
			}
			slices.Sort(targets)
			for _, target := range targets {
				attach.WriteString("                  - target: " + target + "\n                    cidr: " + e.ranges[target] + "\n")
			}
		}
	}
	// The platform's entry is the last one the Release writes, so the first line
	// at its indentation opening its features, or else its uses, is its own. Each
	// anchor opens with a line feed so a deeper-indented line never matches it.
	body, err := os.ReadFile(releasePath)
	if err != nil {
		t.Fatalf("read the Release: %v", err)
	}
	text := string(body)
	instance := "\n        " + platform + ":\n          infra:\n"
	at := strings.Index(text, instance)
	if at < 0 {
		t.Fatalf("the Release holds no entry for %s:\n%s", platform, text)
	}
	if attach.Len() > 0 {
		if strings.Contains(text[at:], "\n          features:\n") {
			edit(releasePath, "\n          features:\n", "\n          features:\n"+attach.String())
		} else {
			edit(releasePath, "\n          uses:\n", "\n          features:\n"+attach.String()+"          uses:\n")
		}
	}
	if e.namespace {
		edit(releasePath, "\n      components:\n", "\n      annotations:\n        aggrega.ai/namespace: "+egressNamespace+
			"\n      components:\n")
	}
	return releasePath
}

// networkPolicy is the part of a rendered NetworkPolicy the cases read.
type networkPolicy struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
	Spec struct {
		PodSelector struct {
			MatchLabels map[string]string `yaml:"matchLabels"`
		} `yaml:"podSelector"`
		PolicyTypes []string `yaml:"policyTypes"`
		Egress      []struct {
			To []struct {
				IPBlock *struct {
					CIDR string `yaml:"cidr"`
				} `yaml:"ipBlock"`
				NamespaceSelector *struct {
					MatchLabels map[string]string `yaml:"matchLabels"`
				} `yaml:"namespaceSelector"`
				PodSelector *struct {
					MatchLabels map[string]string `yaml:"matchLabels"`
				} `yaml:"podSelector"`
			} `yaml:"to"`
			Ports []struct {
				Port     int    `yaml:"port"`
				Protocol string `yaml:"protocol"`
			} `yaml:"ports"`
		} `yaml:"egress"`
	} `yaml:"spec"`
}

// renderedPolicy reads the one network policy the attachment on platform rendered.
func renderedPolicy(t *testing.T, artifacts map[string][]byte, platform string) networkPolicy {
	t.Helper()
	var p networkPolicy
	doc := renderedEnding(t, artifacts, "/"+platform+"--egress-allowlist--networkpolicy.yaml")
	if err := yaml.Unmarshal(doc, &p); err != nil || p.Kind != "NetworkPolicy" {
		t.Fatalf("the attachment rendered no NetworkPolicy (%v):\n%s", err, doc)
	}
	return p
}

// admitted is what one network policy admits, read as sets: the pods it admits,
// each by the labels it selects them by, in the namespace it names; the DNS pods;
// and the address ranges.
type admitted struct {
	pods   []map[string]string
	dns    bool
	ranges []string
}

// admittedBy reads what policy p admits, failing the test on a destination none
// of the three shapes describes.
func admittedBy(t *testing.T, p networkPolicy) admitted {
	t.Helper()
	var a admitted
	for _, rule := range p.Spec.Egress {
		for _, to := range rule.To {
			switch {
			case to.IPBlock != nil:
				a.ranges = append(a.ranges, to.IPBlock.CIDR)
			case to.NamespaceSelector != nil && to.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] ==
				"kube-system" && to.PodSelector != nil && to.PodSelector.MatchLabels["k8s-app"] == "kube-dns":
				a.dns = true
			case to.NamespaceSelector != nil && to.PodSelector != nil &&
				to.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] == egressNamespace:
				a.pods = append(a.pods, to.PodSelector.MatchLabels)
			default:
				t.Fatalf("the policy admits a destination of no shape the attachment renders: %+v", to)
			}
		}
	}
	slices.Sort(a.ranges)
	return a
}

// TestAnImportAskingForTheOutboundOnlyFeatureAttachesItToThePlatform imports the
// committed real export Langflow 1.6.0's Basic Prompting, its Language Model
// referenced as core/ai-model/openai through a mapping override, with
// attach_egress_allowlist true:
//
//   - the platform's instance declares exactly one attachment, egress-allowlist,
//     of type core/egress-allowlist, carrying nothing else — no infra, so no
//     affirmation and no address range;
//   - the comment above the instance's entry of the drafted Release names, for
//     the Release, the affirmation, the address ranges and the namespace the
//     attachment's realization halts without, the entry writes no key of the
//     attachment — the affirmation is a boolean and the ranges a list, so an empty
//     string at either would be no value — and the Aggregate names none of it;
//   - the import reports the attachment once, as information, and the wired model
//     part's decision says the attachment admits its connection;
//   - the same import with the setting false drafts no attachment and reports none.
//
// 🔴 PLANTED DEFECT, rebuilt into the importer fixtures from its source: in
// parseSettings (aggrega-pdk/go/pkg/importer/settings.go), add
// `s.AttachEgressAllowlist = false` before `return s`: the import drafts no
// attachment and this test fails.
func TestAnImportAskingForTheOutboundOnlyFeatureAttachesItToThePlatform(t *testing.T) {
	t.Parallel()
	const part = "languagemodelcomponent-kboja"
	for _, on := range []bool{true, false} {
		t.Run(map[bool]string{true: "true", false: "false"}[on], func(t *testing.T) {
			t.Parallel()
			settings := referencingLanguageModel()
			settings["attach_egress_allowlist"] = on
			rw := hubPlatformWorkspace(t, settings)
			dir := filepath.Join(rw.Root(), "drafts")
			res := importRealExport(t, rw, langflowSelectingProject, authoredcontent.DeliveryFor(false), dir)
			if res.Halted() {
				t.Fatalf("the import halted: %+v", res.Diagnostics)
			}
			text := readDraftedAggregate(t, dir)
			readAsTheCompilerDoes(t, text)
			features := featuresOf(t, text, "langflow")
			reported := diagnosticsCoded(res.Diagnostics, "feature-attached")
			rationale := decisionNamed(t, res.Decisions, selectingNodeID).Rationale
			clause := "; the egress-allowlist attachment of langflow admits its connection to " + part

			if !on {
				if len(features) != 0 || len(reported) != 0 || strings.Contains(rationale, clause) ||
					strings.Contains(text, "egress-allowlist") {
					t.Errorf("with the setting false the draft attaches %v, reports %q and decides %q", features, reported,
						rationale)
				}
				return
			}
			want := map[string]map[string]any{"egress-allowlist": {"type": "core/egress-allowlist"}}
			if !reflect.DeepEqual(features, want) {
				t.Errorf("the platform declares the features %v, want exactly %v", features, want)
			}
			release := readDraftedRelease(t, dir)
			above := "    # Left for the Release, since the source cannot supply it:\n" +
				"    #   - features.egress-allowlist.infra.control_plane_present, true, the affirmation that the " +
				"cluster's network plugin enforces network policy, which only the operator gives and without which the " +
				"build halts\n" +
				"    #   - features.egress-allowlist.infra.admitted_destinations, a list holding, for each connection " +
				"whose target the cluster does not run, such as a hosted model, an entry naming that target and an " +
				"address range, without which the build halts naming that connection\n" +
				"    #   - aggrega.ai/namespace on the boundary holding langflow, where the target emits its " +
				"workloads into no namespace: the attachment's network policy is placed in its workload's " +
				"namespace, and the build halts without one\n    langflow:\n"
			if !strings.Contains(release, above) {
				t.Errorf("the drafted Release does not leave for the Release\n%s\nit reads\n%s", above, release)
			}
			// The affirmation is a boolean and the ranges a list: an empty string at
			// either is neither a value owed nor a valid one, so neither key is written.
			for _, line := range strings.Split(release, "\n") {
				if key := strings.TrimSpace(line); strings.HasPrefix(key, "control_plane_present:") ||
					strings.HasPrefix(key, "admitted_destinations:") || strings.HasPrefix(key, "egress-allowlist:") {
					t.Errorf("the drafted Release writes %q; the import drafts nothing of the attachment's "+
						"infrastructure:\n%s", line, release)
				}
			}
			if strings.Contains(text, "Left for the Release") {
				t.Errorf("the drafted Aggregate names what the Release supplies; the Release's entry does:\n%s", text)
			}
			wantReported := []string{"langflow carries core/egress-allowlist, attached as egress-allowlist, which lets " +
				"it connect only to the components it is wired to; the import sets none of its infrastructure: the " +
				"Release sets infra.control_plane_present on that attachment, without which the build halts, and " +
				"infra.admitted_destinations for each connection whose target the cluster does not run"}
			if !slices.Equal(reported, wantReported) {
				t.Errorf("the import reported %q, want %q", reported, wantReported)
			}
			if !strings.HasSuffix(rationale, clause) {
				t.Errorf("the model part's decision reads %q, want it to end %q", rationale, clause)
			}
		})
	}
}

// TestADraftAttachingTheOutboundOnlyFeatureBuildsOnHelmOnceTheReleaseSetsIt
// imports Langflow 1.6.0's Basic Prompting as the case above does, the setting
// true, and builds the draft on helm with Releases that attach nothing new — each
// sets, at most, the infra of the attachment the draft declares, and a namespace
// on its boundary — and give the platform its image, its administrator's secret
// and the model connection's credential:
//
//   - setting nothing of the attachment, the build halts naming the affirmation,
//     which the import never sets;
//   - affirming, with no namespace, it halts naming aggrega.ai/namespace;
//   - affirming with a namespace and no address range, it halts naming the
//     connection to the hosted model;
//   - given all three, it builds, and the one policy, in the namespace given,
//     selects the platform's pods and admits exactly the platform's own pods, the
//     cluster's DNS and the one range the Release lists for the one drafted
//     connection, whose target — a hosted model — runs no pods.
//
// 🔴 PLANTED DEFECT, rebuilt into the importer fixtures from its source: in
// (*platformDraft).entity (aggrega-pdk/go/pkg/importer/platform.go), replace
// `b.WriteString("          type: " + egressAllowlist + "\n")` with
// `b.WriteString("          type: " + egressAllowlist + "\n          infra:\n" +
// "            control_plane_present: true\n")`: the draft no longer reads as an
// Aggregate's attachment, and the import case above and this one fail.
func TestADraftAttachingTheOutboundOnlyFeatureBuildsOnHelmOnceTheReleaseSetsIt(t *testing.T) {
	t.Parallel()
	const part = "languagemodelcomponent-kboja"
	for _, tc := range []struct {
		name    string
		release egressRelease
		// halts is a phrase the build's refusal carries; empty where it builds.
		halts string
	}{
		{name: "nothing set", release: egressRelease{namespace: true}, halts: "infra.control_plane_present"},
		{name: "no namespace", release: egressRelease{affirm: true, ranges: map[string]string{part: egressModelRange}},
			halts: "set aggrega.ai/namespace on the boundary holding \"langflow\""},
		{name: "no range", release: egressRelease{affirm: true, namespace: true},
			halts: "\"langflow\" connects to \"" + part + "\" through its connection \"" + part + "\""},
		{name: "all set", release: egressRelease{affirm: true, namespace: true,
			ranges: map[string]string{part: egressModelRange}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rw := hubPlatformWorkspace(t, egressAttachSettings(referencingLanguageModel()))
			dir := filepath.Join(rw.Root(), "drafts")
			if res := importRealExport(t, rw, langflowSelectingProject, authoredcontent.DeliveryFor(false), dir); res.Halted() {
				t.Fatalf("the import halted: %+v", res.Diagnostics)
			}
			release := withEgress(t, dir, writePlatformRelease(t, dir, platformRelease{platform: "langflow", target: "helm",
				parts: []string{part}, model: part}), "langflow", tc.release)
			result, err := pipelinetest.BuildE(t, pipelinetest.Input{ReleasePath: release,
				CatalogPaths: egressBuildPaths(t, platformBuildPaths(t))})
			if tc.halts != "" {
				if err == nil {
					t.Fatalf("the build succeeded; want it halted naming %q", tc.halts)
				}
				if chain := cerrors.CauseChain(err); !strings.Contains(chain, tc.halts) {
					t.Fatalf("the build halted with\n%s\nwant a refusal naming %q", chain, tc.halts)
				}
				return
			}
			if err != nil {
				t.Fatalf("the build halted: %s", cerrors.CauseChain(err))
			}
			p := renderedPolicy(t, result.Artifacts, "langflow")
			if p.Metadata.Namespace != egressNamespace || !slices.Equal(p.Spec.PolicyTypes, []string{"Egress"}) {
				t.Errorf("the policy sits in %q restricting %v, want %q restricting Egress", p.Metadata.Namespace,
					p.Spec.PolicyTypes, egressNamespace)
			}
			got := admittedBy(t, p)
			if !reflect.DeepEqual(got.pods, []map[string]string{p.Spec.PodSelector.MatchLabels}) {
				t.Errorf("the policy admits the pods %v, want the platform's own %v alone", got.pods,
					p.Spec.PodSelector.MatchLabels)
			}
			if !got.dns || !slices.Equal(got.ranges, []string{egressModelRange}) {
				t.Errorf("the policy admits DNS %v and the ranges %v, want DNS and %s alone", got.dns, got.ranges,
					egressModelRange)
			}
		})
	}
}

// TestTheOutboundOnlyPolicyAdmitsExactlyTheDraftedConnectionsTargets imports the
// committed n8n agent workflow — a hosted model, an MCP server reached over HTTP
// and a Qdrant store, each wired to the platform — with every HIGH match
// referenced and the setting true, and builds it on helm with a Release setting
// the attachment's affirmation, a range for each connection whose target the
// cluster does not run — the model and the MCP server — and a namespace. The one
// policy admits the platform's own pods, the store's pods — the one target the
// cluster runs, selected by its own labels in the namespace given — the cluster's
// DNS and the two ranges, and nothing else.
func TestTheOutboundOnlyPolicyAdmitsExactlyTheDraftedConnectionsTargets(t *testing.T) {
	t.Parallel()
	rw := n8nWorkspace(t, egressAttachSettings(referencing()))
	res, dir := writtenImport(t, rw, "agent-workflow.json", readN8nWorkflow(t, n8nAgentWorkflow))
	if res.Halted() {
		t.Fatalf("the import halted: %+v", res.Diagnostics)
	}
	ranges := map[string]string{"openai-chat-model": egressModelRange, "docs-mcp": "198.51.100.7/32"}
	release := withEgress(t, dir, writeN8nRelease(t, dir, "helm"), "n8n",
		egressRelease{affirm: true, namespace: true, ranges: ranges})
	result := pipelinetest.Build(t, pipelinetest.Input{ReleasePath: release,
		CatalogPaths: egressBuildPaths(t, n8nBuildPaths(t))})

	got := admittedBy(t, renderedPolicy(t, result.Artifacts, "n8n"))
	if !got.dns || !slices.Equal(got.ranges, []string{"198.51.100.7/32", egressModelRange}) {
		t.Errorf("the policy admits DNS %v and the ranges %v, want DNS and the two ranges listed", got.dns, got.ranges)
	}
	if len(got.pods) != 2 {
		t.Fatalf("the policy admits %d sets of pods %v, want the platform's own and the store's", len(got.pods), got.pods)
	}
	store := 0
	for _, labels := range got.pods {
		for _, v := range labels {
			if v == "knowledge-base" {
				store++
				break
			}
		}
	}
	if store != 1 {
		t.Errorf("the policy admits the pods %v, want exactly one set selecting the store knowledge-base", got.pods)
	}
}

// TestEveryCommittedSourceDraftsAsBeforeWhereTheAttachSettingIsNotTrue imports
// every committed source of the three formats, naming no format, with
// attach_egress_allowlist absent, false, a string and a number, under the default
// layout and under layout nodes, and holds every result — drafts, decisions and
// diagnostics, byte for byte — to the same import with no such setting.
//
// 🔴 PLANTED DEFECT, rebuilt into the importer fixtures from its source: in
// parseSettings (aggrega-pdk/go/pkg/importer/settings.go), add
// `s.AttachEgressAllowlist = true` before `return s`: every row fails.
func TestEveryCommittedSourceDraftsAsBeforeWhereTheAttachSettingIsNotTrue(t *testing.T) {
	t.Parallel()
	sources := []string{n8nAgentWorkflow, n8nHTTPWorkflow, "../compiler/importersocket/testdata/langflow-flow.json",
		"../compiler/importersocket/testdata/langflow-flow-b.json",
		"../compiler/importersocket/testdata/flowise-flow.json"}
	for _, rel := range []string{langflowStarterProject, langflowPromptingProject, langflowSelectingProject,
		langflowRemixProject, flowiseChatflow, flowiseAgentflow} {
		sources = append(sources, realExportPath(rel))
	}
	for _, layout := range []map[string]any{nil, {"layout": "nodes"}} {
		baseline := map[string]Result{}
		rw, _ := catalogFolderImportWorkspaceAttaching(t, layout)
		for _, src := range sources {
			baseline[src] = importFlow(t, rw, src)
		}
		for _, value := range []any{false, "yes", 1} {
			settings := map[string]any{"attach_egress_allowlist": value}
			for k, v := range layout {
				settings[k] = v
			}
			rw, _ := catalogFolderImportWorkspaceAttaching(t, settings)
			for _, src := range sources {
				got := importFlow(t, rw, src)
				a, _ := json.Marshal([]any{baseline[src].Files, baseline[src].Decisions, baseline[src].Diagnostics})
				b, _ := json.Marshal([]any{got.Files, got.Decisions, got.Diagnostics})
				if string(a) != string(b) {
					t.Errorf("%s with attach_egress_allowlist %#v and layout %v drafts differently from no setting",
						filepath.Base(src), value, layout)
				}
			}
		}
	}
}
