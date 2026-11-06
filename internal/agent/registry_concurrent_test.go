package agent

import (
	"fmt"
	"sync"
	"testing"
)

func TestRegistrySnapshotsStayStableDuringUpdates(t *testing.T) {
	r := NewRegistry()
	r.Register(&Agent{Name: "default", Skills: []string{"read_file"}})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for n := 0; n < 100; n++ {
				if worker%2 == 0 {
					r.Register(&Agent{Name: "default", Skills: []string{fmt.Sprintf("skill_%d", n)}})
				} else {
					a := r.Get("default")
					if a.Name != "default" || len(a.Skills) != 1 {
						t.Errorf("inconsistent agent snapshot: %+v", a)
						return
					}
					a.Skills[0] = "local_change"
				}
			}
		}(i)
	}
	wg.Wait()
	if r.Get("default").Skills[0] == "local_change" {
		t.Fatal("Get returned mutable registry storage")
	}
}

func TestRegistryConcurrentSkillActivation(t *testing.T) {
	r := NewRegistry()
	r.Register(&Agent{Name: "default", Skills: []string{"read_file"}})
	const count = 100
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r.AddSkill("default", fmt.Sprintf("dynamic_%d", i))
		}(i)
	}
	wg.Wait()
	got := r.Get("default")
	if len(got.Skills) != count+1 {
		t.Fatalf("lost concurrent skill activation: got %d, want %d", len(got.Skills), count+1)
	}
	seen := make(map[string]bool, len(got.Skills))
	for _, name := range got.Skills {
		if seen[name] {
			t.Fatalf("duplicate skill %s", name)
		}
		seen[name] = true
	}
	if r.AddSkill("default", "dynamic_0") || len(r.Get("default").Skills) != count+1 {
		t.Fatal("duplicate activation changed the registry")
	}
	if !r.RemoveSkill("default", "dynamic_0") || len(r.Get("default").Skills) != count {
		t.Fatal("skill removal did not update the registry")
	}
}

func TestDynamicSkillsSurviveAgentDefinitionUpdates(t *testing.T) {
	r := NewRegistry()
	r.Register(&Agent{Name: "default", Skills: []string{"read_file"}})
	if !r.AddSkill("default", "learned") {
		t.Fatal("dynamic activation failed")
	}
	r.Register(&Agent{Name: "default", Persona: "updated", Skills: []string{"read_file", "save_memory"}})
	got := r.Get("default")
	if got.Persona != "updated" {
		t.Fatalf("persona update was lost: %q", got.Persona)
	}
	for _, name := range []string{"read_file", "save_memory", "learned"} {
		found := false
		for _, skill := range got.Skills {
			found = found || skill == name
		}
		if !found {
			t.Fatalf("skill %q lost after definition update: %v", name, got.Skills)
		}
	}
	if r.RemoveSkill("default", "read_file") {
		t.Fatal("runtime removal changed a configured skill")
	}
	if !r.RemoveSkill("default", "learned") {
		t.Fatal("dynamic skill removal failed")
	}
	if len(r.Get("default").Skills) != 2 {
		t.Fatalf("unexpected skills after dynamic removal: %v", r.Get("default").Skills)
	}
}
