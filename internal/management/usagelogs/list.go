package usagelogs

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	managementauthfiles "github.com/router-for-me/CLIProxyAPI/v6/internal/management/authfiles"
	apikeysettings "github.com/router-for-me/CLIProxyAPI/v6/internal/management/settings/apikey"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func (s *Service) ManagementLogs(input ManagementLogQueryInput) (map[string]any, error) {
	keyNameMap, channelNameMap, authIndexChannelMap, ambiguousAuthIndexChannelMap, authMetaByIndex, authIndexGroup := s.buildNameMaps()
	authIndexes, channelNames, authIndexChannelNames := channelFilterSelectors(input.Channels, channelNameMap, authIndexChannelMap, ambiguousAuthIndexChannelMap, authMetaByIndex, authIndexGroup)

	params := usage.LogQueryParams{
		TenantID:              s.tenantID,
		Page:                  input.Page,
		Size:                  input.Size,
		Days:                  input.Days,
		APIKeys:               input.APIKeys,
		Models:                input.Models,
		Statuses:              input.Statuses,
		MatchNoAPIKeys:        input.MatchNoAPIKeys,
		MatchNoModels:         input.MatchNoModels,
		MatchNoStatuses:       input.MatchNoStatuses,
		MatchNoChannels:       input.MatchNoChannels,
		AuthIndexes:           authIndexes,
		ChannelNames:          channelNames,
		AuthIndexChannelNames: authIndexChannelNames,
	}

	result, err := usage.QueryLogs(params)
	if err != nil {
		return nil, err
	}
	filters, err := usage.QueryFiltersForLogs(params)
	if err != nil {
		return nil, err
	}
	stats, err := usage.QueryStats(params)
	if err != nil {
		return nil, err
	}

	for i := range result.Items {
		item := &result.Items[i]
		if item.APIKeyName == "" {
			if name, ok := keyNameMap[item.APIKey]; ok {
				item.APIKeyName = name
			}
		}
		if channelName := displayChannelNameForLog(*item, channelNameMap, authIndexChannelMap, ambiguousAuthIndexChannelMap); channelName != "" {
			item.ChannelName = channelName
		}
		enrichLogRowChannelMeta(item, authMetaByIndex)
	}

	if filters.APIKeyNames == nil {
		filters.APIKeyNames = make(map[string]string, len(filters.APIKeys))
	}
	for _, key := range filters.APIKeys {
		if name, ok := keyNameMap[key]; ok {
			filters.APIKeyNames[key] = name
		}
	}
	filters.ChannelOptions = enrichChannelFilterOptions(filters.ChannelOptions, channelNameMap, authIndexChannelMap, authMetaByIndex, authIndexGroup)
	filters.Channels = channelLabelsFromOptions(filters.ChannelOptions)

	if result.Items == nil {
		result.Items = make([]usage.LogRow, 0)
	}
	if filters.APIKeys == nil {
		filters.APIKeys = make([]string, 0)
	}
	if filters.Models == nil {
		filters.Models = make([]string, 0)
	}
	if filters.Channels == nil {
		filters.Channels = make([]string, 0)
	}
	if filters.ChannelOptions == nil {
		filters.ChannelOptions = make([]usage.ChannelFilterOption, 0)
	}
	if filters.Statuses == nil {
		filters.Statuses = make([]string, 0)
	}
	if filters.APIKeyNames == nil {
		filters.APIKeyNames = make(map[string]string)
	}

	return map[string]any{
		"items":   result.Items,
		"total":   result.Total,
		"page":    result.Page,
		"size":    result.Size,
		"filters": filters,
		"stats":   stats,
	}, nil
}

func (s *Service) ClearAllRequestLogs() (any, error) {
	return usage.ClearAllRequestLogsForTenant(s.tenantID)
}

func (s *Service) ClearRequestLogs(options usage.ClearRequestLogsOptions) (int, any, error) {
	options.TenantID = s.tenantID
	result, err := usage.ClearRequestLogs(options)
	if err != nil {
		if strings.Contains(err.Error(), "at least one cleanup option") {
			return http.StatusBadRequest, map[string]any{"error": err.Error()}, err
		}
		return http.StatusInternalServerError, map[string]any{"error": err.Error()}, err
	}
	return http.StatusOK, result, nil
}

