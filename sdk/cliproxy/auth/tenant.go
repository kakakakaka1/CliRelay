package auth

import (
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// TenantIDFromAuthID restores the tenant namespace encoded in auth IDs stored
// as <tenant-id>/<relative-file>. Legacy root-level IDs belong to the system tenant.
func TenantIDFromAuthID(id string) string {
	clean := filepath.ToSlash(filepath.Clean(strings.TrimSpace(id)))
	if clean == "." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return defaultTenantID
	}
	first, _, found := strings.Cut(clean, "/")
	if !found {
		return defaultTenantID
	}
	if _, err := uuid.Parse(first); err != nil {
		return defaultTenantID
	}
	return normalizedTenantID(first)
}
