package selector

import (
	"testing"
)

func TestParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		wantCount int
		wantErr   bool
	}{
		{"empty", "", 0, false},
		{"simple equality", "key=value", 1, false},
		{"not equals", "key!=value", 1, false},
		{"in set", "key in (a,b,c)", 1, false},
		{"notin set", "key notin (x,y)", 1, false},
		{"exists", "key", 1, false},
		{"does not exist", "!key", 1, false},
		{"two requirements", "a=1, b!=2", 2, false},
		{"three mixed", "type=server, env in (prod,staging), !deprecated", 3, false},
		{"spaces", "  key = value  ", 1, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reqs, err := Parse(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(reqs) != tt.wantCount {
				t.Errorf("expected %d requirements, got %d", tt.wantCount, len(reqs))
			}
		})
	}
}

func TestMatchString(t *testing.T) {
	t.Parallel()

	labels := map[string]string{
		"component.aggrega.ai/type":       "stateful-server",
		"compliance.aggrega.ai/data-class": "restricted",
		"datastore.aggrega.ai/engine":     "relational",
	}

	tests := []struct {
		name     string
		selector string
		labels   map[string]string
		want     bool
		wantErr  bool
	}{
		{
			name:     "empty selector matches all",
			selector: "",
			labels:   labels,
			want:     true,
		},
		{
			name:     "single equality match",
			selector: "component.aggrega.ai/type=stateful-server",
			labels:   labels,
			want:     true,
		},
		{
			name:     "single equality no match",
			selector: "component.aggrega.ai/type=server",
			labels:   labels,
			want:     false,
		},
		{
			name:     "AND both match",
			selector: "component.aggrega.ai/type=stateful-server, compliance.aggrega.ai/data-class=restricted",
			labels:   labels,
			want:     true,
		},
		{
			name:     "AND one fails",
			selector: "component.aggrega.ai/type=stateful-server, compliance.aggrega.ai/data-class=public",
			labels:   labels,
			want:     false,
		},
		{
			name:     "not equals match",
			selector: "compliance.aggrega.ai/data-class!=public",
			labels:   labels,
			want:     true,
		},
		{
			name:     "not equals no match",
			selector: "compliance.aggrega.ai/data-class!=restricted",
			labels:   labels,
			want:     false,
		},
		{
			name:     "in set match",
			selector: "datastore.aggrega.ai/engine in (relational, vector)",
			labels:   labels,
			want:     true,
		},
		{
			name:     "in set no match",
			selector: "datastore.aggrega.ai/engine in (vector, graph)",
			labels:   labels,
			want:     false,
		},
		{
			name:     "notin set match",
			selector: "datastore.aggrega.ai/engine notin (vector, graph)",
			labels:   labels,
			want:     true,
		},
		{
			name:     "notin set no match",
			selector: "datastore.aggrega.ai/engine notin (relational, graph)",
			labels:   labels,
			want:     false,
		},
		{
			name:     "exists match",
			selector: "component.aggrega.ai/type",
			labels:   labels,
			want:     true,
		},
		{
			name:     "exists no match",
			selector: "nonexistent.label",
			labels:   labels,
			want:     false,
		},
		{
			name:     "does not exist match",
			selector: "!nonexistent.label",
			labels:   labels,
			want:     true,
		},
		{
			name:     "does not exist no match",
			selector: "!component.aggrega.ai/type",
			labels:   labels,
			want:     false,
		},
		{
			name:     "not equals missing key matches",
			selector: "missing.key!=anything",
			labels:   labels,
			want:     true,
		},
		{
			name:     "notin missing key matches",
			selector: "missing.key notin (a,b)",
			labels:   labels,
			want:     true,
		},
		{
			name:     "in missing key does not match",
			selector: "missing.key in (a,b)",
			labels:   labels,
			want:     false,
		},
		{
			name:     "nil labels empty selector",
			selector: "",
			labels:   nil,
			want:     true,
		},
		{
			name:     "nil labels exists fails",
			selector: "key",
			labels:   nil,
			want:     false,
		},
		{
			name:     "nil labels does not exist matches",
			selector: "!key",
			labels:   nil,
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := MatchString(tt.selector, tt.labels)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("MatchString(%q) = %v, want %v", tt.selector, got, tt.want)
			}
		})
	}
}
