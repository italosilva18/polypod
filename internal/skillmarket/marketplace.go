// Package skillmarket — Skill Marketplace
package skillmarket

import (
	"fmt"
	"strings"
	"time"
)

// Marketplace manages skill sharing
type Marketplace struct {
	skills []Skill
}

// Skill represents a shareable skill
type Skill struct {
	ID          string
	Name        string
	Description string
	Author      string
	Downloads   int
	Rating      float64
	CreatedAt   time.Time
}

// NewMarketplace creates a marketplace
func NewMarketplace() *Marketplace {
	return &Marketplace{
		skills: make([]Skill, 0),
	}
}

// Publish publishes a skill
func (m *Marketplace) Publish(skill Skill) {
	skill.ID = fmt.Sprintf("skill_%d", time.Now().UnixNano())
	skill.CreatedAt = time.Now()
	m.skills = append(m.skills, skill)
}

// Search searches for skills
func (m *Marketplace) Search(query string) []Skill {
	results := make([]Skill, 0)
	for _, skill := range m.skills {
		if contains(skill.Name, query) || contains(skill.Description, query) {
			results = append(results, skill)
		}
	}
	return results
}

// Download downloads a skill
func (m *Marketplace) Download(skillID string) {
	for i := range m.skills {
		if m.skills[i].ID == skillID {
			m.skills[i].Downloads++
			break
		}
	}
}

// Rate rates a skill
func (m *Marketplace) Rate(skillID string, rating float64) {
	for i := range m.skills {
		if m.skills[i].ID == skillID {
			// Simple average (in real implementation, use proper average)
			m.skills[i].Rating = (m.skills[i].Rating + rating) / 2
			break
		}
	}
}

// List lists all skills
func (m *Marketplace) List() []Skill {
	return m.skills
}

// FormatMarketplace formats marketplace for display
func FormatMarketplace(m *Marketplace) string {
	var sb strings.Builder
	sb.WriteString("🎯 **Skill Marketplace**\n\n")
	
	for _, skill := range m.skills {
		sb.WriteString(fmt.Sprintf("### **%s**\n", skill.Name))
		sb.WriteString(fmt.Sprintf("%s\n\n", skill.Description))
		sb.WriteString(fmt.Sprintf("👤 Author: %s\n", skill.Author))
		sb.WriteString(fmt.Sprintf("📥 Downloads: %d\n", skill.Downloads))
		sb.WriteString(fmt.Sprintf("⭐ Rating: %.1f\n\n", skill.Rating))
	}
	
	return sb.String()
}

func contains(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
