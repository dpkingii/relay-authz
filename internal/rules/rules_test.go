package rules

import (
	"strings"
	"testing"

	"github.com/dpkingii/relay-authz/internal/tuple"
)

func TestRepoRulesLoad(t *testing.T) {
	r, err := Load("../../rules.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !r.HasRelation("document", "viewer") || !r.HasType("user") {
		t.Errorf("rules.yaml is missing document#viewer or user: %v", r)
	}
}

func TestParseRejects(t *testing.T) {
	tests := []struct {
		name, yaml, wantErr string
	}{
		{"unknown implied_by", "doc:\n  viewer:\n    implied_by: [editor]\n", `implied_by "editor"`},
		{"unknown tupleset", "doc:\n  viewer:\n    via: [{tupleset: parent, relation: viewer}]\n", `tupleset "parent"`},
		{"via without relation", "doc:\n  parent:\n  viewer:\n    via: [{tupleset: parent}]\n", "needs a relation"},
		{"misspelled field", "doc:\n  viewer:\n    implies: [viewer]\n", "implies"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Parse error = %v, want one mentioning %s", err, tc.wantErr)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	r, err := Parse([]byte("user: {}\ngroup:\n  member:\ndoc:\n  viewer:\n"))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		tuple string
		ok    bool
	}{
		{"doc:a#viewer@user:alice", true},
		{"doc:a#viewer@group:eng#member", true},
		{"doc:a#editor@user:alice", false},
		{"folder:a#viewer@user:alice", false},
		{"doc:a#viewer@bot:alice", false},
		{"doc:a#viewer@group:eng#admin", false},
	}
	for _, tc := range tests {
		tup, err := tuple.Parse(tc.tuple)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Validate(tup); (err == nil) != tc.ok {
			t.Errorf("Validate(%s) = %v, want ok=%v", tc.tuple, err, tc.ok)
		}
	}
}
