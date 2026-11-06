package crypto

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"runtime"
	"sort"
	"strings"
)

// GenerateFingerprint produces a stable, unique machine identifier derived
// from: hostname, MAC addresses, OS info, and architecture.
// The fingerprint is stable across reboots but changes with hardware.
func GenerateFingerprint() string {
	var parts []string

	// Hostname
	if h, err := os.Hostname(); err == nil {
		parts = append(parts, "host:"+h)
	}

	// MAC addresses (sorted for stability)
	macs := getMACAddresses()
	if len(macs) > 0 {
		sort.Strings(macs)
		parts = append(parts, "mac:"+strings.Join(macs, ","))
	}

	// OS + Arch
	parts = append(parts, fmt.Sprintf("os:%s/%s", runtime.GOOS, runtime.GOARCH))

	// Machine ID (Linux /etc/machine-id, macOS IOPlatformUUID fallback)
	if mid := readMachineID(); mid != "" {
		parts = append(parts, "mid:"+mid)
	}

	data := strings.Join(parts, "|")
	h := sha256.Sum256([]byte(data))
	return hex.EncodeToString(h[:])
}

// GenerateShortFingerprint returns the first 16 hex chars of the full fingerprint.
func GenerateShortFingerprint() string {
	fp := GenerateFingerprint()
	if len(fp) > 16 {
		return fp[:16]
	}
	return fp
}

// ComputeMessageFingerprint computes a 3-character attribution fingerprint
// matching the TypeScript computeFingerprint(messageText, version).
// Algorithm: SHA256(SALT + msg[4] + msg[7] + msg[20] + version)[:3]
func ComputeMessageFingerprint(messageText, version string) string {
	const salt = "59cf53e54c78"
	indices := [3]int{4, 7, 20}
	var chars strings.Builder
	for _, i := range indices {
		if i < len(messageText) {
			chars.WriteByte(messageText[i])
		} else {
			chars.WriteByte('0')
		}
	}
	input := salt + chars.String() + version
	h := sha256.Sum256([]byte(input))
	return hex.EncodeToString(h[:])[:3]
}

// -------------------------------------------------------------------
// helpers
// -------------------------------------------------------------------

func getMACAddresses() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var macs []string
	for _, iface := range ifaces {
		mac := iface.HardwareAddr.String()
		if mac != "" && !isLoopbackMAC(mac) {
			macs = append(macs, mac)
		}
	}
	return macs
}

func isLoopbackMAC(mac string) bool {
	return mac == "00:00:00:00:00:00"
}

func readMachineID() string {
	// Linux
	if data, err := os.ReadFile("/etc/machine-id"); err == nil {
		return strings.TrimSpace(string(data))
	}
	// macOS fallback – read from IOKit plist (best-effort, no exec)
	if data, err := os.ReadFile("/Library/Preferences/SystemConfiguration/com.apple.Boot.plist"); err == nil {
		return HashSHA256String(string(data))[:16]
	}
	return ""
}
