package aiaccountstatus

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	codexauth "github.com/router-for-me/CLIProxyAPI/v6/internal/auth/codex"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	managementapitools "github.com/router-for-me/CLIProxyAPI/v6/internal/management/apitools"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	"github.com/tidwall/gjson"
)

// Server-fixed upstream endpoints (mirrors codeProxy quota-helpers.ts). Never accept client URLs.
const (
	codexUsageURL             = "https://chatgpt.com/backend-api/wham/usage"
	codexResetCreditsURL      = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits"
	claudeUsageURL            = "https://api.anthropic.com/api/oauth/usage"
	geminiCLIQuotaURL         = "https://cloudcode-pa.googleapis.com/v1internal:retrieveUserQuota"
	kimiUsageURL              = "https://api.kimi.com/coding/v1/usages"
	kiroQuotaURL              = "https://codewhisperer.us-east-1.amazonaws.com"
	xaiBillingWeeklyURL       = "https://cli-chat-proxy.grok.com/v1/billing?format=credits"
	xaiBillingMonthlyURL      = "https://cli-chat-proxy.grok.com/v1/billing"
	defaultAntigravityProject = "bamboo-precept-lgxtn"
)

var antigravityQuotaURLs = []string{
	"https://daily-cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels",
	"https://daily-cloudcode-pa.sandbox.googleapis.com/v1internal:fetchAvailableModels",
	"https://cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels",
}

// ProbeResult is the typed output of a provider-specific upstream status probe.
type ProbeResult struct {
	PlanType               string
	Quotas                 []usage.QuotaWindowDTO
	ResetCreditCount       *int64
	ResetCreditExpirations []string
	Health                 string
	Unsupported            bool
	UnsupportedReason      string
}

func probeAuth(ctx context.Context, svc *managementapitools.Service, cfg *config.Config, auth *coreauth.Auth) (ProbeResult, error) {
	if auth == nil {
		return ProbeResult{}, fmt.Errorf("auth is nil")
	}
	provider := normalizeProvider(auth.Provider)
	switch provider {
	case "codex":
		return probeCodex(ctx, svc, auth)
	case "claude", "anthropic":
		if !isClaudeOAuthLike(auth) {
			return ProbeResult{Unsupported: true, UnsupportedReason: "claude api-key accounts have no oauth usage probe"}, nil
		}
		return probeClaude(ctx, svc, auth)
	case "gemini-cli":
		return probeGeminiCLI(ctx, svc, auth)
	case "kimi":
		return probeKimi(ctx, svc, auth)
	case "kiro":
		return probeKiro(ctx, svc, auth)
	case "xai", "grok":
		return probeXAI(ctx, svc, auth)
	case "antigravity":
		return probeAntigravity(ctx, svc, auth)
	default:
		return ProbeResult{Unsupported: true, UnsupportedReason: "provider " + provider + " has no server-side status probe"}, nil
	}
}

func normalizeProvider(p string) string {
	p = strings.ToLower(strings.TrimSpace(p))
	p = strings.ReplaceAll(p, "_", "-")
	if p == "x-ai" || p == "grok" {
		return "xai"
	}
	return p
}

func isClaudeOAuthLike(auth *coreauth.Auth) bool {
	if auth == nil {
		return false
	}
	accountType, _ := auth.AccountInfo()
	accountType = strings.ToLower(strings.TrimSpace(accountType))
	if accountType == "api-key" || accountType == "apikey" || accountType == "api_key" {
		return false
	}
	return true
}

func probeCodex(ctx context.Context, svc *managementapitools.Service, auth *coreauth.Auth) (ProbeResult, error) {
	body, err := doAuthGET(ctx, svc, auth, codexUsageURL, map[string]string{
		"Content-Type": "application/json",
		"User-Agent":   "codex_cli_rs/0.76.0 (Debian 13.0.0; x86_64) WindowsTerminal",
	}, func(req *http.Request) {
		if accountID := codexAccountID(auth); accountID != "" {
			req.Header.Set("Chatgpt-Account-Id", accountID)
		}
	})
	if err != nil {
		return ProbeResult{}, err
	}
	_ = svc.ReconcileCodexWhamUsagePlan(ctx, auth, parseURL(codexUsageURL), 200, body)

	root := gjson.ParseBytes(body)
	result := ProbeResult{
		PlanType: strings.ToLower(strings.TrimSpace(firstJSONResult(root, "plan_type", "planType").String())),
		Quotas:   parseCodexWhamQuotas(body),
	}
	credits := firstJSONResult(root, "rate_limit_reset_credits", "rateLimitResetCredits")
	if count := firstJSONResult(credits, "available_count", "availableCount"); count.Exists() {
		v := count.Int()
		result.ResetCreditCount = &v
		if v > 0 {
			if expBody, expErr := doAuthGET(ctx, svc, auth, codexResetCreditsURL, map[string]string{
				"Content-Type": "application/json",
				"User-Agent":   "codex_cli_rs/0.76.0 (Debian 13.0.0; x86_64) WindowsTerminal",
			}, func(req *http.Request) {
				if accountID := codexAccountID(auth); accountID != "" {
					req.Header.Set("Chatgpt-Account-Id", accountID)
				}
			}); expErr == nil {
				result.ResetCreditExpirations = parseCodexResetExpirations(expBody)
			}
		}
	}
	return result, nil
}

