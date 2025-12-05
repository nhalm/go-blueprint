package id

import "github.com/segmentio/ksuid"

// GenerateIDWithPrefix creates a new KSUID with the given prefix.
// KSUIDs are time-ordered, collision-resistant, and URL-safe.
//
// Format: <prefix><27-char-ksuid>
// Example: prod_2ArTLVPddDx8vZk7CqEbiYp1
func GenerateIDWithPrefix(prefix string) string {
	return prefix + ksuid.New().String()
}
