// Package asciicast records terminal sessions in asciicast v2 format
// (asciinema-compatible). It captures output and input events with precise
// timestamps and serialises them as a header JSON line followed by
// newline-delimited event JSON arrays.
package asciicast

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Header is the first line of an asciicast v2 file.
type Header struct {
	Version   int               `json:"version"`
	Width     int               `json:"width"`
	Height    int               `json:"height"`
	Timestamp int64             `json:"timestamp,omitempty"`
	Title     string            `json:"title,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
}

// Event represents a single asciicast v2 event.
// Time is seconds since recording start, Type is "o" (output) or "i" (input),
// and Data is the payload string.
type Event struct {
	Time float64 `json:"time"`
	Type string  `json:"type"`
	Data string  `json:"data"`
}

// MarshalJSON produces the asciicast v2 array format: [time, type, data].
func (e Event) MarshalJSON() ([]byte, error) {
	return json.Marshal([3]any{e.Time, e.Type, e.Data})
}

// UnmarshalJSON parses the asciicast v2 array format: [time, type, data].
func (e *Event) UnmarshalJSON(data []byte) error {
	var arr [3]json.RawMessage
	if err := json.Unmarshal(data, &arr); err != nil {
		return fmt.Errorf("asciicast: event must be a 3-element array: %w", err)
	}
	if err := json.Unmarshal(arr[0], &e.Time); err != nil {
		return fmt.Errorf("asciicast: invalid event time: %w", err)
	}
	if err := json.Unmarshal(arr[1], &e.Type); err != nil {
		return fmt.Errorf("asciicast: invalid event type: %w", err)
	}
	if err := json.Unmarshal(arr[2], &e.Data); err != nil {
		return fmt.Errorf("asciicast: invalid event data: %w", err)
	}
	return nil
}

// Recorder captures terminal events and can serialise them as asciicast v2.
type Recorder struct {
	mu     sync.Mutex
	header Header
	events []Event
	start  time.Time
	stop   time.Time
}

// NewRecorder creates a Recorder pre-configured with the given terminal
// dimensions and title. Call Start to begin timing.
func NewRecorder(width, height int, title string) *Recorder {
	return &Recorder{
		header: Header{
			Version: 2,
			Width:   width,
			Height:  height,
			Title:   title,
		},
	}
}

// Start marks the beginning of the recording. Events recorded after this
// call will have their Time field set relative to this moment.
func (r *Recorder) Start() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.start = time.Now()
	r.header.Timestamp = r.start.Unix()
}

// elapsed returns seconds since Start was called. Must be called with mu held.
func (r *Recorder) elapsed() float64 {
	return time.Since(r.start).Seconds()
}

// WriteOutput records an output ("o") event with the current elapsed time.
func (r *Recorder) WriteOutput(data string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, Event{
		Time: r.elapsed(),
		Type: "o",
		Data: data,
	})
}

// WriteInput records an input ("i") event with the current elapsed time.
func (r *Recorder) WriteInput(data string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, Event{
		Time: r.elapsed(),
		Type: "i",
		Data: data,
	})
}

// Stop marks the end of the recording.
func (r *Recorder) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stop = time.Now()
}

// Duration returns the wall-clock duration of the recording (Start to Stop).
// If Stop has not been called, it returns the time elapsed since Start.
func (r *Recorder) Duration() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stop.IsZero() {
		return time.Since(r.start)
	}
	return r.stop.Sub(r.start)
}

// EventCount returns the number of recorded events.
func (r *Recorder) EventCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

// Save writes the recording to the given path in asciicast v2 format.
// The file consists of a header JSON object on the first line, followed by
// one JSON array per event on subsequent lines.
func (r *Recorder) Save(path string) error {
	r.mu.Lock()
	header := r.header
	events := make([]Event, len(r.events))
	copy(events, r.events)
	r.mu.Unlock()

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("asciicast: create file: %w", err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)

	if err := enc.Encode(header); err != nil {
		return fmt.Errorf("asciicast: write header: %w", err)
	}

	for i := range events {
		if err := enc.Encode(events[i]); err != nil {
			return fmt.Errorf("asciicast: write event %d: %w", i, err)
		}
	}

	return nil
}
