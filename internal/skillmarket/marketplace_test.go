package skillmarket

import (
	"testing"
)

func TestNewMarketplace(t *testing.T) {
	m := NewMarketplace()
	if m == nil {
		t.Error("Expected marketplace to be created")
	}
}

func TestPublish(t *testing.T) {
	m := NewMarketplace()
	skill := Skill{
		Name: "Test Skill",
		Description: "Test description",
		Author: "Test Author",
	}
	
	m.Publish(skill)
	
	if len(m.skills) != 1 {
		t.Errorf("Expected 1 skill, got %d", len(m.skills))
	}
	if m.skills[0].ID == "" {
		t.Error("Expected skill ID to be generated")
	}
}

func TestSearch(t *testing.T) {
	m := NewMarketplace()
	m.Publish(Skill{Name: "Go Patterns", Description: "Go best practices"})
	m.Publish(Skill{Name: "Python Tips", Description: "Python tips"})
	
	results := m.Search("go")
	if len(results) != 1 {
		t.Errorf("Expected 1 result, got %d", len(results))
	}
}

func TestDownload(t *testing.T) {
	m := NewMarketplace()
	m.Publish(Skill{Name: "Test"})
	skillID := m.skills[0].ID
	
	m.Download(skillID)
	
	if m.skills[0].Downloads != 1 {
		t.Errorf("Expected 1 download, got %d", m.skills[0].Downloads)
	}
}
