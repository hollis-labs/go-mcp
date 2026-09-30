package skills

import (
	"errors"
	"fmt"
)

// Meta is one catalog entry: what a skill is called and what it is for.
type Meta struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ErrNotFound is what Source.Get returns (wrapped) for a name it does not
// have. Register turns it into a "skill_not_found" tool error.
var ErrNotFound = errors.New("skill not found")

// Source is where a skills tool gets its content, and the extension point for
// anything beyond a name, a description and a body. Implementations must be
// safe for concurrent use: tool calls are not serialized.
type Source interface {
	// List returns the catalog in the order it should be shown.
	List() ([]Meta, error)
	// Get returns the body of the named skill, or an error wrapping
	// ErrNotFound when there is none.
	Get(name string) (string, error)
}

type mapSource struct {
	index  []Meta
	bodies map[string]string
}

// MapSource serves index, in that order, as the catalog and bodies as the
// content. Both are copied, so changing the arguments afterwards has no
// effect. A name in index without a body, or a body without an index entry, is
// not an error here: an entry without a body is reported by Register, and a
// body without an entry is simply not listed but can still be fetched by name.
func MapSource(index []Meta, bodies map[string]string) Source {
	m := &mapSource{
		index:  append([]Meta(nil), index...),
		bodies: make(map[string]string, len(bodies)),
	}
	for k, v := range bodies {
		m.bodies[k] = v
	}
	return m
}

func (m *mapSource) List() ([]Meta, error) {
	return append([]Meta(nil), m.index...), nil
}

func (m *mapSource) Get(name string) (string, error) {
	body, ok := m.bodies[name]
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	return body, nil
}