func (s *Service) PublicUsageLogs(input PublicLogQueryInput) (map[string]any, error) {
	_, channelNameMap, authIndexChannelMap, ambiguousAuthIndexChannelMap, authMetaByIndex, authIndexGroup := s.buildNameMaps()
	authIndexes, channelNames, authIndexChannelNames := channelFilterSelectors(input.Channels, channelNameMap, authIndexChannelMap, ambiguousAuthIndexChannelMap, authMetaByIndex, authIndexGroup)
	params := usage.LogQueryParams{
		TenantID:              usage.ResolveAPIKeyTenant(input.APIKey),
		Page:                  input.Page,
		Size:                  input.Size,
		Days:                  input.Days,
		APIKey:                input.APIKey,
		Models:                input.Models,
		Statuses:              input.Statuses,
		MatchNoModels:         input.MatchNoModels,
		MatchNoChannels:       input.MatchNoChannels,
		MatchNoStatuses:       input.MatchNoStatuses,
		AuthIndexes:           authIndexes,
		ChannelNames:          channelNames,
		AuthIndexChannelNames: authIndexChannelNames,
	}

	result, err := usage.QueryLogs(params)
	if err != nil {
		return nil, err
	}
	stats, err := usage.QueryStats(params)
	if err != nil {
		return nil, err
	}
	filters, err := usage.QueryFiltersForLogs(params)
	if err != nil {
		return nil, err
	}

	apiKeyName := s.publicAPIKeyName(input.APIKey)
	for i := range result.Items {
		if apiKeyName == "" {
			apiKeyName = strings.TrimSpace(result.Items[i].APIKeyName)
		}
		channelName := displayChannelNameForLog(result.Items[i], channelNameMap, authIndexChannelMap, ambiguousAuthIndexChannelMap)
		result.Items[i].Source = ""
		result.Items[i].AuthIndex = ""
		result.Items[i].ChannelName = channelName
		result.Items[i].APIKey = ""
		result.Items[i].APIKeyName = ""
		// Keep provider/auth_type for public UI badges, but strip identity keys above.
		enrichLogRowChannelMeta(&result.Items[i], authMetaByIndex)
		result.Items[i].AuthIndex = ""
	}

	filters.ChannelOptions = enrichChannelFilterOptions(filters.ChannelOptions, channelNameMap, authIndexChannelMap, authMetaByIndex, authIndexGroup)
	// Public responses keep opaque filter values (auth_index) so same-email
	// multi-provider accounts stay selectable, but strip the auth_index field.
	for i := range filters.ChannelOptions {
		filters.ChannelOptions[i].AuthIndex = ""
	}
	filters.Channels = channelLabelsFromOptions(filters.ChannelOptions)
	if result.Items == nil {
		result.Items = make([]usage.LogRow, 0)
	}
	if filters.Models == nil {
		filters.Models = make([]string, 0)
	}
	if filters.Channels == nil {
		filters.Channels = make([]string, 0)
	}
	if filters.ChannelOptions == nil {
		filters.ChannelOptions = make([]usage.ChannelFilterOption, 0)
	}
	if filters.Statuses == nil {
		filters.Statuses = make([]string, 0)
	}

	return map[string]any{
		"items":        result.Items,
		"total":        result.Total,
		"page":         result.Page,
		"size":         result.Size,
		"stats":        stats,
		"api_key_name": apiKeyName,
		"filters": map[string]any{
			"models":          filters.Models,
			"channels":        filters.Channels,
			"channel_options": filters.ChannelOptions,
			"statuses":        filters.Statuses,
		},
	}, nil
}

type authChannelMeta struct {
	label    string
	provider string
	authType string
}

