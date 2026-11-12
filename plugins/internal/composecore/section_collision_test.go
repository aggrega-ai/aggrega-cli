// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package composecore

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// volumeFragment is one contributor's compose fragment declaring a service of
// its own plus a top-level volume named `shared-data` with the given body. The
// body is authored as text rather than built from the Volume struct so the test
// exercises the same parse path a real fragment arrives through.
func volumeFragment(node, body string, origin renderer.EmitOrigin) renderer.EmittedFile {
	return renderer.EmittedFile{
		Name:   node + ".compose-fragment.yaml",
		Origin: origin,
		Body: []byte("services:\n  " + node + ":\n    image: app:1.0\n" +
			"volumes:\n  shared-data:\n" + body),
		Intermediate: true,
	}
}

// configFragment is one contributor's compose fragment declaring a service of
// its own plus a top-level config named `shared-settings` with the given inline
// content.
func configFragment(node, content string, origin renderer.EmitOrigin) renderer.EmittedFile {
	return renderer.EmittedFile{
		Name:   node + ".compose-fragment.yaml",
		Origin: origin,
		Body: []byte("services:\n  " + node + ":\n    image: app:1.0\n" +
			"configs:\n  shared-settings:\n    content: |\n      " + content + "\n"),
		Intermediate: true,
	}
}

// assembleTwo runs boundary assembly over exactly two contributing nodes, which
// is the shape every case in this file is about: one name, two authors.
func assembleTwo(alpha, zeta renderer.EmittedFile) (map[string][]byte, error) {
	out, _, err := AssembleBoundaryCore(
		map[string][]renderer.EmittedFile{
			"alpha": {alpha},
			"zeta":  {zeta},
		},
		map[string]map[string]string{"alpha": {}, "zeta": {}},
		nil, nil, "default", 2,
	)
	return out, err
}

// TestComposeAssemble_ConflictingVolumeBodiesHalt covers two contributors
// declaring one volume name with bodies that disagree. Resolving that by
// keeping either body drops the other contributor's declaration from a build
// that would otherwise succeed, so it halts.
//
// The assertions are on what the author needs in order to act: which section,
// which name, and both parties — the person reading the halt did not
// necessarily write either contributor, so a message naming only the name tells
// them nothing about where the two declarations came from.
func TestComposeAssemble_ConflictingVolumeBodiesHalt(t *testing.T) {
	t.Parallel()

	_, err := assembleTwo(
		volumeFragment("alpha", "    driver: local\n    driver_opts:\n      device: /srv/alpha\n", renderer.EmitOriginArtifactsInline),
		volumeFragment("zeta", "    driver: nfs\n    driver_opts:\n      device: /srv/zeta\n", renderer.EmitOriginArtifactsInline),
	)
	if err == nil {
		t.Fatal("assemble accepted two contributors disagreeing about one volume name; want a halt")
	}
	for _, want := range []string{
		"volume",
		`"shared-data"`,
		`node="alpha"`,
		`node="zeta"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("halt does not name %s: %s", want, err.Error())
		}
	}
}

// TestComposeAssemble_ConflictingConfigBodiesHalt is the same case on the
// configs section, whose entries carry inline content rather than driver
// settings.
func TestComposeAssemble_ConflictingConfigBodiesHalt(t *testing.T) {
	t.Parallel()

	_, err := assembleTwo(
		configFragment("alpha", "alpha-setting: 1", renderer.EmitOriginArtifactsInline),
		configFragment("zeta", "zeta-setting: 2", renderer.EmitOriginArtifactsInline),
	)
	if err == nil {
		t.Fatal("assemble accepted two contributors disagreeing about one config name; want a halt")
	}
	for _, want := range []string{
		"config",
		`"shared-settings"`,
		`node="alpha"`,
		`node="zeta"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("halt does not name %s: %s", want, err.Error())
		}
	}
}

// TestComposeAssemble_CollisionHaltIsStableAcrossRuns pins that the halt is a
// function of the collisions rather than of the order the walk found them in.
// Both sections are exercised with two colliding names each, so the ordering of
// entries and of the parties within an entry both have something to sort.
func TestComposeAssemble_CollisionHaltIsStableAcrossRuns(t *testing.T) {
	t.Parallel()

	two := func(node, driver string) renderer.EmittedFile {
		return renderer.EmittedFile{
			Name:   node + ".compose-fragment.yaml",
			Origin: renderer.EmitOriginArtifactsInline,
			Body: []byte("services:\n  " + node + ":\n    image: app:1.0\n" +
				"volumes:\n  zeta-data:\n    driver: " + driver + "\n  alpha-data:\n    driver: " + driver + "\n"),
			Intermediate: true,
		}
	}

	var first string
	for i := 0; i < 20; i++ {
		_, err := assembleTwo(two("alpha", "local"), two("zeta", "nfs"))
		if err == nil {
			t.Fatal("assemble accepted disagreeing volume bodies; want a halt")
		}
		if first == "" {
			first = err.Error()
			continue
		}
		if err.Error() != first {
			t.Fatalf("halt message is not stable across runs:\n first: %s\nlater: %s", first, err.Error())
		}
	}
	alpha := strings.Index(first, "alpha-data")
	zeta := strings.Index(first, "zeta-data")
	if alpha < 0 || zeta < 0 {
		t.Fatalf("halt names only some colliding volumes: %s", first)
	}
	if alpha > zeta {
		t.Errorf("colliding volumes are not reported in name order: %s", first)
	}
}

