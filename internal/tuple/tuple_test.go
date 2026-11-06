package tuple

import "testing"

func TestParseRoundTrip(t *testing.T) {
	for _, s := range []string{
		"document:budget#viewer@user:alice",
		"document:budget#viewer@group:finance#member",
		"folder:q3-reports#parent@folder:2026.finance",
	} {
		got, err := Parse(s)
		if err != nil {
			t.Fatalf("Parse(%q): %v", s, err)
		}
		if got.String() != s {
			t.Errorf("Parse(%q).String() = %q", s, got.String())
		}
	}
}

func TestParseFields(t *testing.T) {
	got, err := Parse("document:budget#viewer@group:finance#member")
	if err != nil {
		t.Fatal(err)
	}
	want := Tuple{
		Object:   Object{Type: "document", ID: "budget"},
		Relation: "viewer",
		Subject:  Subject{Type: "group", ID: "finance", Relation: "member"},
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestParseRejects(t *testing.T) {
	for _, s := range []string{
		"",
		"document:budget#viewer",
		"document:budget@user:alice",
		"document#viewer@user:alice",
		"document:#viewer@user:alice",
		"Document:budget#viewer@user:alice",
		"document:budget#@user:alice",
		"document:budget#viewer@user",
		"document:budget#viewer@user:alice#",
		"document:bud get#viewer@user:alice",
		"document:budget#viewer@user:alice@user:bob",
	} {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", s)
		}
	}
}
