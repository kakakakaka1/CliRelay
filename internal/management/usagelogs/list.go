package usagelogs

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	managementauthfiles "github.com/router-for-me/CLIProxyAPI/v6/internal/management/authfiles"
	apikeysettings "github.com/router-for-me/CLIProxyAPI/v6/internal/management/settings/apikey"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func (s *Service) ManagementLogs(input ManagementLogQueryInput) (map[string]any, error) {
	keyNameMap, channelNameMap, authIndexChannelMap, ambiguousAuthIndexChannelMap, authMetaByIndex := s.buildNameMaps()
	authIndexes, channelNames, authIndexChannelNames := channelFilterSelectors(input.Channels, channelNameMap, authIndexChannelMap, ambiguousAuthIndexChannelMap, authMetaByIndex)

	params := usage.LogQueryParams{
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
	filters.ChannelOptions = enrichChannelFilterOptions(filters.ChannelOptions, channelNameMap, authIndexChannelMap, authMetaByIndex)
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
	return usage.ClearAllRequestLogs()
}

func (s *Service) ClearRequestLogs(options usage.ClearRequestLogsOptions) (int, any, error) {
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
	_, channelNameMap, authIndexChannelMap, ambiguousAuthIndexChannelMap, authMetaByIndex := s.buildNameMaps()
	authIndexes, channelNames, authIndexChannelNames := channelFilterSelectors(input.Channels, channelNameMap, authIndexChannelMap, ambiguousAuthIndexChannelMap, authMetaByIndex)
	params := usage.LogQueryParams{
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

	filters.ChannelOptions = enrichChannelFilterOptions(filters.ChannelOptions, channelNameMap, authIndexChannelMap, authMetaByIndex)
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
			appendAuthIndex(raw)
			if legacyChannels := ambiguousAuthIndexChannelMap[raw]; len(legacyChannels) > 0 {
				authIndexChannelNames[raw] = append(authIndexChannelNames[raw], legacyChannels...)
			}
			continue
		}
		if _, ok := authMetaByIndex[raw]; ok {
			appendAuthIndex(raw)
			continue
		}
		matchedAuthIndex := false
		for idx := range authIndexChannelMap {
			if strings.EqualFold(strings.TrimSpace(idx), raw) {
				appendAuthIndex(idx)
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
				appendAuthIndex(idx)
				matchedAuthIndex = true
			}
		}
		if matchedAuthIndex {
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
			appendAuthIndex(idx)
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

func enrichChannelFilterOptions(
	options []usage.ChannelFilterOption,
	channelNameMap, authIndexChannelMap map[string]string,
	authMetaByIndex map[string]authChannelMeta,
) []usage.ChannelFilterOption {
	if len(options) == 0 {
		return make([]usage.ChannelFilterOption, 0)
	}

	// Prefer one option per live auth_index. Collapse pure name-only rows when
	// the same label already has auth-backed options.
	out := make([]usage.ChannelFilterOption, 0, len(options))
	seenValue := make(map[string]struct{}, len(options))
	authBackedLabels := make(map[string]struct{})

	for _, option := range options {
		authIndex := strings.TrimSpace(option.AuthIndex)
		if authIndex == "" {
			authIndex = strings.TrimSpace(option.Value)
		}
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
	row := apikeysettings.NewService(nil).GetRow(apiKey)
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
) {
	keyNameMap = make(map[string]string)
	channelNameMap = make(map[string]string)
	authIndexChannelMap = make(map[string]string)
	ambiguousAuthIndexChannelMap = make(map[string][]string)
	authMetaByIndex = make(map[string]authChannelMeta)

	for _, row := range apikeysettings.NewService(nil).ListRows() {
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
		for _, auth := range s.authManager.List() {
			if auth == nil {
				continue
			}
			channel := strings.TrimSpace(auth.ChannelName())
			if channel == "" {
				continue
			}
			auth.EnsureIndex()
			idx := strings.TrimSpace(auth.Index)
			if idx != "" {
				authIndexChannelMap[idx] = channel
				authMetaByIndex[idx] = authChannelMeta{
					label:    channel,
					provider: normalizeProviderKey(auth.Provider),
					authType: resolveAuthType(auth),
				}
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