// TestComposeAssemble_IdenticalVolumeBodiesMerge is the other half of the rule
// and a separate proof from the halt: a named volume is one physical volume, so
// two contributors declaring it identically have both asked for exactly the
// entry the merged document ends up with, and nothing is lost. The build
// succeeds and the section carries one entry.
func TestComposeAssemble_IdenticalVolumeBodiesMerge(t *testing.T) {
	t.Parallel()

	body := "    driver: local\n    driver_opts:\n      device: /srv/shared\n"
	out, err := assembleTwo(
		volumeFragment("alpha", body, renderer.EmitOriginArtifactsInline),
		volumeFragment("zeta", body, renderer.EmitOriginArtifactsInline),
	)
	if err != nil {
		t.Fatalf("assemble refused two identical volume declarations: %v", err)
	}
	assembled := string(out[AssembledOutputFile])
	if got := strings.Count(assembled, "shared-data:"); got != 1 {
		t.Errorf("volume emitted %d times, want exactly one entry:\n%s", got, assembled)
	}
	for _, want := range []string{"driver: local", "device: /srv/shared"} {
		if !strings.Contains(assembled, want) {
			t.Errorf("merged entry lost %q:\n%s", want, assembled)
		}
	}
}

// TestComposeAssemble_IdenticalConfigBodiesMerge is the same tolerance on the
// configs section. It is the shape the cross-network gateway relies on, where
// more than one contributor can carry the same generated declaration.
func TestComposeAssemble_IdenticalConfigBodiesMerge(t *testing.T) {
	t.Parallel()

	out, err := assembleTwo(
		configFragment("alpha", "shared-setting: 1", renderer.EmitOriginArtifactsInline),
		configFragment("zeta", "shared-setting: 1", renderer.EmitOriginArtifactsInline),
	)
	if err != nil {
		t.Fatalf("assemble refused two identical config declarations: %v", err)
	}
	assembled := string(out[AssembledOutputFile])
	if got := strings.Count(assembled, "shared-settings:"); got != 1 {
		t.Errorf("config emitted %d times, want exactly one entry:\n%s", got, assembled)
	}
	if !strings.Contains(assembled, "shared-setting: 1") {
		t.Errorf("merged entry lost its content:\n%s", assembled)
	}
}

// TestComposeAssemble_BodiesEmittingIdenticallyDoNotConflict pins why the
// comparison is on emitted bytes rather than on the parsed values, and it is
// the guard against the check over-firing.
//
// One contributor writes an empty `driver_opts:` mapping and the other writes
// no `driver_opts:` at all. Those are distinct values in memory — a nil map and
// an empty map — so comparing the parsed structs would report a conflict. But
// the field carries omitempty, so neither emits anything for it: the two
// declarations produce the same bytes and describe the same volume, and
// refusing them would refuse a build in which nothing is lost.
func TestComposeAssemble_BodiesEmittingIdenticallyDoNotConflict(t *testing.T) {
	t.Parallel()

	out, err := assembleTwo(
		volumeFragment("alpha", "    driver: local\n    driver_opts: {}\n", renderer.EmitOriginArtifactsInline),
		volumeFragment("zeta", "    driver: local\n", renderer.EmitOriginArtifactsInline),
	)
	if err != nil {
		t.Fatalf("assemble refused two declarations that emit identically: %v", err)
	}
	assembled := string(out[AssembledOutputFile])
	if got := strings.Count(assembled, "shared-data:"); got != 1 {
		t.Errorf("volume emitted %d times, want exactly one entry:\n%s", got, assembled)
	}
	if strings.Contains(assembled, "driver_opts") {
		t.Errorf("an empty driver_opts reached the emitted document:\n%s", assembled)
	}
}