func parseCodexWhamQuotas(body []byte) []usage.QuotaWindowDTO {
	root := gjson.ParseBytes(body)
	items := make([]usage.QuotaWindowDTO, 0, 12)
	appendLimit := func(limit gjson.Result, prefix string, includeNonStandard bool) {
		if !limit.Exists() {
			return
		}
		windows := codexRateLimitWindows(limit)
		var fiveHour, weekly gjson.Result
		for _, window := range windows {
			switch codexWindowSeconds(window) {
			case 18000:
				if !fiveHour.Exists() {
					fiveHour = window
				}
			case 604800:
				if !weekly.Exists() {
					weekly = window
				}
			}
		}
		if fiveHour.Exists() {
			label := "m_quota.code_5h"
			if prefix == "review" {
				label = "m_quota.review_5h"
			}
			items = append(items, codexWindowDTO(limit, fiveHour, prefix+"_5h", label, 18000))
		}
		if weekly.Exists() {
			label := "m_quota.code_weekly"
			if prefix == "review" {
				label = "m_quota.review_weekly"
			}
			items = append(items, codexWindowDTO(limit, weekly, prefix+"_week", label, 604800))
		}
		if !includeNonStandard {
			return
		}
		for _, window := range windows {
			seconds := codexWindowSeconds(window)
			if seconds <= 0 || seconds == 18000 || seconds == 604800 {
				continue
			}
			label := "m_quota.code_subscription"
			if prefix == "review" {
				label = "m_quota.review_subscription"
			}
			key := fmt.Sprintf("%s_subscription_%d", prefix, seconds)
			items = append(items, codexWindowDTO(limit, window, key, label, seconds))
		}
	}

	rateLimit := firstJSONResult(root, "rate_limit", "rateLimit")
	appendLimit(rateLimit, "code", true)
	appendLimit(firstJSONResult(root, "code_review_rate_limit", "codeReviewRateLimit"), "review", true)

	additional := firstJSONResult(root, "additional_rate_limits", "additionalRateLimits")
	if additional.IsArray() {
		additional.ForEach(func(_, entry gjson.Result) bool {
			limit := firstJSONResult(entry, "rate_limit", "rateLimit")
			if !limit.Exists() {
				return true
			}
			name := strings.TrimSpace(firstJSONResult(entry, "limit_name", "limitName").String())
			if name == "" {
				name = "Additional Codex quota"
			}
			keyPart := strings.TrimSpace(firstJSONResult(entry, "metered_feature", "meteredFeature").String())
			if keyPart == "" {
				if strings.EqualFold(name, "gpt-5.3-codex-spark") {
					keyPart = "codex_bengalfox"
				} else {
					keyPart = normalizeQuotaKeyPart(name)
				}
			} else {
				keyPart = normalizeQuotaKeyPart(keyPart)
			}
			if keyPart == "" {
				keyPart = "additional"
			}
			for _, window := range codexRateLimitWindows(limit) {
				seconds := codexWindowSeconds(window)
				suffix, label := "", ""
				switch seconds {
				case 18000:
					suffix, label = "5h", name+": 5h"
				case 604800:
					suffix, label = "week", name+": Weekly"
				default:
					continue
				}
				items = append(items, codexWindowDTO(limit, window, "additional:"+keyPart+":"+suffix, label, seconds))
			}
			return true
		})
	}
	return items
}

func codexRateLimitWindows(limit gjson.Result) []gjson.Result {
	windows := make([]gjson.Result, 0, 2)
	for _, paths := range [][2]string{{"primary_window", "primaryWindow"}, {"secondary_window", "secondaryWindow"}} {
		if window := firstJSONResult(limit, paths[0], paths[1]); window.Exists() {
			windows = append(windows, window)
		}
	}
	return windows
}

func codexWindowSeconds(window gjson.Result) int64 {
	return firstJSONResult(window, "limit_window_seconds", "limitWindowSeconds").Int()
}

func codexWindowDTO(limit, window gjson.Result, key, label string, seconds int64) usage.QuotaWindowDTO {
	dto := usage.QuotaWindowDTO{QuotaKey: key, QuotaLabel: label, WindowSeconds: seconds}
	if used := firstJSONResult(window, "used_percent", "usedPercent"); used.Exists() {
		remaining := 100 - clampPct(used.Float())
		dto.Percent = &remaining
	} else {
		limitReached := firstJSONResult(limit, "limit_reached", "limitReached")
		allowed := limit.Get("allowed")
		if (limitReached.Exists() && limitReached.Bool()) || (allowed.Exists() && !allowed.Bool()) {
			remaining := 0.0
			dto.Percent = &remaining
		}
	}
	if resetAt := firstJSONResult(window, "reset_at", "resetAt"); resetAt.Exists() && resetAt.Int() > 0 {
		t := time.Unix(resetAt.Int(), 0).UTC()
		dto.ResetAt = &t
	} else if after := firstJSONResult(window, "reset_after_seconds", "resetAfterSeconds"); after.Exists() && after.Float() > 0 {
		t := time.Now().UTC().Add(time.Duration(after.Float() * float64(time.Second)))
		dto.ResetAt = &t
	}
	return dto
}

