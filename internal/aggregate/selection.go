package aggregate

import (
	"fmt"
	"sort"

	"github.com/TheGU/awxreport/internal/awx"
)

// ResolveSelection computes the final template ID set for selective mode.
// Exclude rules win over inclusion: matching templates are removed from the
// selection before any data is fetched.
func ResolveSelection(l *awx.Lookups, templateIDs, projectIDs []int, ex ExcludeRules) (selected, removedByExclude []int, err error) {
	if len(templateIDs) == 0 && len(projectIDs) == 0 {
		return nil, nil, nil
	}

	set := make(map[int]struct{})
	for _, id := range templateIDs {
		if _, ok := l.Templates[id]; !ok {
			return nil, nil, fmt.Errorf("template id %d not found on controller", id)
		}
		set[id] = struct{}{}
	}
	for _, pid := range projectIDs {
		matched := false
		for tid, t := range l.Templates {
			if t.Project == pid {
				set[tid] = struct{}{}
				matched = true
			}
		}
		if !matched {
			return nil, nil, fmt.Errorf("project id %d matched no job templates", pid)
		}
	}

	for id := range set {
		t := l.Templates[id]
		if hit, _ := ex.Matches(id, t.Name); hit {
			removedByExclude = append(removedByExclude, id)
			delete(set, id)
		}
	}

	if len(set) == 0 {
		return nil, nil, fmt.Errorf("template selection is empty after applying exclude_templates rules")
	}

	selected = make([]int, 0, len(set))
	for id := range set {
		selected = append(selected, id)
	}
	sort.Ints(selected)
	sort.Ints(removedByExclude)
	return selected, removedByExclude, nil
}
