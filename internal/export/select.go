package export

import (
	"errors"
	"fmt"
	"strings"
)

// The parts of a report that can be exported separately.
const (
	CategoryHost       = "host"
	CategoryDaemon     = "daemon"
	CategoryCompose    = "compose"
	CategoryContainers = "containers"
	CategoryImages     = "images"
	CategoryNetworks   = "networks"
	CategoryVolumes    = "volumes"
)

// Categories lists every category, in the order used by the export.
var Categories = []string{
	CategoryHost, CategoryDaemon, CategoryCompose,
	CategoryContainers, CategoryImages, CategoryNetworks, CategoryVolumes,
}

// ErrNoCategory is returned when a selection is empty.
var ErrNoCategory = errors.New("no category selected")

// ParseCategories turns comma separated lists ("containers,images") into a
// validated selection, without duplicates and in the canonical order. Blank
// items are ignored. An unknown name is an error: silently exporting less than
// asked would be misleading.
func ParseCategories(values ...string) ([]string, error) {
	want := map[string]bool{}
	for _, v := range values {
		for _, name := range strings.Split(v, ",") {
			name = strings.ToLower(strings.TrimSpace(name))
			if name == "" {
				continue
			}
			if !isCategory(name) {
				return nil, fmt.Errorf("unknown category %q (valid: %s)", name, strings.Join(Categories, ", "))
			}
			want[name] = true
		}
	}
	return inCanonicalOrder(want), nil
}

// Without returns every category except the excluded ones. It fails on an
// unknown name or when nothing would be left.
func Without(excluded ...string) ([]string, error) {
	drop, err := ParseCategories(excluded...)
	if err != nil {
		return nil, err
	}
	keep := map[string]bool{}
	for _, c := range Categories {
		keep[c] = true
	}
	for _, c := range drop {
		delete(keep, c)
	}
	if len(keep) == 0 {
		return nil, ErrNoCategory
	}
	return inCanonicalOrder(keep), nil
}

// Select resolves the -include / -exclude options of the command line into a
// selection. Neither option means everything (nil); both is a mistake.
func Select(include, exclude string) ([]string, error) {
	switch {
	case strings.TrimSpace(include) != "" && strings.TrimSpace(exclude) != "":
		return nil, errors.New("use -include or -exclude, not both")
	case strings.TrimSpace(include) != "":
		cats, err := ParseCategories(include)
		if err != nil {
			return nil, err
		}
		if len(cats) == 0 {
			return nil, ErrNoCategory
		}
		return cats, nil
	case strings.TrimSpace(exclude) != "":
		return Without(exclude)
	default:
		return nil, nil
	}
}

func isCategory(name string) bool {
	for _, c := range Categories {
		if c == name {
			return true
		}
	}
	return false
}

func inCanonicalOrder(set map[string]bool) []string {
	var out []string
	for _, c := range Categories {
		if set[c] {
			out = append(out, c)
		}
	}
	return out
}

// selection is a validated set of categories. A nil selected slice means
// everything.
type selection struct {
	set     map[string]bool
	partial bool
}

func newSelection(selected []string) (selection, error) {
	if selected == nil {
		selected = Categories
	}
	// Re-parse: callers may pass anything.
	cats, err := ParseCategories(strings.Join(selected, ","))
	if err != nil {
		return selection{}, err
	}
	if len(cats) == 0 {
		return selection{}, ErrNoCategory
	}
	set := make(map[string]bool, len(cats))
	for _, c := range cats {
		set[c] = true
	}
	return selection{set: set, partial: len(cats) != len(Categories)}, nil
}

func (s selection) list() []string { return inCanonicalOrder(s.set) }

// filter removes the unselected parts from the decoded report. A full export
// is left untouched, so anything the export does not know about is still
// included; in a partial one, anything not explicitly selected is dropped.
func (s selection) filter(root map[string]any) {
	if !s.partial {
		return
	}
	results, _ := root["results"].(map[string]any)
	for name, value := range results {
		switch name {
		case "host":
			if !s.set[CategoryHost] {
				delete(results, name)
			}
		case "docker":
			docker, _ := value.(map[string]any)
			for key := range docker {
				if !s.set[key] {
					delete(docker, key)
				}
			}
			if len(docker) == 0 {
				delete(results, name)
			}
		default:
			delete(results, name)
		}
	}
}
