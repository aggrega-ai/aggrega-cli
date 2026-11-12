// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package helmcore

import (
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// envOrderEntry is one rendered env entry: a plain entry carries its value, a
// Secret-read entry the Secret and data key its secretKeyRef names.
type envOrderEntry struct {
	name      string
	value     string
	secretRef string
}

// envOrderMount is one rendered volumeMounts entry.
type envOrderMount struct {
	name      string
	mountPath string
	readOnly  bool
}

// TestSynthesizeWorkload_EnvOrderSecretsFirst pins the order a synthesized
// container writes its env in: every attachment delivered `as: env` (a
// secretKeyRef entry) first, then every plain variable, each kind in authored
// order, with envFrom sources kept in their own envFrom list. Kubernetes
// expands a `$(NAME)` reference in an env value from the entries listed before
// it, so this order is what lets a plain value compose a Secret-read value by
// reference.
//
// The fixture authors the variables and the env-mode attachments each out of
// alphabetical order, with a file-mode attachment between the two env-mode
// ones, so a sort, a file entry leaking into env, or the variables written
// first each fail a row. Every row also carries a plain value referencing the
// DB_PASSWORD Secret-read entry, which must render verbatim at an index after
// that entry, and asserts the container's volumeMounts: the file-mode
// attachment's mount ahead of the declared one.
//
// Entries are read from the rendered YAML node tree in document order, never
// through a Go map, because order is the property under test.
func TestSynthesizeWorkload_EnvOrderSecretsFirst(t *testing.T) {
	t.Parallel()

	const databaseURL = "postgres://u:$(DB_PASSWORD)@db:5432/app"

	nodeCtx := nodeCtxFor("api")
	attachmentProvides := map[string]types.OrderedMap[string]{
		"db-credentials":  providesBag("db-creds-secret", "password"),
		"api-credentials": providesBag("api-secret", "token"),
		"tls":             providesBag("tls-bundle", "tls.crt"),
	}

	// fixture is the container under test: three plain variables and two
	// env-mode attachments, each kind authored out of alphabetical order, a
	// file-mode attachment between the two env-mode ones, and one declared
	// volume mount.
	fixture := func() types.ContainerSpec {
		c := types.ContainerSpec{Image: "app:1.0"}
		c.Variables.Set("ZEBRA", "z")
		c.Variables.Set("DATABASE_URL", databaseURL)
		c.Variables.Set("ALPHA", "a")
		c.Attachments.Set("DB_PASSWORD", types.AttachmentRef{From: "db-credentials", As: "env"})
		c.Attachments.Set("tls-cert", types.AttachmentRef{From: "tls", As: "file", Path: "/etc/tls/cert.pem"})
		c.Attachments.Set("API_TOKEN", types.AttachmentRef{From: "api-credentials", As: "env"})
		c.VolumeMounts = []types.VolumeMount{{Name: "data", Path: "/data"}}
		return c
	}
	newWorkload := func() *types.Workload {
		w := &types.Workload{APIVersion: "aggrega.ai/v1beta1", Kind: "Workload"}
		w.Volumes.Set("data", types.VolumeSpec{ConfigMap: &types.ConfigMapVolume{Source: "app-config"}})
		return w
	}

	// The rule, written out literally: Secret-read entries in authored order,
	// then plain variables in authored order.
	wantEnv := []envOrderEntry{
		{name: "DB_PASSWORD", secretRef: "db-creds-secret/password"},
		{name: "API_TOKEN", secretRef: "api-secret/token"},
		{name: "ZEBRA", value: "z"},
		{name: "DATABASE_URL", value: databaseURL},
		{name: "ALPHA", value: "a"},
	}
	wantMounts := []envOrderMount{
		{name: secretVolumeName(synthesizedNameBasis(nodeCtx), "tls"), mountPath: "/etc/tls/cert.pem", readOnly: true},
		{name: "data", mountPath: "/data"},
	}

	tests := []struct {
		name        string
		workload    func() *types.Workload
		podField    string // the pod-spec list the container under test sits in
		wantEnvFrom []string
	}{
		{
			name: "main container",
			workload: func() *types.Workload {
				w := newWorkload()
				w.Containers.Set("main", fixture())
				return w
			},
			podField: "containers",
		},
		{
			name: "init container",
			workload: func() *types.Workload {
				w := newWorkload()
				w.Containers.Set("main", types.ContainerSpec{Image: "app:1.0"})
				migrate := fixture()
				migrate.Name = "migrate"
				w.InitContainers = append(w.InitContainers, migrate)
				return w
			},
			podField: "initContainers",
		},
		{
			name: "envFrom sources stay in envFrom",
			workload: func() *types.Workload {
				w := newWorkload()
				c := fixture()
				c.EnvFrom = []types.EnvFromSource{
					{ConfigMap: "app-config"},
					{Secret: "db-credentials", Prefix: "DB_"},
				}
				w.Containers.Set("main", c)
				return w
			},
			podField:    "containers",
			wantEnvFrom: []string{"configMapRef app-config", "secretRef db-creds-secret prefix DB_"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			files, err := SynthesizeWorkloadCore(tc.workload(), attachmentProvides, nodeCtx)
			if err != nil {
				t.Fatalf("SynthesizeWorkload: %v", err)
			}
			body := files[0].Body
			container := envOrderPodContainer(t, body, tc.podField)

			gotEnv := envOrderEnvEntries(container)
			if !slices.Equal(gotEnv, wantEnv) {
				t.Errorf("env = %+v\nwant %+v\n(Secret-read entries, then plain variables, each in authored order)", gotEnv, wantEnv)
			}

			// The planted reference renders verbatim, never escaped, and after the
			// Secret-read entry it names, so Kubernetes can expand it.
			passwordAt, urlAt := envOrderIndex(gotEnv, "DB_PASSWORD"), envOrderIndex(gotEnv, "DATABASE_URL")
			if passwordAt < 0 || urlAt < 0 || urlAt <= passwordAt {
				t.Errorf("DATABASE_URL at index %d must follow DB_PASSWORD at index %d; env = %+v", urlAt, passwordAt, gotEnv)
			}
			if !strings.Contains(string(body), databaseURL) || strings.Contains(string(body), "$$(") {
				t.Errorf("the plain value %q must render verbatim and unescaped; got:\n%s", databaseURL, body)
			}

			if gotMounts := envOrderMounts(container); !slices.Equal(gotMounts, wantMounts) {
				t.Errorf("volumeMounts = %+v\nwant %+v", gotMounts, wantMounts)
			}

			if gotEnvFrom := envOrderEnvFrom(container); !slices.Equal(gotEnvFrom, tc.wantEnvFrom) {
				t.Errorf("envFrom = %v, want %v", gotEnvFrom, tc.wantEnvFrom)
			}
		})
	}
}

// envOrderPodContainer returns the first entry of the pod template's field list
// (containers or initContainers) in the rendered controller document.
func envOrderPodContainer(t *testing.T, body []byte, field string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("yaml unmarshal: %v\n%s", err, body)
	}
	if len(doc.Content) == 0 {
		t.Fatalf("empty yaml document:\n%s", body)
	}
	podSpec := envOrderChild(envOrderChild(envOrderChild(doc.Content[0], "spec"), "template"), "spec")
	list := envOrderChild(podSpec, field)
	if list == nil || list.Kind != yaml.SequenceNode || len(list.Content) == 0 {
		t.Fatalf("rendered pod spec has no %s:\n%s", field, body)
	}
	return list.Content[0]
}

