package aggregate

import (
	"reflect"
	"sort"
	"testing"

	"github.com/TheGU/awxreport/internal/awx"
)

func testLookups() *awx.Lookups {
	return &awx.Lookups{
		Templates: map[int]awx.TemplateLite{
			4:  {ID: 4, Name: "Deploy App", Playbook: "deploy.yml", Project: 1},
			9:  {ID: 9, Name: "Deploy DB", Playbook: "deploy-db.yml", Project: 1},
			12: {ID: 12, Name: "Health Check", Playbook: "health.yml", Project: 2},
			20: {ID: 20, Name: "Smoke Test", Playbook: "smoke.yml", Project: 3},
		},
	}
}

func TestResolveSelection_EmptyInputsReturnFullMode(t *testing.T) {
	l := testLookups()
	selected, removed, err := ResolveSelection(l, nil, nil, NewExcludeRules(nil, nil))
	if err != nil {
		t.Fatalf("ResolveSelection: %v", err)
	}
	if selected != nil || removed != nil {
		t.Errorf("selected=%v removed=%v, want nil, nil (full mode)", selected, removed)
	}
}

func TestResolveSelection_ExplicitTemplateIDs(t *testing.T) {
	l := testLookups()
	selected, removed, err := ResolveSelection(l, []int{9, 4}, nil, NewExcludeRules(nil, nil))
	if err != nil {
		t.Fatalf("ResolveSelection: %v", err)
	}
	if !reflect.DeepEqual(selected, []int{4, 9}) {
		t.Errorf("selected = %v, want [4 9]", selected)
	}
	if len(removed) != 0 {
		t.Errorf("removed = %v, want empty", removed)
	}
}

func TestResolveSelection_ProjectResolvesToTemplates(t *testing.T) {
	l := testLookups()
	selected, _, err := ResolveSelection(l, nil, []int{1}, NewExcludeRules(nil, nil))
	if err != nil {
		t.Fatalf("ResolveSelection: %v", err)
	}
	if !reflect.DeepEqual(selected, []int{4, 9}) {
		t.Errorf("selected = %v, want [4 9]", selected)
	}
}

func TestResolveSelection_UnionOfTemplatesAndProjectsDeduped(t *testing.T) {
	l := testLookups()
	// template 4 is also in project 1; must not appear twice.
	selected, _, err := ResolveSelection(l, []int{4, 20}, []int{1}, NewExcludeRules(nil, nil))
	if err != nil {
		t.Fatalf("ResolveSelection: %v", err)
	}
	if !reflect.DeepEqual(selected, []int{4, 9, 20}) {
		t.Errorf("selected = %v, want [4 9 20]", selected)
	}
}

func TestResolveSelection_UnknownTemplateIDErrors(t *testing.T) {
	l := testLookups()
	_, _, err := ResolveSelection(l, []int{999}, nil, NewExcludeRules(nil, nil))
	if err == nil {
		t.Fatal("expected error for unknown template id")
	}
}

func TestResolveSelection_ProjectWithZeroTemplatesErrors(t *testing.T) {
	l := testLookups()
	_, _, err := ResolveSelection(l, nil, []int{999}, NewExcludeRules(nil, nil))
	if err == nil {
		t.Fatal("expected error for project with no templates")
	}
}

func TestResolveSelection_ExcludeByIDRemovesFromSelection(t *testing.T) {
	l := testLookups()
	ex := NewExcludeRules([]int{9}, nil)
	selected, removed, err := ResolveSelection(l, []int{4, 9}, nil, ex)
	if err != nil {
		t.Fatalf("ResolveSelection: %v", err)
	}
	if !reflect.DeepEqual(selected, []int{4}) {
		t.Errorf("selected = %v, want [4]", selected)
	}
	if !reflect.DeepEqual(removed, []int{9}) {
		t.Errorf("removed = %v, want [9]", removed)
	}
}

func TestResolveSelection_ExcludeByNameContainsRemovesFromSelection(t *testing.T) {
	l := testLookups()
	ex := NewExcludeRules(nil, []string{"health"})
	selected, removed, err := ResolveSelection(l, nil, []int{2, 3}, ex)
	if err != nil {
		t.Fatalf("ResolveSelection: %v", err)
	}
	if !reflect.DeepEqual(selected, []int{20}) {
		t.Errorf("selected = %v, want [20]", selected)
	}
	if !reflect.DeepEqual(removed, []int{12}) {
		t.Errorf("removed = %v, want [12]", removed)
	}
}

func TestResolveSelection_ErrorsWhenEverythingExcluded(t *testing.T) {
	l := testLookups()
	ex := NewExcludeRules([]int{4, 9}, nil)
	_, _, err := ResolveSelection(l, []int{4, 9}, nil, ex)
	if err == nil {
		t.Fatal("expected error when selection is empty after exclude")
	}
}

func TestResolveSelection_OutputSorted(t *testing.T) {
	l := testLookups()
	selected, _, err := ResolveSelection(l, []int{20, 4, 12, 9}, nil, NewExcludeRules(nil, nil))
	if err != nil {
		t.Fatalf("ResolveSelection: %v", err)
	}
	if !sort.IntsAreSorted(selected) {
		t.Errorf("selected = %v, not sorted", selected)
	}
	if !reflect.DeepEqual(selected, []int{4, 9, 12, 20}) {
		t.Errorf("selected = %v, want [4 9 12 20]", selected)
	}
}
