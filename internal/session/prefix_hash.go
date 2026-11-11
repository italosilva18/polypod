package session

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"time"
)

var messagePrefixSeed = sha256.Sum256([]byte("polypod-session-prefix-v1"))

// MessagePrefixHash binds the ordered ID, role, content, and timestamp of a
// transcript prefix. A caller may compare it with SessionHeader.PrefixHash to
// prove that an append leaves every previously saved message unchanged.
func MessagePrefixHash(messages []Message) string {
	state := messagePrefixSeed
	for _, message := range messages {
		state = nextMessagePrefixHash(state, message)
	}
	return hex.EncodeToString(state[:])
}

func extendMessagePrefixHash(prefix string, messages []Message) string {
	decoded, err := hex.DecodeString(prefix)
	if err != nil || len(decoded) != sha256.Size {
		return ""
	}
	var state [sha256.Size]byte
	copy(state[:], decoded)
	for _, message := range messages {
		state = nextMessagePrefixHash(state, message)
	}
	return hex.EncodeToString(state[:])
}

func nextMessagePrefixHash(state [sha256.Size]byte, message Message) [sha256.Size]byte {
	h := sha256.New()
	_, _ = h.Write(state[:])
	writePrefixField(h, message.ID)
	writePrefixField(h, message.Role)
	writePrefixField(h, message.Content)
	writePrefixField(h, message.Timestamp.UTC().Format(time.RFC3339Nano))
	var next [sha256.Size]byte
	copy(next[:], h.Sum(nil))
	return next
}

func writePrefixField(h hash.Hash, value string) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = h.Write(size[:])
	_, _ = h.Write([]byte(value))
}

func validPrefixHash(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}