// envOrderChild returns the value node for key in a mapping node, or nil.
func envOrderChild(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// envOrderScalar returns the scalar value at key in a mapping node, or "".
func envOrderScalar(node *yaml.Node, key string) string {
	if v := envOrderChild(node, key); v != nil {
		return v.Value
	}
	return ""
}

// envOrderEnvEntries reads a container's env list in document order.
func envOrderEnvEntries(container *yaml.Node) []envOrderEntry {
	env := envOrderChild(container, "env")
	if env == nil {
		return nil
	}
	out := make([]envOrderEntry, 0, len(env.Content))
	for _, entry := range env.Content {
		e := envOrderEntry{name: envOrderScalar(entry, "name"), value: envOrderScalar(entry, "value")}
		if ref := envOrderChild(envOrderChild(entry, "valueFrom"), "secretKeyRef"); ref != nil {
			e.secretRef = envOrderScalar(ref, "name") + "/" + envOrderScalar(ref, "key")
		}
		out = append(out, e)
	}
	return out
}

// envOrderMounts reads a container's volumeMounts list in document order.
func envOrderMounts(container *yaml.Node) []envOrderMount {
	mounts := envOrderChild(container, "volumeMounts")
	if mounts == nil {
		return nil
	}
	out := make([]envOrderMount, 0, len(mounts.Content))
	for _, m := range mounts.Content {
		out = append(out, envOrderMount{
			name:      envOrderScalar(m, "name"),
			mountPath: envOrderScalar(m, "mountPath"),
			readOnly:  envOrderScalar(m, "readOnly") == "true",
		})
	}
	return out
}

// envOrderEnvFrom reads a container's envFrom list in document order, one
// "<source kind> <name>[ prefix <prefix>]" string per entry.
func envOrderEnvFrom(container *yaml.Node) []string {
	sources := envOrderChild(container, "envFrom")
	if sources == nil {
		return nil
	}
	var out []string
	for _, s := range sources.Content {
		var desc string
		switch {
		case envOrderChild(s, "configMapRef") != nil:
			desc = "configMapRef " + envOrderScalar(envOrderChild(s, "configMapRef"), "name")
		case envOrderChild(s, "secretRef") != nil:
			desc = "secretRef " + envOrderScalar(envOrderChild(s, "secretRef"), "name")
		}
		if prefix := envOrderScalar(s, "prefix"); prefix != "" {
			desc += " prefix " + prefix
		}
		out = append(out, desc)
	}
	return out
}

// envOrderIndex returns the position of the env entry called name, or -1.
func envOrderIndex(entries []envOrderEntry, name string) int {
	return slices.IndexFunc(entries, func(e envOrderEntry) bool { return e.name == name })
}