func normalizeQuotaKeyPart(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		isAlphaNum := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if isAlphaNum {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if b.Len() > 0 && !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func parseCodexResetExpirations(body []byte) []string {
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}
	seen := make(map[string]struct{})
	values := make([]string, 0)
	var walk func(any, int)
	walk = func(value any, depth int) {
		if depth > 6 || value == nil {
			return
		}
		switch typed := value.(type) {
		case []any:
			for _, item := range typed {
				walk(item, depth+1)
			}
		case map[string]any:
			for _, key := range []string{"expires_at", "expiresAt"} {
				if raw, ok := typed[key].(string); ok {
					value := strings.TrimSpace(raw)
					if value != "" {
						if _, exists := seen[value]; !exists {
							seen[value] = struct{}{}
							values = append(values, value)
						}
					}
				}
			}
			for _, child := range typed {
				walk(child, depth+1)
			}
		}
	}
	walk(payload, 0)
	sort.SliceStable(values, func(i, j int) bool {
		left, leftErr := time.Parse(time.RFC3339, values[i])
		right, rightErr := time.Parse(time.RFC3339, values[j])
		if leftErr != nil || rightErr != nil {
			return values[i] < values[j]
		}
		return left.Before(right)
	})
	return values
}

func probeClaude(ctx context.Context, svc *managementapitools.Service, auth *coreauth.Auth) (ProbeResult, error) {
	body, err := doAuthGET(ctx, svc, auth, claudeUsageURL, map[string]string{
		"Accept":         "application/json, text/plain, */*",
		"Content-Type":   "application/json",
		"User-Agent":     "claude-code/2.1.7",
		"anthropic-beta": "oauth-2025-04-20",
	}, nil)
	if err != nil {
		return ProbeResult{}, err
	}
	return ProbeResult{Quotas: parseClaudeUsage(body)}, nil
}

func parseClaudeUsage(body []byte) []usage.QuotaWindowDTO {
	keys := []struct {
		path, key, label string
		window           int64
	}{
		{"five_hour", "five_hour", "claude_quota.five_hour", 18000},
		{"seven_day", "seven_day", "claude_quota.seven_day", 604800},
		{"seven_day_oauth_apps", "seven_day_oauth_apps", "claude_quota.seven_day_oauth_apps", 604800},
		{"seven_day_opus", "seven_day_opus", "claude_quota.seven_day_opus", 604800},
		{"seven_day_sonnet", "seven_day_sonnet", "claude_quota.seven_day_sonnet", 604800},
		{"seven_day_cowork", "seven_day_cowork", "claude_quota.seven_day_cowork", 604800},
		{"iguana_necktie", "iguana_necktie", "claude_quota.iguana_necktie", 0},
	}
	out := make([]usage.QuotaWindowDTO, 0, len(keys)+1)
	root := gjson.ParseBytes(body)
	for _, k := range keys {
		win := root.Get(k.path)
		if !win.Exists() {
			continue
		}
		dto := usage.QuotaWindowDTO{QuotaKey: k.key, QuotaLabel: k.label, WindowSeconds: k.window}
		if utilPct := win.Get("utilization"); utilPct.Exists() {
			remaining := 100 - clampPct(utilPct.Float())
			dto.Percent = &remaining
		}
		if reset := firstJSONResult(win, "resets_at", "resetsAt"); reset.Exists() {
			dto.ResetAt = parseFlexibleTime(reset)
		}
		if dto.Percent != nil || dto.ResetAt != nil {
			out = append(out, dto)
		}
	}
	extra := root.Get("extra_usage")
	if extra.Exists() && extra.Get("is_enabled").Bool() {
		if utilization := extra.Get("utilization"); utilization.Exists() {
			remaining := 100 - clampPct(utilization.Float())
			used := strings.TrimSpace(extra.Get("used_credits").String())
			limit := strings.TrimSpace(extra.Get("monthly_limit").String())
			meta := ""
			if used != "" && limit != "" {
				meta = used + " / " + limit + " credits"
			}
			out = append(out, usage.QuotaWindowDTO{
				QuotaKey: "extra_usage", QuotaLabel: "claude_quota.extra_usage_label", Percent: &remaining, Meta: meta,
			})
		}
	}
	return out
}

func probeGeminiCLI(ctx context.Context, svc *managementapitools.Service, auth *coreauth.Auth) (ProbeResult, error) {
	projectID := firstNonEmpty(
		authString(auth, "project_id", "projectId", "gemini_virtual_project"),
		metadataNestedString(auth, "installed", "project_id", "projectId"),
	)
	if projectID == "" {
		return ProbeResult{Unsupported: true, UnsupportedReason: "missing_project_id"}, nil
	}
	payload, err := json.Marshal(map[string]string{"project": projectID})
	if err != nil {
		return ProbeResult{}, fmt.Errorf("encode gemini project: %w", err)
	}
	body, err := doAuthPOST(ctx, svc, auth, geminiCLIQuotaURL, map[string]string{
		"Content-Type": "application/json",
	}, string(payload))
	if err != nil {
		return ProbeResult{}, err
	}
	return ProbeResult{Quotas: parseGeminiCLIQuota(body)}, nil
}

type geminiQuotaBucket struct {
	modelID           string
	tokenType         string
	remainingFraction *float64
	remainingAmount   *float64
	resetAt           *time.Time
}

type geminiQuotaGroup struct {
	id               string
	label            string
	preferredModelID string
	modelIDs         []string
}

var geminiQuotaGroups = []geminiQuotaGroup{
	{id: "gemini-2.5-pro", label: "Gemini 2.5 Pro", preferredModelID: "gemini-2.5-pro", modelIDs: []string{"gemini-2.5-pro", "gemini-2.5-pro-preview"}},
	{id: "gemini-2.5-flash", label: "Gemini 2.5 Flash", preferredModelID: "gemini-2.5-flash", modelIDs: []string{"gemini-2.5-flash", "gemini-2.5-flash-preview"}},
	{id: "gemini-2.5-flash-lite", label: "Gemini 2.5 Flash Lite", preferredModelID: "gemini-2.5-flash-lite", modelIDs: []string{"gemini-2.5-flash-lite"}},
	{id: "gemini-1.5-pro", label: "Gemini 1.5 Pro", preferredModelID: "gemini-1.5-pro", modelIDs: []string{"gemini-1.5-pro", "gemini-1.5-pro-latest"}},
	{id: "gemini-1.5-flash", label: "Gemini 1.5 Flash", preferredModelID: "gemini-1.5-flash", modelIDs: []string{"gemini-1.5-flash", "gemini-1.5-flash-latest"}},
}

func parseGeminiCLIQuota(body []byte) []usage.QuotaWindowDTO {
	parsed := make([]geminiQuotaBucket, 0)
	gjson.GetBytes(body, "buckets").ForEach(func(_, raw gjson.Result) bool {
		modelID := strings.TrimSpace(firstJSONResult(raw, "modelId", "model_id").String())
		if strings.HasPrefix(modelID, "projects/") {
			parts := strings.SplitN(modelID, "/", 3)
			if len(parts) == 3 {
				modelID = strings.TrimSpace(parts[2])
			}
		}
		if modelID == "" || strings.HasPrefix(modelID, "gemini-2.0-flash") {
			return true
		}
		bucket := geminiQuotaBucket{
			modelID:   modelID,
			tokenType: strings.TrimSpace(firstJSONResult(raw, "tokenType", "token_type").String()),
			resetAt:   parseFlexibleTime(firstJSONResult(raw, "resetTime", "reset_time")),
		}
		if fraction := firstJSONResult(raw, "remainingFraction", "remaining_fraction"); fraction.Exists() {
			value := quotaFraction(fraction)
			if value != nil {
				bucket.remainingFraction = value
			}
		}
		if amount := firstJSONResult(raw, "remainingAmount", "remaining_amount"); amount.Exists() {
			value := amount.Float()
			bucket.remainingAmount = &value
		}
		if bucket.remainingFraction == nil {
			if bucket.remainingAmount != nil && *bucket.remainingAmount <= 0 || bucket.remainingAmount == nil && bucket.resetAt != nil {
				zero := 0.0
				bucket.remainingFraction = &zero
			}
		}
		parsed = append(parsed, bucket)
		return true
	})

	type groupedBucket struct {
		id, label, tokenType, preferredModelID string
		preferred, fallback                    *geminiQuotaBucket
		order                                  int
	}
	groups := make(map[string]*groupedBucket)
	for i := range parsed {
		bucket := &parsed[i]
		groupID, label, preferred, order := bucket.modelID, bucket.modelID, "", len(geminiQuotaGroups)+1
		for groupIndex, definition := range geminiQuotaGroups {
			for _, modelID := range definition.modelIDs {
				if bucket.modelID == modelID {
					groupID, label, preferred, order = definition.id, definition.label, definition.preferredModelID, groupIndex
					break
				}
			}
			if groupID == definition.id {
				break
			}
		}
		key := groupID + "|" + bucket.tokenType
		group := groups[key]
		if group == nil {
			group = &groupedBucket{id: groupID, label: label, tokenType: bucket.tokenType, preferredModelID: preferred, order: order}
			groups[key] = group
		}
		if group.fallback == nil || group.fallback.remainingFraction == nil && bucket.remainingFraction != nil {
			group.fallback = bucket
		}
		if bucket.modelID == group.preferredModelID {
			group.preferred = bucket
		}
	}
	ordered := make([]*groupedBucket, 0, len(groups))
	for _, group := range groups {
		ordered = append(ordered, group)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].order != ordered[j].order {
			return ordered[i].order < ordered[j].order
		}
		if ordered[i].id != ordered[j].id {
			return ordered[i].id < ordered[j].id
		}
		return ordered[i].tokenType < ordered[j].tokenType
	})

	out := make([]usage.QuotaWindowDTO, 0, len(ordered))
	for _, group := range ordered {
		bucket := group.preferred
		if bucket == nil {
			bucket = group.fallback
		}
		if bucket == nil {
			continue
		}
		dto := usage.QuotaWindowDTO{QuotaKey: "model:" + group.id, QuotaLabel: group.label, ResetAt: bucket.resetAt}
		if group.tokenType != "" {
			dto.QuotaKey += ":" + normalizeQuotaKeyPart(group.tokenType)
		}
		if bucket.remainingFraction != nil {
			remaining := math.Round(clampPct(*bucket.remainingFraction * 100))
			dto.Percent = &remaining
		}
		meta := make([]string, 0, 2)
		if group.tokenType != "" {
			meta = append(meta, "tokenType="+group.tokenType)
		}
		if bucket.remainingAmount != nil {
			meta = append(meta, fmt.Sprintf("%.0f tokens", *bucket.remainingAmount))
		}
		dto.Meta = strings.Join(meta, " · ")
		out = append(out, dto)
	}
	return out
}