func channelFilterSelectors(
	channels []string,
	channelNameMap, authIndexChannelMap map[string]string,
	ambiguousAuthIndexChannelMap map[string][]string,
	authMetaByIndex map[string]authChannelMeta,
	authIndexGroup map[string][]string,
) ([]string, []string, map[string][]string) {
	// Preserve original selected values. Only use lower-case keys for label matching.
	selectedRaw := make([]string, 0, len(channels))
	selectedLabelKeys := make(map[string]struct{})
	for _, part := range channels {
		raw := strings.TrimSpace(part)
		if raw == "" {
			continue
		}
		selectedRaw = append(selectedRaw, raw)
		selectedLabelKeys[strings.ToLower(raw)] = struct{}{}
	}
	if len(selectedRaw) == 0 {
		return nil, nil, nil
	}

	var authIndexes []string
	var channelNames []string
	authIndexChannelNames := make(map[string][]string)
	seenAuthIndex := make(map[string]struct{})
	seenChannelName := make(map[string]struct{})

	appendAuthIndex := func(idx string) {
		idx = strings.TrimSpace(idx)
		if idx == "" {
			return
		}
		if _, ok := seenAuthIndex[idx]; ok {
			return
		}
		seenAuthIndex[idx] = struct{}{}
		authIndexes = append(authIndexes, idx)
	}
	// Expand xAI OAuth historical aliases (id: vs file: seed) so one UI option
	// returns logs written under either index.
	appendAuthIndexGroup := func(idx string) {
		idx = strings.TrimSpace(idx)
		if idx == "" {
			return
		}
		if group := authIndexGroup[idx]; len(group) > 0 {
			for _, member := range group {
				appendAuthIndex(member)
			}
			return
		}
		appendAuthIndex(idx)
	}
	appendChannelName := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		key := strings.ToLower(name)
		if _, ok := seenChannelName[key]; ok {
			return
		}
		seenChannelName[key] = struct{}{}
		channelNames = append(channelNames, name)
	}

	for _, raw := range selectedRaw {
		// Prefer exact auth_index matches so multi-provider same-email accounts
		// filter independently when clients send auth_index as the value.
		if _, ok := authIndexChannelMap[raw]; ok {
			appendAuthIndexGroup(raw)
			if legacyChannels := ambiguousAuthIndexChannelMap[raw]; len(legacyChannels) > 0 {
				authIndexChannelNames[raw] = append(authIndexChannelNames[raw], legacyChannels...)
			}
			// Also attach legacy channel names for every expanded group member.
			for _, member := range authIndexGroup[raw] {
				if legacyChannels := ambiguousAuthIndexChannelMap[member]; len(legacyChannels) > 0 {
					authIndexChannelNames[member] = append(authIndexChannelNames[member], legacyChannels...)
				}
			}
			continue
		}
		if _, ok := authMetaByIndex[raw]; ok {
			appendAuthIndexGroup(raw)
			continue
		}
		matchedAuthIndex := false
		for idx := range authIndexChannelMap {
			if strings.EqualFold(strings.TrimSpace(idx), raw) {
				appendAuthIndexGroup(idx)
				if legacyChannels := ambiguousAuthIndexChannelMap[idx]; len(legacyChannels) > 0 {
					authIndexChannelNames[idx] = append(authIndexChannelNames[idx], legacyChannels...)
				}
				matchedAuthIndex = true
			}
		}
		if matchedAuthIndex {
			continue
		}
		for idx := range authMetaByIndex {
			if strings.EqualFold(strings.TrimSpace(idx), raw) {
				appendAuthIndexGroup(idx)
				matchedAuthIndex = true
			}
		}
		if matchedAuthIndex {
			continue
		}
		// Historical channel_options values are auth_index hashes even when the
		// auth file was later reindexed (file: vs id: seed) or deleted. Treat
		// stable auth_index tokens as AuthIndexes; never fall back to
		// channel_name matching for them (that path yields 0 rows).
		if looksLikeAuthIndex(raw) {
			appendAuthIndexGroup(raw)
			continue
		}
		// Legacy clients still send display labels / emails.
		appendChannelName(raw)
	}

	for raw, name := range channelNameMap {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" {
			continue
		}
		if _, ok := selectedLabelKeys[key]; ok {
			appendChannelName(raw)
		}
	}
	for idx, name := range authIndexChannelMap {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" {
			continue
		}
		if _, ok := selectedLabelKeys[key]; ok {
			appendAuthIndexGroup(idx)
			if legacyChannels := ambiguousAuthIndexChannelMap[idx]; len(legacyChannels) > 0 {
				authIndexChannelNames[idx] = append(authIndexChannelNames[idx], legacyChannels...)
			}
		}
	}
	if len(authIndexes) == 0 && len(channelNames) == 0 {
		authIndexes = []string{""}
	}
	return authIndexes, channelNames, authIndexChannelNames
}

