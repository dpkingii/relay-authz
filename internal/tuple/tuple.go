// Package tuple defines relation tuples and their text form,
// e.g. "document:budget#viewer@user:alice" or "document:budget#viewer@group:finance#member".
package tuple

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	idPattern   = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
)

// Object is a typed resource such as document:budget, user:alice or group:finance.
type Object struct {
	Type string
	ID   string
}

// Subject is who a tuple grants the relation to: a single object (user:alice),
// or everyone with a relation on an object (group:finance#member).
type Subject struct {
	Type     string
	ID       string
	Relation string // empty for a single object
}

type Tuple struct {
	Object   Object
	Relation string
	Subject  Subject
}

func (o Object) String() string { return o.Type + ":" + o.ID }

func (s Subject) String() string {
	if s.Relation == "" {
		return s.Type + ":" + s.ID
	}
	return s.Type + ":" + s.ID + "#" + s.Relation
}

func (t Tuple) String() string {
	return t.Object.String() + "#" + t.Relation + "@" + t.Subject.String()
}

// Parse reads the "type:id#relation@type:id[#relation]" form.
func Parse(s string) (Tuple, error) {
	left, right, ok := strings.Cut(s, "@")
	if !ok {
		return Tuple{}, fmt.Errorf("tuple %q: missing @", s)
	}
	obj, rel, ok := strings.Cut(left, "#")
	if !ok {
		return Tuple{}, fmt.Errorf("tuple %q: missing #relation before @", s)
	}
	object, err := ParseObject(obj)
	if err != nil {
		return Tuple{}, err
	}
	if !namePattern.MatchString(rel) {
		return Tuple{}, fmt.Errorf("tuple %q: bad relation %q", s, rel)
	}
	subject, err := ParseSubject(right)
	if err != nil {
		return Tuple{}, err
	}
	return Tuple{Object: object, Relation: rel, Subject: subject}, nil
}

// ParseObject reads the "type:id" form.
func ParseObject(s string) (Object, error) {
	typ, id, ok := strings.Cut(s, ":")
	if !ok || !namePattern.MatchString(typ) || !idPattern.MatchString(id) {
		return Object{}, fmt.Errorf("object %q: want type:id", s)
	}
	return Object{Type: typ, ID: id}, nil
}

// ParseSubject reads the "type:id" or "type:id#relation" form.
func ParseSubject(s string) (Subject, error) {
	obj, rel, hasRel := strings.Cut(s, "#")
	object, err := ParseObject(obj)
	if err != nil {
		return Subject{}, err
	}
	if hasRel && !namePattern.MatchString(rel) {
		return Subject{}, fmt.Errorf("subject %q: bad relation %q", s, rel)
	}
	return Subject{Type: object.Type, ID: object.ID, Relation: rel}, nil
}