func probeKimi(ctx context.Context, svc *managementapitools.Service, auth *coreauth.Auth) (ProbeResult, error) {
	body, err := doAuthGET(ctx, svc, auth, kimiUsageURL, nil, nil)
	if err != nil {
		return ProbeResult{}, err
	}
	return ProbeResult{Quotas: parseKimiUsage(body)}, nil
}

func parseKimiUsage(body []byte) []usage.QuotaWindowDTO {
	root := gjson.ParseBytes(body)
	source := root
	if !root.Get("usage").Exists() && !root.Get("limits").Exists() {
		usages := root.Get("usages")
		if !usages.IsArray() {
			return nil
		}
		source = gjson.Result{}
		usages.ForEach(func(_, entry gjson.Result) bool {
			if !source.Exists() {
				source = entry
			}
			if strings.EqualFold(strings.TrimSpace(entry.Get("scope").String()), "FEATURE_CODING") {
				source = entry
				return false
			}
			return true
		})
		if !source.Exists() {
			return nil
		}
	}

	var fiveHourDetail, weeklyDetail gjson.Result
	limits := source.Get("limits")
	if limits.IsArray() {
		limits.ForEach(func(_, limit gjson.Result) bool {
			switch kimiWindowMinutes(limit.Get("window")) {
			case 300:
				if !fiveHourDetail.Exists() {
					fiveHourDetail = limit.Get("detail")
				}
			case 7 * 24 * 60:
				if !weeklyDetail.Exists() {
					weeklyDetail = limit.Get("detail")
				}
			}
			return true
		})
	}
	if detail := source.Get("usage"); detail.Exists() {
		weeklyDetail = detail
	} else if detail := source.Get("detail"); detail.Exists() {
		weeklyDetail = detail
	}

	out := make([]usage.QuotaWindowDTO, 0, 2)
	if dto, ok := kimiDetailDTO("code_5h", "m_quota.code_5h", 18000, fiveHourDetail); ok {
		out = append(out, dto)
	}
	if dto, ok := kimiDetailDTO("code_week", "m_quota.code_weekly", 604800, weeklyDetail); ok {
		out = append(out, dto)
	}
	return out
}

func kimiWindowMinutes(window gjson.Result) int64 {
	if !window.Exists() {
		return 0
	}
	duration := window.Get("duration").Float()
	if duration <= 0 {
		return 0
	}
	unit := strings.ToUpper(strings.TrimSpace(firstJSONResult(window, "timeUnit", "time_unit").String()))
	switch unit {
	case "", "TIME_UNIT_MINUTE":
		return int64(duration)
	case "TIME_UNIT_HOUR":
		return int64(duration * 60)
	case "TIME_UNIT_DAY":
		return int64(duration * 24 * 60)
	case "TIME_UNIT_WEEK":
		return int64(duration * 7 * 24 * 60)
	default:
		return 0
	}
}

