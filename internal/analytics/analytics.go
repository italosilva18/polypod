// Package analytics — Advanced Analytics
package analytics

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Event represents an analytics event
type Event struct {
	Name       string                 `json:"name"`
	Timestamp  time.Time              `json:"timestamp"`
	SessionID  string                 `json:"session_id"`
	UserID     string                 `json:"user_id"`
	Properties map[string]interface{} `json:"properties"`
}

// AnalyticsTracker tracks analytics events
type AnalyticsTracker struct {
	events    []Event
	sessionID string
	userID    string
	filePath  string
}

// NewAnalyticsTracker creates new analytics tracker
func NewAnalyticsTracker(filePath string) *AnalyticsTracker {
	return &AnalyticsTracker{
		events:    make([]Event, 0),
		sessionID: generateSessionID(),
		userID:    os.Getenv("USER"),
		filePath:  filePath,
	}
}

// Track tracks an event
func (t *AnalyticsTracker) Track(name string, properties map[string]interface{}) {
	event := Event{
		Name:       name,
		Timestamp:  time.Now(),
		SessionID:  t.sessionID,
		UserID:     t.userID,
		Properties: properties,
	}
	t.events = append(t.events, event)
}

// Save saves events to file
func (t *AnalyticsTracker) Save() error {
	dir := filepath.Dir(t.filePath)
	os.MkdirAll(dir, 0755)
	data, _ := json.MarshalIndent(t.events, "", "  ")
	return os.WriteFile(t.filePath, data, 0644)
}

func generateSessionID() string {
	return fmt.Sprintf("session_%d", time.Now().UnixNano())
}
