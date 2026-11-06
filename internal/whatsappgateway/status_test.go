package whatsappgateway

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStatusRoundTripAndPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "health", "whatsapp-status.json")
	if err := WriteStatus(path, "connected"); err != nil {
		t.Fatal(err)
	}
	status, err := ReadStatus(path)
	if err != nil || status.State != "connected" || !status.SocketConnected || !status.Authenticated || time.Since(status.CheckedAt) > time.Minute {
		t.Fatalf("status: %+v, %v", status, err)
	}
	if err := WriteStatusHealth(path, "connected", true, false); err == nil {
		t.Fatal("connected state accepted without authentication")
	}
	if err := WriteStatusHealth(path, "retrying", true, false); err != nil {
		t.Fatal(err)
	}
	status, err = ReadStatus(path)
	if err != nil || status.State != "retrying" || !status.SocketConnected || status.Authenticated {
		t.Fatalf("unauthenticated socket status: %+v, %v", status, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("status permissions: %v, %v", info, err)
	}
	if err := WriteStatus(path, "stopped"); err != nil {
		t.Fatal(err)
	}
	status, err = ReadStatus(path)
	if err != nil || status.State != "stopped" {
		t.Fatalf("updated status: %+v, %v", status, err)
	}
}