func kimiDetailDTO(key, label string, windowSeconds int64, detail gjson.Result) (usage.QuotaWindowDTO, bool) {
	if !detail.Exists() {
		return usage.QuotaWindowDTO{}, false
	}
	dto := usage.QuotaWindowDTO{QuotaKey: key, QuotaLabel: label, WindowSeconds: windowSeconds}
	limit := detail.Get("limit")
	if limit.Exists() {
		limitValue := limit.Float()
		if limitValue <= 0 {
			remaining := 0.0
			dto.Percent = &remaining
		} else if remainingAmount := detail.Get("remaining"); remainingAmount.Exists() {
			remaining := math.Round(clampPct((remainingAmount.Float() / limitValue) * 100))
			dto.Percent = &remaining
		} else if used := detail.Get("used"); used.Exists() {
			remaining := math.Round(clampPct(((limitValue - used.Float()) / limitValue) * 100))
			dto.Percent = &remaining
		}
	}
	dto.ResetAt = parseFlexibleTime(firstJSONResult(detail, "resetTime", "reset_time"))
	return dto, dto.Percent != nil || dto.ResetAt != nil
}

func probeKiro(ctx context.Context, svc *managementapitools.Service, auth *coreauth.Auth) (ProbeResult, error) {
	body, err := doAuthPOST(ctx, svc, auth, kiroQuotaURL, map[string]string{
		"Content-Type": "application/x-amz-json-1.0",
		"x-amz-target": "AmazonCodeWhispererService.GetUsageLimits",
	}, `{"origin":"AI_EDITOR","resourceType":"AGENTIC_REQUEST"}`)
	if err != nil {
		return ProbeResult{}, err
	}
	return ProbeResult{Quotas: parseKiroQuota(body)}, nil
}

func parseKiroQuota(body []byte) []usage.QuotaWindowDTO {
	root := gjson.ParseBytes(body)
	out := make([]usage.QuotaWindowDTO, 0, 3)
	if subscription := strings.TrimSpace(root.Get("subscriptionInfo.subscriptionTitle").String()); subscription != "" {
		out = append(out, usage.QuotaWindowDTO{
			QuotaKey: "subscription", QuotaLabel: "m_quota.subscription", Meta: subscription, Value: subscription,
		})
	}
	usage0 := root.Get("usageBreakdownList.0")
	if !usage0.Exists() {
		return out
	}
	if limit, used := usage0.Get("usageLimitWithPrecision"), usage0.Get("currentUsageWithPrecision"); limit.Exists() && used.Exists() {
		remaining := 0.0
		if limit.Float() > 0 {
			remaining = math.Round(clampPct(((limit.Float() - used.Float()) / limit.Float()) * 100))
		}
		dto := usage.QuotaWindowDTO{
			QuotaKey: "base_quota", QuotaLabel: "m_quota.base_quota", Percent: &remaining,
			Meta: fmt.Sprintf("used %.0f / limit %.0f", used.Float(), limit.Float()),
		}
		reset := usage0.Get("nextDateReset")
		if !reset.Exists() {
			reset = root.Get("nextDateReset")
		}
		dto.ResetAt = parseFlexibleTime(reset)
		out = append(out, dto)
	}
	trial := usage0.Get("freeTrialInfo")
	if trial.Exists() {
		limit, used := trial.Get("usageLimitWithPrecision"), trial.Get("currentUsageWithPrecision")
		if limit.Exists() && used.Exists() {
			remaining := 0.0
			if limit.Float() > 0 {
				remaining = math.Round(clampPct(((limit.Float() - used.Float()) / limit.Float()) * 100))
			}
			status := strings.TrimSpace(trial.Get("freeTrialStatus").String())
			if status == "" {
				status = "trial"
			}
			dto := usage.QuotaWindowDTO{
				QuotaKey: "trial_quota", QuotaLabel: "m_quota.trial_quota", Percent: &remaining,
				Meta:    fmt.Sprintf("%s · used %.0f / limit %.0f", status, used.Float(), limit.Float()),
				ResetAt: parseFlexibleTime(trial.Get("freeTrialExpiry")),
			}
			out = append(out, dto)
		}
	}
	return out
}

func probeXAI(ctx context.Context, svc *managementapitools.Service, auth *coreauth.Auth) (ProbeResult, error) {
	headers := map[string]string{
		"x-xai-token-auth":      "xai-grok-cli",
		"x-grok-client-version": "0.2.91",
		"accept":                "*/*",
		"user-agent":            "grok-pager/0.2.91 grok-shell/0.2.91 (macos; aarch64)",
	}
	if userID := resolveXAIUserID(auth); userID != "" {
		headers["x-userid"] = userID
	}
	// Parallel weekly/monthly under the same probe slot (does not take extra global semaphore).
	weeklyBody, weeklyErr, monthlyBody, monthlyErr := fetchXAIBillingParallel(ctx,
		func(ctx context.Context) ([]byte, error) {
			return doAuthGET(ctx, svc, auth, xaiBillingWeeklyURL, headers, nil)
		},
		func(ctx context.Context) ([]byte, error) {
			return doAuthGET(ctx, svc, auth, xaiBillingMonthlyURL, headers, nil)
		},
	)
	if weeklyErr != nil && monthlyErr != nil {
		return ProbeResult{}, weeklyErr
	}

	quotas := make([]usage.QuotaWindowDTO, 0, 8)
	if weeklyErr == nil {
		quotas = append(quotas, parseXAIWeeklyBilling(weeklyBody)...)
	} else if monthlyErr == nil {
		quotas = append(quotas, parseXAIWeeklyBilling(monthlyBody)...)
	}
	if monthlyErr == nil {
		quotas = append(quotas, parseXAIMonthlyBilling(monthlyBody)...)
	} else if weeklyErr == nil {
		quotas = append(quotas, parseXAIMonthlyBilling(weeklyBody)...)
	}
	if len(quotas) == 0 {
		return ProbeResult{}, fmt.Errorf("empty_data")
	}
	planBody := monthlyBody
	if monthlyErr != nil {
		planBody = weeklyBody
	}
	return ProbeResult{Quotas: quotas, PlanType: resolveXAIPlan(planBody)}, nil
}

