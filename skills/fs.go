package skills

import (
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	// startHere is listed first by FSSource, ahead of the alphabetical rest: the
	// orientation skill every one of the existing implementations names.
	startHere = "start-here"
	// maxDescriptionRunes caps a description FSSource derives from a body, so a
	// file that opens with a long paragraph does not bloat the catalog.
	maxDescriptionRunes = 200
)

type fsSource struct {
	metas  []Meta
	bodies map[string]string
}

// FSSource serves the *.md files at the top level of fsys, one skill per file,
// named after the file without the ".md" suffix. Subdirectories, other
// extensions and anything that is not a regular file are ignored. The catalog
// lists "start-here" first, then the rest in name order.
//
// The description is the first non-blank line of the file with any leading
// markdown heading marks removed (or the skill's name if that leaves nothing),
// cut to 200 runes. FSSource does not parse frontmatter: a file that opens with
// one is served verbatim and its description is that first line. A caller that
// needs authored metadata implements Source itself.
//
// Every file is read once, here, so a bad file system fails at startup and
// List and Get never touch fsys again. An fsys with no skills is an error: a
// skills tool with nothing to say is a wiring mistake. Pass an embed.FS; an
// os.DirFS follows symlinks out of its directory.
func FSSource(fsys fs.FS) (Source, error) {
	if fsys == nil {
		return nil, errors.New("skills: FSSource needs a file system")
	}
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("skills: read skill directory: %w", err)
	}
	s := &fsSource{bodies: map[string]string{}}
	for _, e := range entries {
		file := e.Name()
		name, ok := strings.CutSuffix(file, ".md")
		if !ok || name == "" || !e.Type().IsRegular() {
			continue
		}
		data, err := fs.ReadFile(fsys, file)
		if err != nil {
			return nil, fmt.Errorf("skills: read %s: %w", file, err)
		}
		body := string(data)
		s.bodies[name] = body
		s.metas = append(s.metas, Meta{Name: name, Description: describe(name, body)})
	}
	if len(s.metas) == 0 {
		return nil, errors.New("skills: no *.md skill files at the top level")
	}
	sort.Slice(s.metas, func(i, j int) bool {
		a, b := s.metas[i].Name, s.metas[j].Name
		if (a == startHere) != (b == startHere) {
			return a == startHere
		}
		return a < b
	})
	return s, nil
}

func (s *fsSource) List() ([]Meta, error) {
	return append([]Meta(nil), s.metas...), nil
}

// Get looks name up in what was read at construction. The name is never used
// as a path, so "../x" or "a/b" simply are not skills.
func (s *fsSource) Get(name string) (string, error) {
	body, ok := s.bodies[name]
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	return body, nil
}

func describe(name, body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.TrimSpace(strings.TrimLeft(line, "#"))
		if line == "" {
			break
		}
		if utf8.RuneCountInString(line) > maxDescriptionRunes {
			line = string([]rune(line)[:maxDescriptionRunes-1]) + "…"
		}
		return line
	}
	return name
}
