package netutil

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"time"
)

func RandomMAC() string {
	octets := []byte{0x52, 0x54, 0x00, 0x00, 0x00, 0x00}
	if _, err := rand.Read(octets[3:]); err != nil {
		fallback := sha256.Sum256([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
		copy(octets[3:], fallback[:3])
	}
	return formatMAC(octets)
}

func DeterministicMAC(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	octets := []byte{0x52, 0x54, 0x00, sum[0], sum[1], sum[2]}
	octets[3] |= 0x02
	octets[3] &^= 0x01
	return formatMAC(octets)
}

func formatMAC(octets []byte) string {
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", octets[0], octets[1], octets[2], octets[3], octets[4], octets[5])
}