// looksLikeAuthIndex reports whether value matches the stable auth index format
// produced by coreauth.stableAuthIndex (first 8 bytes of SHA-256 as 16 hex chars).
// Channel facet values use this token so historical rows remain filterable after
// live auth reindex or deletion.
func looksLikeAuthIndex(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 16 {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

func enrichChannelFilterOptions(
	options []usage.ChannelFilterOption,
	channelNameMap, authIndexChannelMap map[string]string,
	authMetaByIndex map[string]authChannelMeta,
	authIndexGroup map[string][]string,
) []usage.ChannelFilterOption {
	if len(options) == 0 {
		return make([]usage.ChannelFilterOption, 0)
	}

	// Prefer one option per live auth_index. Collapse pure name-only rows when
	// the same label already has auth-backed options. For xAI OAuth, also
	// collapse historical id:/file: seed aliases onto the canonical live index.
	out := make([]usage.ChannelFilterOption, 0, len(options))
	seenValue := make(map[string]struct{}, len(options))
	authBackedLabels := make(map[string]struct{})

	canonicalAuthIndex := func(authIndex string) string {
		authIndex = strings.TrimSpace(authIndex)
		if authIndex == "" {
			return ""
		}
		if group := authIndexGroup[authIndex]; len(group) > 0 {
			// buildNameMaps stores the live EnsureIndex() first.
			return strings.TrimSpace(group[0])
		}
		return authIndex
	}

	for _, option := range options {
		authIndex := strings.TrimSpace(option.AuthIndex)
		if authIndex == "" {
			authIndex = strings.TrimSpace(option.Value)
		}
		authIndex = canonicalAuthIndex(authIndex)
		if authIndex == "" {
			continue
		}
		if _, ok := authIndexChannelMap[authIndex]; ok {
			if name := strings.TrimSpace(authIndexChannelMap[authIndex]); name != "" {
				authBackedLabels[strings.ToLower(name)] = struct{}{}
			}
		}
		if meta, ok := authMetaByIndex[authIndex]; ok && meta.label != "" {
			authBackedLabels[strings.ToLower(meta.label)] = struct{}{}
		}
	}

	for _, option := range options {
		label := strings.TrimSpace(option.Label)
		authIndex := strings.TrimSpace(option.AuthIndex)
		if authIndex == "" {
			authIndex = strings.TrimSpace(option.Value)
		}
		// Fold xAI OAuth historical aliases onto the live index so the UI shows
		// one Grok OAuth option with provider/auth_type badges.
		authIndex = canonicalAuthIndex(authIndex)
		if label == "" && authIndex != "" {
			if name, ok := authIndexChannelMap[authIndex]; ok && strings.TrimSpace(name) != "" {
				label = strings.TrimSpace(name)
			}
		}
		if label == "" {
			if name, ok := channelNameMap[strings.TrimSpace(option.Value)]; ok && strings.TrimSpace(name) != "" {
				label = strings.TrimSpace(name)
			}
		}
		if label == "" {
			label = strings.TrimSpace(option.Value)
		}
		if label == "" {
			continue
		}

		provider := strings.TrimSpace(option.Provider)
		authType := strings.TrimSpace(option.AuthType)
		value := strings.TrimSpace(option.Value)
		if value == "" {
			value = authIndex
		}
		if value == "" {
			value = label
		}
		// When we rewrote authIndex to the group canonical, filter value must
		// match so clients select the live index (which expands server-side).
		if authIndex != "" {
			if group := authIndexGroup[authIndex]; len(group) > 0 {
				value = authIndex
			}
		}

		hasLiveMeta := false
		if meta, ok := authMetaByIndex[authIndex]; ok {
			hasLiveMeta = true
			if meta.label != "" {
				label = meta.label
			}
			if meta.provider != "" {
				provider = meta.provider
			}
			if meta.authType != "" {
				authType = meta.authType
			}
			value = authIndex
		} else if authIndex != "" {
			// Keep auth_index as the filter value even without live meta so
			// historical rows for deleted auths stay independently selectable.
			value = authIndex
			if mapped, ok := authIndexChannelMap[authIndex]; ok && strings.TrimSpace(mapped) != "" {
				label = strings.TrimSpace(mapped)
			}
		} else if mapped, ok := channelNameMap[label]; ok && strings.TrimSpace(mapped) != "" {
			label = strings.TrimSpace(mapped)
		}

		// Drop name-only rows that would re-merge same-email multi-provider
		// accounts already represented by auth_index-backed options.
		if !hasLiveMeta && authIndex == "" {
			if _, ok := authBackedLabels[strings.ToLower(label)]; ok {
				continue
			}
		}

		dedupeKey := strings.ToLower(value)
		if _, ok := seenValue[dedupeKey]; ok {
			continue
		}
		seenValue[dedupeKey] = struct{}{}

		out = append(out, usage.ChannelFilterOption{
			Value:     value,
			Label:     label,
			Provider:  provider,
			AuthType:  normalizeAuthType(authType),
			AuthIndex: authIndex,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		li := strings.ToLower(out[i].Label)
		lj := strings.ToLower(out[j].Label)
		if li != lj {
			return li < lj
		}
		pi := strings.ToLower(out[i].Provider)
		pj := strings.ToLower(out[j].Provider)
		if pi != pj {
			return pi < pj
		}
		return strings.ToLower(out[i].Value) < strings.ToLower(out[j].Value)
	})
	return out
}

func channelLabelsFromOptions(options []usage.ChannelFilterOption) []string {
	if len(options) == 0 {
		return make([]string, 0)
	}
	// Keep legacy channels as unique display labels (may collapse same-email providers).
	// New clients should use channel_options.
	seen := make(map[string]struct{}, len(options))
	labels := make([]string, 0, len(options))
	for _, option := range options {
		label := strings.TrimSpace(option.Label)
		if label == "" {
			label = strings.TrimSpace(option.Value)
		}
		if label == "" {
			continue
		}
		key := strings.ToLower(label)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		labels = append(labels, label)
	}
	return labels
}

func enrichLogRowChannelMeta(item *usage.LogRow, authMetaByIndex map[string]authChannelMeta) {
	if item == nil {
		return
	}
	if meta, ok := authMetaByIndex[strings.TrimSpace(item.AuthIndex)]; ok {
		if meta.provider != "" {
			item.Provider = meta.provider
		}
		if meta.authType != "" {
			item.AuthType = normalizeAuthType(meta.authType)
		}
		return
	}
	if item.Provider == "" {
		item.Provider = usageGuessProviderFromSource(item.Source)
	}
}

func usageGuessProviderFromSource(source string) string {
	source = strings.ToLower(strings.TrimSpace(source))
	if source == "" || strings.Contains(source, "@") || strings.Contains(source, " ") || len(source) > 32 {
		return ""
	}
	return source
}

func normalizeAuthType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "oauth":
		return "oauth"
	case "api", "api_key", "apikey":
		return "api"
	default:
		return ""
	}
}

func (s *Service) publicAPIKeyName(apiKey string) string {
	row := apikeysettings.NewService(nil, apikeysettings.WithTenantID(s.tenantID)).GetRow(apiKey)
	if row == nil {
		return ""
	}
	return strings.TrimSpace(row.Name)
}

func displayChannelNameForLog(item usage.LogRow, channelNameMap, authIndexChannelMap map[string]string, ambiguousAuthIndexChannelMap map[string][]string) string {
	if channel := strings.TrimSpace(item.ChannelName); channel != "" {
		if name, ok := authIndexChannelMap[item.AuthIndex]; ok && strings.TrimSpace(name) != "" {
			if _, legacy := channelNameMap[channel]; legacy || containsFold(ambiguousAuthIndexChannelMap[item.AuthIndex], channel) {
				return strings.TrimSpace(name)
			}
		}
		if name, ok := channelNameMap[channel]; ok && strings.TrimSpace(name) != "" {
			return strings.TrimSpace(name)
		}
		return channel
	}
	if name, ok := authIndexChannelMap[item.AuthIndex]; ok && strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name)
	}
	if name, ok := channelNameMap[item.Source]; ok {
		return strings.TrimSpace(name)
	}
	return ""
}