// fetchXAIBillingParallel runs weekly and monthly fetches concurrently and
// preserves partial-success merge semantics (one side may fail).
func fetchXAIBillingParallel(ctx context.Context, fetchWeekly, fetchMonthly func(context.Context) ([]byte, error)) (weeklyBody []byte, weeklyErr error, monthlyBody []byte, monthlyErr error) {
	type getResult struct {
		body []byte
		err  error
	}
	weeklyCh := make(chan getResult, 1)
	monthlyCh := make(chan getResult, 1)
	go func() {
		if fetchWeekly == nil {
			weeklyCh <- getResult{err: fmt.Errorf("weekly fetch unavailable")}
			return
		}
		body, err := fetchWeekly(ctx)
		weeklyCh <- getResult{body: body, err: err}
	}()
	go func() {
		if fetchMonthly == nil {
			monthlyCh <- getResult{err: fmt.Errorf("monthly fetch unavailable")}
			return
		}
		body, err := fetchMonthly(ctx)
		monthlyCh <- getResult{body: body, err: err}
	}()
	weekly := <-weeklyCh
	monthly := <-monthlyCh
	return weekly.body, weekly.err, monthly.body, monthly.err
}

func resolveXAIUserID(auth *coreauth.Auth) string {
	return firstNonEmpty(
		authString(auth, "sub", "subject", "user_id", "userId", "x_userid"),
		metadataNestedString(auth, "oauth", "sub", "subject", "user_id", "userId"),
		metadataNestedString(auth, "user", "sub", "subject", "id", "user_id", "userId"),
	)
}

func parseXAIBilling(body []byte, key, _ string, _ int64) []usage.QuotaWindowDTO {
	if key == "weekly_limit" {
		return parseXAIWeeklyBilling(body)
	}
	return parseXAIMonthlyBilling(body)
}

func parseXAIWeeklyBilling(body []byte) []usage.QuotaWindowDTO {
	cfg := gjson.GetBytes(body, "config")
	if !cfg.Exists() {
		return nil
	}
	current := firstJSONResult(cfg, "currentPeriod", "current_period")
	periodType := strings.ToLower(strings.TrimSpace(current.Get("type").String()))
	used := firstJSONResult(cfg, "creditUsagePercent", "credit_usage_percent")
	products := firstJSONResult(cfg, "productUsage", "product_usage")
	if !used.Exists() && !strings.Contains(periodType, "weekly") && !products.IsArray() {
		return nil
	}

	remaining := 100.0
	if used.Exists() {
		remaining = math.Round(100 - clampPct(used.Float()))
	}
	reset := firstJSONResult(current, "end")
	if !reset.Exists() {
		reset = firstJSONResult(cfg, "billingPeriodEnd", "billing_period_end")
	}
	start := strings.TrimSpace(firstJSONResult(current, "start").String())
	if start == "" {
		start = strings.TrimSpace(firstJSONResult(cfg, "billingPeriodStart", "billing_period_start").String())
	}
	end := strings.TrimSpace(reset.String())
	meta := ""
	if start != "" && end != "" {
		meta = start + " - " + end
	} else {
		meta = firstNonEmpty(end, start)
	}
	out := []usage.QuotaWindowDTO{{
		QuotaKey: "weekly_limit", QuotaLabel: "xai_quota.weekly_limit", Percent: &remaining,
		Value: formatPercent(remaining), ResetAt: parseFlexibleTime(reset), WindowSeconds: 604800, Meta: meta,
	}}
	if products.IsArray() {
		index := 0
		products.ForEach(func(_, product gjson.Result) bool {
			index++
			name := strings.TrimSpace(product.Get("product").String())
			if name == "" {
				name = fmt.Sprintf("Product %d", index)
			}
			productRemaining := 100.0
			if productUsed := firstJSONResult(product, "usagePercent", "usage_percent"); productUsed.Exists() {
				productRemaining = math.Round(100 - clampPct(productUsed.Float()))
			}
			out = append(out, usage.QuotaWindowDTO{
				QuotaKey: "product:" + name, QuotaLabel: "xai_quota.product_usage_named::" + name,
				Percent: &productRemaining, Value: formatPercent(productRemaining),
			})
			return true
		})
	}
	return out
}

