// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package rendercore

import (
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// injectMapping builds an inject mapping from rows of mechanism, delivered name
// and provides key, in the order given.
func injectMapping(rows ...[3]string) types.OrderedMap[types.OrderedMap[string]] {
	var out types.OrderedMap[types.OrderedMap[string]]
	for _, r := range rows {
		keys, _ := out.Get(r[0])
		keys.Set(r[1], r[2])
		out.Set(r[0], keys)
	}
	return out
}

func TestLinkInjectedKeys(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		inject types.OrderedMap[types.OrderedMap[string]]
		want   []string
	}{
		{"no inject mapping", injectMapping(), []string{}},
		{"one env entry", injectMapping([3]string{"env", "DB_HOST", "host"}), []string{"host"}},
		{
			"every mechanism, sorted, each key once",
			injectMapping(
				[3]string{"env", "DB_PORT", "port"},
				[3]string{"env", "DB_HOST", "host"},
				[3]string{"file", "/etc/db/host", "host"},
				[3]string{"arg", "--user", "user"},
				[3]string{"ssm_ref", "/db/endpoint", "endpoint"},
			),
			[]string{"endpoint", "host", "port", "user"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := LinkInjectedKeys(LinkEdge{Inject: c.inject})
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("LinkInjectedKeys = %#v, want %#v", got, c.want)
			}
		})
	}
}