func (s *Service) buildNameMaps() (
	keyNameMap, channelNameMap, authIndexChannelMap map[string]string,
	ambiguousAuthIndexChannelMap map[string][]string,
	authMetaByIndex map[string]authChannelMeta,
	authIndexGroup map[string][]string,
) {
	keyNameMap = make(map[string]string)
	channelNameMap = make(map[string]string)
	authIndexChannelMap = make(map[string]string)
	ambiguousAuthIndexChannelMap = make(map[string][]string)
	authMetaByIndex = make(map[string]authChannelMeta)
	authIndexGroup = make(map[string][]string)

	for _, row := range apikeysettings.NewService(nil, apikeysettings.WithTenantID(s.tenantID)).ListRows() {
		if row.Key != "" && row.Name != "" {
			keyNameMap[row.Key] = row.Name
		}
	}

	cfg := s.cfg
	if cfg != nil {
		for _, k := range cfg.GeminiKey {
			if k.APIKey != "" && k.Name != "" {
				channelNameMap[k.APIKey] = k.Name
			}
		}
		for _, k := range cfg.ClaudeKey {
			if k.APIKey != "" && k.Name != "" {
				channelNameMap[k.APIKey] = k.Name
			}
		}
		for _, k := range cfg.CodexKey {
			if k.APIKey != "" && k.Name != "" {
				channelNameMap[k.APIKey] = k.Name
			}
		}
		for _, provider := range cfg.OpenAICompatibility {
			if provider.Name == "" {
				continue
			}
			for _, entry := range provider.APIKeyEntries {
				if entry.APIKey != "" {
					channelNameMap[entry.APIKey] = provider.Name
				}
			}
		}
	}

	type legacyChannelCandidate struct {
		key       string
		channel   string
		authIndex string
	}
	var legacyCandidates []legacyChannelCandidate

	if s.authManager != nil {
		for _, auth := range s.authManager.ListForTenant(s.tenantID) {
			if auth == nil {
				continue
			}
			channel := strings.TrimSpace(auth.ChannelName())
			if channel == "" {
				continue
			}
			auth.EnsureIndex()
			idx := strings.TrimSpace(auth.Index)
			meta := authChannelMeta{
				label:    channel,
				provider: normalizeProviderKey(auth.Provider),
				authType: resolveAuthType(auth),
			}
			// xAI/Grok OAuth historically flipped between id: and file: seeds for
			// the same credential. Register the full index group so filters and
			// channel_options collapse onto one live option while still matching
			// historical rows.
			group := xaiOAuthAuthIndexGroup(auth)
			if len(group) == 0 && idx != "" {
				group = []string{idx}
			}
			for _, member := range group {
				member = strings.TrimSpace(member)
				if member == "" {
					continue
				}
				authIndexChannelMap[member] = channel
				authMetaByIndex[member] = meta
				authIndexGroup[member] = group
			}
			if accountType, account := auth.AccountInfo(); strings.EqualFold(accountType, "oauth") {
				if source := strings.TrimSpace(account); source != "" {
					legacyCandidates = append(legacyCandidates, legacyChannelCandidate{key: source, channel: channel, authIndex: idx})
				}
			}
			if email := strings.TrimSpace(managementauthfiles.Email(auth)); email != "" {
				legacyCandidates = append(legacyCandidates, legacyChannelCandidate{key: email, channel: channel, authIndex: idx})
			}
		}
	}

	legacyChannelsByKey := make(map[string]map[string]struct{})
	for _, candidate := range legacyCandidates {
		key := strings.TrimSpace(candidate.key)
		channel := strings.TrimSpace(candidate.channel)
		if key == "" || channel == "" {
			continue
		}
		if legacyChannelsByKey[key] == nil {
			legacyChannelsByKey[key] = make(map[string]struct{})
		}
		legacyChannelsByKey[key][strings.ToLower(channel)] = struct{}{}
	}
	for _, candidate := range legacyCandidates {
		key := strings.TrimSpace(candidate.key)
		if key == "" {
			continue
		}
		if len(legacyChannelsByKey[key]) > 1 {
			if candidate.authIndex != "" {
				ambiguousAuthIndexChannelMap[candidate.authIndex] = append(ambiguousAuthIndexChannelMap[candidate.authIndex], key)
			}
			continue
		}
		channelNameMap[key] = strings.TrimSpace(candidate.channel)
	}

	return
}

