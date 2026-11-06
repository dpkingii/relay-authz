// Package rules loads the config that says, per object type, which relations exist
// and how they grant each other (Zanzibar's namespace config and userset rewrites).
package rules

import (
	"bytes"
	"fmt"
	"os"

	"go.yaml.in/yaml/v3"

	"github.com/dpkingii/relay-authz/internal/tuple"
)

// Rules maps object type → relation name → how that relation is granted.
type Rules map[string]map[string]Relation

// Relation lists what grants a relation besides its own tuples. Every source is
// unioned, so a user has the relation if any one of them grants it.
type Relation struct {
	// Other relations on the same object that include this one,
	// e.g. viewer implied_by [editor] means every editor is also a viewer.
	ImpliedBy []string `yaml:"implied_by"`
	// Relations on related objects that grant this one, e.g. {tupleset: parent, relation: viewer}
	// means viewers of the object's parent folder are viewers of the object.
	Via []Via `yaml:"via"`
}

type Via struct {
	Tupleset string `yaml:"tupleset"` // relation on this object whose subjects are the related objects
	Relation string `yaml:"relation"` // relation to check on each related object
}

func Load(path string) (Rules, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

func Parse(data []byte) (Rules, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var r Rules
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("rules: %w", err)
	}
	for typ, relations := range r {
		for name, rel := range relations {
			for _, implied := range rel.ImpliedBy {
				if _, ok := relations[implied]; !ok {
					return nil, fmt.Errorf("rules: %s#%s: implied_by %q is not a relation on %s", typ, name, implied, typ)
				}
			}
			for _, via := range rel.Via {
				if _, ok := relations[via.Tupleset]; !ok {
					return nil, fmt.Errorf("rules: %s#%s: tupleset %q is not a relation on %s", typ, name, via.Tupleset, typ)
				}
				if via.Relation == "" {
					return nil, fmt.Errorf("rules: %s#%s: via %q needs a relation", typ, name, via.Tupleset)
				}
			}
		}
	}
	return r, nil
}

func (r Rules) HasType(typ string) bool {
	_, ok := r[typ]
	return ok
}

func (r Rules) HasRelation(typ, relation string) bool {
	_, ok := r[typ][relation]
	return ok
}

// Validate rejects a tuple that names a type or relation the rules don't define,
// so a typo fails loudly instead of being stored and never matching.
func (r Rules) Validate(t tuple.Tuple) error {
	if !r.HasRelation(t.Object.Type, t.Relation) {
		return fmt.Errorf("unknown relation %s#%s", t.Object.Type, t.Relation)
	}
	if !r.HasType(t.Subject.Type) {
		return fmt.Errorf("unknown type %s", t.Subject.Type)
	}
	if t.Subject.Relation != "" && !r.HasRelation(t.Subject.Type, t.Subject.Relation) {
		return fmt.Errorf("unknown relation %s#%s", t.Subject.Type, t.Subject.Relation)
	}
	return nil
}
