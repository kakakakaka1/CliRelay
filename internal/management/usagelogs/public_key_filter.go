package usagelogs

import (
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
)

func publicAllowedAPIKeyIDs(tenantID, endUserID string, subjectSecrets []string) map[string]struct{} {
	allowed := make(map[string]struct{})
	if eu := strings.TrimSpace(endUserID); eu != "" {
		for _, id := range usage.ListAPIKeyIDsForEndUserForTenant(tenantID, eu) {
			if trimmed := strings.TrimSpace(id); trimmed != "" {
				allowed[trimmed] = struct{}{}
			}
		}
		return allowed
	}
	for _, secret := range subjectSecrets {
		row := usage.GetAPIKeyForTenant(tenantID, secret)
		if row == nil {
			row = usage.GetAPIKey(secret)
		}
		if row == nil {
			continue
		}
		if id := strings.TrimSpace(row.ID); id != "" {
			allowed[id] = struct{}{}
		}
	}
	return allowed
}

func constrainPublicAPIKeyIDs(
	requested []string,
	matchNone bool,
	allowed map[string]struct{},
) ([]string, bool) {
	if matchNone {
		return nil, true
	}
	if len(requested) == 0 {
		return nil, false
	}
	if len(allowed) == 0 {
		return nil, true
	}
	out := make([]string, 0, len(requested))
	seen := make(map[string]struct{}, len(requested))
	for _, id := range requested {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := allowed[id]; !ok {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil, true
	}
	return out, false
}

func filterPublicAPIKeyIDOptions(
	allowed map[string]struct{},
	ids []string,
	names map[string]string,
) ([]string, map[string]string) {
	if len(allowed) == 0 {
		return make([]string, 0), make(map[string]string)
	}
	outIDs := make([]string, 0, len(ids))
	outNames := make(map[string]string, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := allowed[id]; !ok {
			continue
		}
		outIDs = append(outIDs, id)
		if name := strings.TrimSpace(names[id]); name != "" {
			outNames[id] = name
		}
	}
	return outIDs, outNames
}