// xaiOAuthAuthIndexGroup returns the live EnsureIndex first, followed by known
// historical seeds for the same xAI/Grok OAuth credential. Non-xAI auths return
// only the live index (or nil when empty).
//
// Grok OAuth is special: the same account can log under both id:<file> and
// file:<file> depending on whether FileName was populated at write time.
func xaiOAuthAuthIndexGroup(auth *coreauth.Auth) []string {
	if auth == nil {
		return nil
	}
	primary := strings.TrimSpace(auth.EnsureIndex())
	if primary == "" {
		return nil
	}
	if !isXAIProvider(auth.Provider) {
		return []string{primary}
	}
	accountType, _ := auth.AccountInfo()
	// Only merge OAuth-style xAI credentials; API-key channels stay independent.
	if !strings.EqualFold(strings.TrimSpace(accountType), "oauth") {
		// AccountInfo may miss email when metadata shape differs; still merge when
		// the channel looks like an email (typical OAuth label).
		channel := strings.TrimSpace(auth.ChannelName())
		if !strings.Contains(channel, "@") {
			return []string{primary}
		}
	}

	seen := map[string]struct{}{primary: {}}
	out := []string{primary}
	addSeed := func(seed string) {
		seed = strings.TrimSpace(seed)
		if seed == "" {
			return
		}
		idx := authIndexFromSeed(seed)
		if idx == "" {
			return
		}
		if _, ok := seen[idx]; ok {
			return
		}
		seen[idx] = struct{}{}
		out = append(out, idx)
	}

	fileName := strings.TrimSpace(auth.FileName)
	if fileName != "" {
		base := filepath.Base(fileName)
		// Current seed path.
		addSeed("file:" + fileName)
		addSeed("file:" + base)
		// Historical path: ID was set to the auth file name (including .json)
		// before FileName was populated, so EnsureIndex used id:<filename>.
		addSeed("id:" + fileName)
		addSeed("id:" + base)
	}
	if id := strings.TrimSpace(auth.ID); id != "" {
		addSeed("id:" + id)
		// If ID is a bare filename, also cover file: variants.
		if strings.HasSuffix(strings.ToLower(id), ".json") {
			addSeed("file:" + id)
			addSeed("file:" + filepath.Base(id))
		}
	}
	return out
}

func isXAIProvider(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "xai", "grok":
		return true
	default:
		return false
	}
}

// authIndexFromSeed mirrors coreauth.stableAuthIndex (sha256 first 8 bytes hex).
func authIndexFromSeed(seed string) string {
	seed = strings.TrimSpace(seed)
	if seed == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:8])
}

func resolveAuthType(auth *coreauth.Auth) string {
	if auth == nil {
		return ""
	}
	accountType, _ := auth.AccountInfo()
	return normalizeAuthType(accountType)
}

func normalizeProviderKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func containsFold(values []string, needle string) bool {
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return false
	}
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), needle) {
			return true
		}
	}
	return false
}

func IntQueryDefault(raw string, def int) int {
	v := strings.TrimSpace(raw)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return def
	}
	return n
}