func parseXAIMonthlyBilling(body []byte) []usage.QuotaWindowDTO {
	cfg := gjson.GetBytes(body, "config")
	if !cfg.Exists() {
		return nil
	}
	monthlyLimit, hasMonthlyLimit := xaiCentValue(cfg, "monthlyLimit", "monthly_limit")
	used, hasUsed := xaiCentValue(cfg, "used")
	onDemandCap, hasOnDemandCap := xaiCentValue(cfg, "onDemandCap", "on_demand_cap")
	onDemandUsed, hasOnDemandUsed := xaiCentValue(cfg, "onDemandUsed", "on_demand_used")
	billingEnd := firstJSONResult(cfg, "billingPeriodEnd", "billing_period_end")
	hasMonthlyData := hasMonthlyLimit || hasUsed || hasOnDemandCap || billingEnd.Exists()
	if !hasMonthlyData {
		return nil
	}

	if !hasOnDemandUsed && hasUsed && hasMonthlyLimit {
		onDemandUsed = math.Max(0, used-monthlyLimit)
		hasOnDemandUsed = true
	}
	out := make([]usage.QuotaWindowDTO, 0, 2)
	payGoRemaining := 100.0
	payGoMeta := ""
	if hasOnDemandCap && onDemandCap > 0 {
		if hasOnDemandUsed {
			payGoRemaining = math.Round(100 - clampPct((onDemandUsed/onDemandCap)*100))
		}
		remainingCents := 0.0
		if hasOnDemandUsed {
			remainingCents = math.Max(0, onDemandCap-onDemandUsed)
		}
		payGoMeta = formatUSDCents(remainingCents) + " / " + formatUSDCents(onDemandCap)
	}
	out = append(out, usage.QuotaWindowDTO{
		QuotaKey: "pay_as_you_go", QuotaLabel: "xai_quota.pay_as_you_go_label",
		Percent: &payGoRemaining, Value: formatPercent(payGoRemaining), Meta: payGoMeta,
	})

	if hasMonthlyLimit || hasUsed || billingEnd.Exists() {
		includedUsed := used
		if hasUsed && hasMonthlyLimit && monthlyLimit > 0 {
			includedUsed = math.Min(used, monthlyLimit)
		}
		monthlyRemaining := 100.0
		if hasMonthlyLimit && monthlyLimit > 0 && hasUsed {
			monthlyRemaining = math.Round(100 - clampPct((includedUsed/monthlyLimit)*100))
		}
		remainingCents := 0.0
		if hasMonthlyLimit && hasUsed {
			remainingCents = math.Max(0, monthlyLimit-includedUsed)
		}
		meta := ""
		if hasMonthlyLimit {
			meta = formatUSDCents(remainingCents) + " / " + formatUSDCents(monthlyLimit)
		}
		out = append(out, usage.QuotaWindowDTO{
			QuotaKey: "monthly_credits", QuotaLabel: "xai_quota.monthly_credits",
			Percent: &monthlyRemaining, Value: formatPercent(monthlyRemaining), ResetAt: parseFlexibleTime(billingEnd), Meta: meta,
		})
	}
	return out
}

func xaiCentValue(cfg gjson.Result, paths ...string) (float64, bool) {
	value := firstJSONResult(cfg, paths...)
	if !value.Exists() {
		return 0, false
	}
	if value.IsObject() {
		value = value.Get("val")
	}
	if !value.Exists() {
		return 0, false
	}
	return value.Float(), true
}

func resolveXAIPlan(body []byte) string {
	cfg := gjson.GetBytes(body, "config")
	limit, ok := xaiCentValue(cfg, "monthlyLimit", "monthly_limit")
	if !ok {
		return ""
	}
	switch int64(math.Round(limit)) {
	case 15000:
		return "supergrok"
	case 150000:
		return "supergrok-heavy"
	default:
		return ""
	}
}

func formatPercent(percent float64) string {
	return fmt.Sprintf("%.0f%%", math.Round(clampPct(percent)))
}

func formatUSDCents(cents float64) string {
	return fmt.Sprintf("$%.2f", cents/100)
}

func probeAntigravity(ctx context.Context, svc *managementapitools.Service, auth *coreauth.Auth) (ProbeResult, error) {
	projectID := firstNonEmpty(
		metadataString(auth, "project_id", "projectId"),
		metadataNestedString(auth, "installed", "project_id", "projectId"),
		metadataNestedString(auth, "web", "project_id", "projectId"),
		defaultAntigravityProject,
	)
	payloadJSON, err := json.Marshal(map[string]string{"project": projectID})
	if err != nil {
		return ProbeResult{}, fmt.Errorf("encode antigravity project: %w", err)
	}
	payload := string(payloadJSON)
	var lastErr error
	for _, url := range antigravityQuotaURLs {
		body, err := doAuthPOST(ctx, svc, auth, url, map[string]string{
			"Content-Type": "application/json",
			"User-Agent":   "antigravity/1.11.5 windows/amd64",
		}, payload)
		if err != nil {
			lastErr = err
			continue
		}
		quotas := parseAntigravityModels(body)
		if len(quotas) == 0 {
			lastErr = fmt.Errorf("no_model_quota")
			continue
		}
		return ProbeResult{Quotas: quotas}, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("request_failed")
	}
	return ProbeResult{}, lastErr
}

type antigravityQuotaGroup struct {
	key, label string
	models     map[string]struct{}
}

var antigravityQuotaGroups = []antigravityQuotaGroup{
	{key: "provider:gemini3-pro", label: "antigravity_quota.gemini3_pro", models: stringSet("gemini-3-pro-low", "gemini-3-pro-high", "gemini-3-pro-preview", "gemini-3.1-pro-low", "gemini-3.1-pro-high", "gemini-3.1-pro-preview")},
	{key: "provider:gemini3-flash", label: "antigravity_quota.gemini3_flash", models: stringSet("gemini-3-flash", "gemini-3-flash-agent")},
	{key: "provider:gemini-image", label: "antigravity_quota.gemini_image", models: stringSet("gemini-2.5-flash-image", "gemini-3.1-flash-image", "gemini-3-pro-image", "gemini-3-pro-image-preview")},
	{key: "provider:claude", label: "antigravity_quota.claude", models: stringSet("claude-fable-5", "claude-sonnet-4-5", "claude-sonnet-4-5-thinking", "claude-opus-4-5-thinking", "claude-sonnet-4-6", "claude-opus-4-6", "claude-opus-4-6-thinking", "claude-opus-4-7", "claude-opus-4-8")},
}

var antigravitySkippedModels = stringSet(
	"chat_20706", "chat_23310", "tab_flash_lite_preview", "tab_jump_flash_lite_preview",
	"gemini-2.5-flash-thinking", "gemini-2.5-pro",
)

