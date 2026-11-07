package check

import (
	"context"
	"slices"
	"testing"

	"github.com/dpkingii/relay-authz/internal/rules"
)

// TestClinicExample keeps the README's clinic walkthrough true.
func TestClinicExample(t *testing.T) {
	rs, err := rules.Load("../../examples/clinic.yaml")
	if err != nil {
		t.Fatal(err)
	}
	ts := tuples(t,
		"patient:p1#self@user:pat",
		"patient:p1#primary@user:dr_kim",
		"patient:p1#care_team@team:cardiology",
		"team:cardiology#member@user:nurse_ana",
		"patient:p1#referred@user:dr_lee",
		"record:p1_echo#patient@patient:p1",
		"record:p1_echo#author@user:dr_cho",
		"record:p2_labs#patient@patient:p2",
	)
	for _, tup := range ts {
		if err := rs.Validate(tup); err != nil {
			t.Fatal(err)
		}
	}
	c := Checker{Rules: rs, Reader: ts}
	tests := []struct {
		relation, user, record string
		want                   bool
	}{
		{"viewer", "user:pat", "record:p1_echo", true},
		{"viewer", "user:nurse_ana", "record:p1_echo", true},
		{"viewer", "user:nurse_ana", "record:p2_labs", false},
		{"viewer", "user:dr_lee", "record:p1_echo", true},
		{"editor", "user:dr_lee", "record:p1_echo", false},
		{"editor", "user:dr_kim", "record:p1_echo", true},
		{"editor", "user:dr_cho", "record:p1_echo", true},
		{"editor", "user:pat", "record:p1_echo", false},
	}
	for _, tc := range tests {
		got, err := c.Check(context.Background(), object(t, tc.record), tc.relation, object(t, tc.user))
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("Check(%s#%s@%s) = %v, want %v", tc.record, tc.relation, tc.user, got, tc.want)
		}
	}

	got, err := Lookup{Rules: rs, Reader: ts}.Objects(context.Background(), "patient", "viewer", object(t, "user:nurse_ana"))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"patient:p1"}; !slices.Equal(ids(got), want) {
		t.Errorf("patients nurse_ana can view = %v, want %v", ids(got), want)
	}
}
