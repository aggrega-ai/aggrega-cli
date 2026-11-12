// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pullops

import (
	"context"
	"errors"
	"testing"
)

// TestEveryVerifyExclusionIsDecidedByOneRule holds the rule to each pair it
// refuses and to each configuration it must let through.
//
// The rows that let a configuration through are as load-bearing as the refusals:
// a rule that refused every verify would pass every refusal row, so the verify on
// its own, an empty targets list, and every other request made WITHOUT a verify
// are asserted to draw no refusal. The last row holds the declared order, which is
// what makes every surface report the same conflict for a request carrying several.
func TestEveryVerifyExclusionIsDecidedByOneRule(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		name string
		cfg  Config
		// want is the conflict the rule must report, or zero where it must report
		// none.
		want VerifyConflict
	}{
		{name: "a verify on its own asks for nothing else", cfg: Config{Verify: true}},
		{name: "an empty targets list names nothing", cfg: Config{Verify: true, UpgradeTargets: []string{}}},
		{
			name: "every other request at once, without a verify, is no pair",
			cfg:  Config{Upgrade: true, UpgradeTargets: []string{"core/database"}, DryRun: true, PinSources: &yes},
		},
		{name: "a verify beside a bare upgrade", cfg: Config{Verify: true, Upgrade: true}, want: VerifyWithUpgrade},
		{
			name: "a verify beside an upgrade naming targets",
			cfg:  Config{Verify: true, Upgrade: true, UpgradeTargets: []string{"core/database"}},
			want: VerifyWithUpgrade,
		},
		{name: "a verify beside a dry run", cfg: Config{Verify: true, DryRun: true}, want: VerifyWithDryRun},
		{name: "a verify beside the answer to pin sources", cfg: Config{Verify: true, PinSources: &yes}, want: VerifyWithPinSources},
		{name: "a verify beside the answer not to pin sources", cfg: Config{Verify: true, PinSources: &no}, want: VerifyWithPinSources},
		{
			name: "a verify beside targets named without the upgrade switch",
			cfg:  Config{Verify: true, UpgradeTargets: []string{"core/database"}},
			want: VerifyWithUpgradeTargets,
		},
		{
			name: "several at once are refused for the first in the declared order",
			cfg:  Config{Verify: true, DryRun: true, PinSources: &yes, UpgradeTargets: []string{"core/database"}},
			want: VerifyWithDryRun,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.VerifyConflict()
			if tc.want == 0 {
				if err != nil {
					t.Fatalf("a configuration asking a verify for nothing else was refused: %v", err)
				}
				return
			}
			var conflict *VerifyConflictError
			if !errors.As(err, &conflict) {
				t.Fatalf("want the verify-conflict refusal; got %v", err)
			}
			if conflict.With != tc.want {
				t.Errorf("the rule reports conflict %d; want %d", conflict.With, tc.want)
			}
			if conflict.Error() == (&VerifyConflictError{}).Error() {
				t.Errorf("conflict %d carries no sentence of its own: %q", conflict.With, conflict.Error())
			}
		})
	}
}

// TestRunRefusesAVerifyConflictBeforeItReadsAnything holds the run to asking the
// rule first.
//
// The configuration names a Release that does not exist and supplies no workspace,
// and either stops a run that gets past its entry — so a run that read anything
// before the rule would report one of those instead of the conflict. The second
// call is the control: the same configuration without the conflicting request is
// stopped by the missing workspace, which shows the first call's refusal is the
// pair's.
func TestRunRefusesAVerifyConflictBeforeItReadsAnything(t *testing.T) {
	no := false
	_, err := Run(context.Background(), Config{Verify: true, PinSources: &no, ReleasePath: "no-such-release.yaml"})
	var conflict *VerifyConflictError
	if !errors.As(err, &conflict) || conflict.With != VerifyWithPinSources {
		t.Fatalf("a verify beside an answer to pin sources was not refused at the run's entry; got %v", err)
	}

	_, err = Run(context.Background(), Config{Verify: true, ReleasePath: "no-such-release.yaml"})
	if err == nil {
		t.Fatal("the control run with no workspace returned no error, so it cannot show what the first call would have met")
	}
	if errors.As(err, &conflict) {
		t.Fatalf("a verify on its own was refused as a conflict: %v", err)
	}
}