// TestComposeAssemble_OrdinaryContributorsConflictTheSameWay covers the general
// case rather than the attachment-shaped one it was found through.
//
// Boundary assembly is agnostic to what kind of contributor a node is: its input
// is per-node fragments keyed by node identity, with nothing in that shape
// distinguishing an attached Feature from a component the author declared
// directly. So two ordinary components synthesized from their own workloads
// reach one volume name exactly as two attached Features do, and the same halt
// covers both. The origin here is the workload input kind, which is what an
// ordinary component's own fragment carries.
func TestComposeAssemble_OrdinaryContributorsConflictTheSameWay(t *testing.T) {
	t.Parallel()

	_, err := assembleTwo(
		volumeFragment("alpha", "    driver: local\n", renderer.EmitOriginWorkload),
		volumeFragment("zeta", "    driver: nfs\n", renderer.EmitOriginWorkload),
	)
	if err == nil {
		t.Fatal("assemble accepted two ordinary contributors disagreeing about one volume name; want a halt")
	}
	for _, want := range []string{`"shared-data"`, `node="alpha"`, `node="zeta"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("halt does not name %s: %s", want, err.Error())
		}
	}
}

// TestComposeAssemble_TwoFragmentsOfOneNodeAreTwoContributors pins that every
// body-carrying section is compared per fragment, not per node: two fragments of
// one component — its own fragment and an artifacts: entry — declaring one
// volume, config or model name leave a single entry where they agree and halt
// where they differ, naming both fragments, exactly as two components do. One
// fault per row; each section's identical row is the positive control of its
// differing one.
func TestComposeAssemble_TwoFragmentsOfOneNodeAreTwoContributors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, noun, own, extra string
		wantHalt               bool
	}{
		{"volume, identical", "volume", "volumes:\n  shared:\n    driver: local\n", "volumes:\n  shared:\n    driver: local\n", false},
		{"volume, differing", "volume", "volumes:\n  shared:\n    driver: local\n", "volumes:\n  shared:\n    driver: nfs\n", true},
		{"config, identical", "config", "configs:\n  shared:\n    content: a\n", "configs:\n  shared:\n    content: a\n", false},
		{"config, differing", "config", "configs:\n  shared:\n    content: a\n", "configs:\n  shared:\n    content: b\n", true},
		{"model, identical", "model", "models:\n  shared:\n    model: ai/a\n", "models:\n  shared:\n    model: ai/a\n", false},
		{"model, differing", "model", "models:\n  shared:\n    model: ai/a\n", "models:\n  shared:\n    model: ai/b\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			own := renderer.EmittedFile{
				Name: "alpha.compose-fragment.yaml", Origin: renderer.EmitOriginSource,
				Body: []byte("services:\n  alpha:\n    image: app:1.0\n" + tc.own), Intermediate: true,
			}
			extra := renderer.EmittedFile{
				Name: "extra.yaml", Origin: renderer.EmitOriginArtifactsInline,
				Body: []byte(tc.extra), Intermediate: true,
			}
			var (
				out map[string][]byte
				err error
			)
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("assemble panicked: %v", r)
					}
				}()
				out, _, err = AssembleBoundaryCore(
					map[string][]renderer.EmittedFile{"alpha": {own, extra}},
					map[string]map[string]string{"alpha": {}},
					nil, nil, "default", 2,
				)
			}()
			if !tc.wantHalt {
				if err != nil {
					t.Fatalf("assemble refused two fragments of one node agreeing about one %s: %v", tc.noun, err)
				}
				if got := strings.Count(string(out[AssembledOutputFile]), "  shared:"); got != 1 {
					t.Errorf("%s emitted %d times, want exactly one entry:\n%s", tc.noun, got, out[AssembledOutputFile])
				}
				return
			}
			if err == nil {
				t.Fatalf("assemble accepted two fragments of one node disagreeing about one %s; want a halt", tc.noun)
			}
			want := `conflicting ` + tc.noun + ` declarations across contributors: ` + tc.noun + ` "shared" contributed by ` +
				`[boundary="default" node="alpha" fragment="alpha.compose-fragment.yaml", boundary="default" node="alpha" fragment="extra.yaml"]`
			if !strings.Contains(err.Error(), want) {
				t.Errorf("halt does not name the %s and both fragments:\n want: %s\n  got: %s", tc.noun, want, err.Error())
			}
		})
	}
}

// TestComposeAssemble_ServiceCollisionRefusesIdenticalBodies pins the deliberate
// asymmetry between the sections, so a later change cannot relax services to the
// conflict-only rule on the grounds that the three checks ought to agree.
//
// A compose service is a runtime singleton. Two contributors each declaring a
// service `web` with byte-identical bodies each wanted a container and get one
// between them, so a contributor's intent is lost however identical the two
// definitions are — which is exactly what comparing bodies cannot recover. A
// named volume is the opposite case: two agreeing declarations describe the one
// object both authors asked for.
func TestComposeAssemble_ServiceCollisionRefusesIdenticalBodies(t *testing.T) {
	t.Parallel()

	identical := func(node string) renderer.EmittedFile {
		return renderer.EmittedFile{
			Name:         node + ".compose-fragment.yaml",
			Origin:       renderer.EmitOriginWorkload,
			Body:         []byte("services:\n  web:\n    image: app:1.0\n"),
			Intermediate: true,
		}
	}

	_, err := assembleTwo(identical("alpha"), identical("zeta"))
	if err == nil {
		t.Fatal("assemble accepted two contributors of one service name; want a halt on the name alone")
	}

	// The whole message, not a substring. The service halt and the two
	// body-section halts are worded by one shared reporter, so a change to that
	// reporter can reword this message without any assertion on the sections it
	// was added for noticing. Pinning it in full is what makes the service
	// wording a fixed contract rather than a side effect.
	const want = `[TRANSLATION_ERROR] compose AssembleBoundary: ` +
		`service-name collisions across contributors: ` +
		`service "web" contributed by [boundary="default" node="alpha", boundary="default" node="zeta"]`
	if err.Error() != want {
		t.Errorf("service collision message changed:\n want: %s\n  got: %s", want, err.Error())
	}
}
