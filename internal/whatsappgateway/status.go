package whatsappgateway

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Status is a local, credential-free heartbeat for the native gateway.
type Status struct {
	State           string    `json:"state"`
	CheckedAt       time.Time `json:"checked_at"`
	SocketConnected bool      `json:"socket_connected"`
	Authenticated   bool      `json:"authenticated"`
}

func WriteStatus(path, state string) error {
	return WriteStatusHealth(path, state, state == "connected", state == "connected")
}

// WriteStatusHealth distinguishes a live websocket from an authenticated
// WhatsApp session. A websocket alone cannot receive account messages.
func WriteStatusHealth(path, state string, socketConnected, authenticated bool) error {
	if path == "" {
		return nil
	}
	if state != "connected" && state != "retrying" && state != "stopped" {
		return errors.New("whatsapp: invalid status")
	}
	if state == "connected" && (!socketConnected || !authenticated) {
		return errors.New("whatsapp: connected status requires authenticated socket")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	raw, err := json.Marshal(Status{State: state, CheckedAt: time.Now().UTC(), SocketConnected: socketConnected, Authenticated: authenticated})
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".whatsapp-status-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(raw); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func ReadStatus(path string) (Status, error) {
	var status Status
	raw, err := os.ReadFile(path)
	if err != nil {
		return status, err
	}
	err = json.Unmarshal(raw, &status)
	return status, err
}
