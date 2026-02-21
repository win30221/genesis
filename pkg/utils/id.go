package utils

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"time"
)

var objectIDCounter uint32

// GenerateID generates a 12-byte ObjectID-like string (24 hex characters).
func GenerateID() string {
	var b [12]byte
	binary.BigEndian.PutUint32(b[0:4], uint32(time.Now().Unix()))
	_, _ = rand.Read(b[4:9])
	c := atomic.AddUint32(&objectIDCounter, 1) % 0xFFFFFF
	b[9] = byte(c >> 16)
	b[10] = byte(c >> 8)
	b[11] = byte(c)
	return hex.EncodeToString(b[:])
}

// GenerateTimestampPrefix returns an 8-char hex timestamp followed by an underscore.
// Example: "65cfda3f_"
func GenerateTimestampPrefix() string {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(time.Now().Unix()))
	return hex.EncodeToString(b) + "_"
}

// GetTimeFromID extracts the creation time from a string that starts with 8-char hex timestamp.
func GetTimeFromID(id string) (time.Time, error) {
	if len(id) < 8 {
		return time.Time{}, fmt.Errorf("id too short: %d", len(id))
	}
	// Support both "65cfda3f..." and "65cfda3f_..."
	hexPart := id[:8]
	b, err := hex.DecodeString(hexPart)
	if err != nil {
		return time.Time{}, err
	}
	sec := binary.BigEndian.Uint32(b)
	return time.Unix(int64(sec), 0), nil
}

// IsOlderThan checks if the ID was created more than 'd' duration ago.
func IsOlderThan(id string, d time.Duration) bool {
	t, err := GetTimeFromID(id)
	if err != nil {
		return false
	}
	return time.Since(t) > d
}