func parseAntigravityModels(body []byte) []usage.QuotaWindowDTO {
	root := gjson.ParseBytes(body)
	models := root.Get("models")
	if !models.IsObject() && root.IsObject() {
		models = root
	}
	if !models.IsObject() {
		return nil
	}
	type groupValue struct {
		percent *float64
		resetAt *time.Time
		count   int
	}
	grouped := make(map[string]*groupValue, len(antigravityQuotaGroups))
	models.ForEach(func(modelID, model gjson.Result) bool {
		id := strings.ToLower(strings.TrimSpace(modelID.String()))
		id = strings.TrimPrefix(id, "models/")
		if id == "" {
			return true
		}
		if _, skip := antigravitySkippedModels[id]; skip {
			return true
		}
		var group *antigravityQuotaGroup
		for i := range antigravityQuotaGroups {
			if _, ok := antigravityQuotaGroups[i].models[id]; ok {
				group = &antigravityQuotaGroups[i]
				break
			}
		}
		if group == nil {
			return true
		}
		info := firstJSONResult(model, "quotaInfo", "quota_info")
		fraction := firstJSONResult(info, "remainingFraction", "remaining_fraction", "remaining")
		resetAt := parseFlexibleTime(firstJSONResult(info, "resetTime", "reset_time"))
		if !fraction.Exists() && resetAt == nil {
			return true
		}
		current := grouped[group.key]
		if current == nil {
			current = &groupValue{}
			grouped[group.key] = current
		}
		current.count++
		if fraction.Exists() {
			fractionValue := quotaFraction(fraction)
			if fractionValue == nil {
				return true
			}
			remaining := math.Round(clampPct(*fractionValue * 100))
			if current.percent == nil || remaining < *current.percent {
				current.percent = &remaining
			}
		}
		if resetAt != nil && (current.resetAt == nil || resetAt.Before(*current.resetAt)) {
			current.resetAt = resetAt
		}
		return true
	})

	out := make([]usage.QuotaWindowDTO, 0, len(grouped))
	for _, group := range antigravityQuotaGroups {
		value := grouped[group.key]
		if value == nil || value.count == 0 {
			continue
		}
		out = append(out, usage.QuotaWindowDTO{
			QuotaKey: group.key, QuotaLabel: group.label, Percent: value.percent, ResetAt: value.resetAt,
		})
	}
	return out
}

func stringSet(values ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

func doAuthGET(ctx context.Context, svc *managementapitools.Service, auth *coreauth.Auth, url string, headers map[string]string, mutate func(*http.Request)) ([]byte, error) {
	return doAuthRequest(ctx, svc, auth, http.MethodGet, url, headers, "", mutate)
}

func doAuthPOST(ctx context.Context, svc *managementapitools.Service, auth *coreauth.Auth, url string, headers map[string]string, body string) ([]byte, error) {
	return doAuthRequest(ctx, svc, auth, http.MethodPost, url, headers, body, nil)
}

func doAuthRequest(ctx context.Context, svc *managementapitools.Service, auth *coreauth.Auth, method, urlStr string, headers map[string]string, body string, mutate func(*http.Request)) ([]byte, error) {
	if svc == nil {
		return nil, fmt.Errorf("api tools unavailable")
	}
	token, err := svc.ResolveTokenForAuth(ctx, auth)
	if err != nil || strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("token unavailable")
	}
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, urlStr, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	for k, v := range headers {
		if strings.EqualFold(k, "Authorization") {
			// Always server-controlled bearer from resolved token.
			continue
		}
		req.Header.Set(k, strings.ReplaceAll(v, "$TOKEN$", token))
	}
	if mutate != nil {
		mutate(req)
	}
	client := util.NewHTTPClient(30 * time.Second)
	if transport := svc.APICallTransport(auth); transport != nil {
		client.Transport = transport
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("upstream http %d", resp.StatusCode)
	}
	return raw, nil
}

func codexAccountID(auth *coreauth.Auth) string {
	if direct := authString(auth, "account_id", "accountId", "chatgpt_account_id", "chatgptAccountId"); direct != "" {
		return direct
	}
	idToken := authString(auth, "id_token", "idToken")
	if idToken == "" {
		return ""
	}
	claims, err := codexauth.ParseJWTToken(idToken)
	if err != nil || claims == nil {
		return ""
	}
	return strings.TrimSpace(claims.CodexAuthInfo.ChatgptAccountID)
}

func authString(auth *coreauth.Auth, keys ...string) string {
	if auth == nil {
		return ""
	}
	if value := metadataString(auth, keys...); value != "" {
		return value
	}
	for _, key := range keys {
		if value := strings.TrimSpace(auth.Attributes[key]); value != "" {
			return value
		}
	}
	return ""
}

func metadataNestedString(auth *coreauth.Auth, parent string, keys ...string) string {
	if auth == nil || auth.Metadata == nil {
		return ""
	}
	raw, ok := auth.Metadata[parent]
	if !ok {
		return ""
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	for _, key := range keys {
		if v, ok := m[key]; ok {
			if s, ok := v.(string); ok {
				if t := strings.TrimSpace(s); t != "" {
					return t
				}
			}
		}
	}
	return ""
}

func firstJSONResult(root gjson.Result, paths ...string) gjson.Result {
	for _, path := range paths {
		if value := root.Get(path); value.Exists() {
			return value
		}
	}
	return gjson.Result{}
}

func quotaFraction(value gjson.Result) *float64 {
	if !value.Exists() {
		return nil
	}
	raw := strings.TrimSpace(value.String())
	fraction := value.Float()
	if strings.HasSuffix(raw, "%") {
		parsed, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(raw, "%")), 64)
		if err != nil {
			return nil
		}
		fraction = parsed / 100
	}
	return &fraction
}

func parseFlexibleTime(value gjson.Result) *time.Time {
	if !value.Exists() {
		return nil
	}
	raw := strings.TrimSpace(value.String())
	if raw != "" {
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
			if parsed, err := time.Parse(layout, raw); err == nil {
				t := parsed.UTC()
				return &t
			}
		}
	}
	seconds := value.Float()
	if value.Type == gjson.String {
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil
		}
		seconds = parsed
	}
	if seconds <= 0 {
		return nil
	}
	if seconds > 1e12 {
		seconds /= 1000
	}
	t := time.Unix(int64(seconds), int64((seconds-float64(int64(seconds)))*float64(time.Second))).UTC()
	return &t
}

func clampPct(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func parseURL(raw string) *url.URL {
	u, _ := url.Parse(raw)
	return u
}
