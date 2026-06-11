package usagelogs

import (
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func (s *Service) AuthExists(authIndex string) bool {
	return s.authByIndex(authIndex) != nil
}

func (s *Service) AuthFileGroupTrend(group string, days int) (AuthFileGroupTrendResponse, error) {
	authIndexes := s.authIndexesForProviderGroup(group)
	points, err := usage.QueryDailyCallsByAuthIndexes(authIndexes, days)
	if err != nil {
		return AuthFileGroupTrendResponse{}, err
	}
	if points == nil {
		points = []usage.DailyCountPoint{}
	}
	quotaPoints, err := usage.QueryDailyQuotaByAuthIndexes(authIndexes, "code_week", days)
	if err != nil {
		return AuthFileGroupTrendResponse{}, err
	}
	if quotaPoints == nil {
		quotaPoints = []usage.DailyQuotaPoint{}
	}
	return AuthFileGroupTrendResponse{Days: days, Group: group, Points: points, QuotaPoints: quotaPoints}, nil
}

func (s *Service) AuthFileTrend(authIndex string, days int, hours int) (int, any) {
	if strings.TrimSpace(authIndex) == "" {
		return http.StatusBadRequest, map[string]any{"error": "auth_index is required"}
	}
	auth := s.authByIndex(authIndex)
	if auth == nil {
		return http.StatusNotFound, map[string]any{"error": "auth not found"}
	}
	matcher := s.authSubjectMatcher(auth)

	dailyRaw, err := usage.QueryDailyCallsByAuthSubject(matcher, days)
	if err != nil {
		return http.StatusInternalServerError, map[string]any{"error": err.Error()}
	}
	daily := fillDailyCountPoints(dailyRaw, days)

	hourly, err := usage.QueryHourlyCallsByAuthSubject(matcher, hours)
	if err != nil {
		return http.StatusInternalServerError, map[string]any{"error": err.Error()}
	}
	if hourly == nil {
		hourly = []usage.HourlyCountPoint{}
	}

	cutoff := usage.CutoffStartUTC(days)
	requestTotal, err := usage.QueryRequestCountByAuthSubjectSince(matcher, cutoff)
	if err != nil {
		return http.StatusInternalServerError, map[string]any{"error": err.Error()}
	}

	trendStart := time.Now().AddDate(0, 0, -7)
	trendEnd := time.Now().Add(time.Minute)
	series, err := usage.QueryQuotaSnapshotSeriesByAuthSubject(matcher, trendStart, trendEnd)
	if err != nil {
		return http.StatusInternalServerError, map[string]any{"error": err.Error()}
	}
	if series == nil {
		series = []usage.QuotaSnapshotSeries{}
	}

	var cycleStart time.Time
	if identity := usage.ResolveAuthSubjectIdentity(auth); identity != nil && identity.ID != "" {
		cycle, err := usage.QueryLatestWeeklyQuotaCycleByAuthSubject(identity.ID)
		if err != nil {
			return http.StatusInternalServerError, map[string]any{"error": err.Error()}
		}
		if cycle != nil {
			cycleStart = cycle.CycleStartAt.UTC()
		}
	}
	if cycleStart.IsZero() {
		if weeklyCycleStart, ok := latestWeeklyQuotaCycleStart(series); ok {
			cycleStart = weeklyCycleStart
		}
	}

	var cycleRequestTotal int64
	var cycleCostTotal float64
	cycleKnown := !cycleStart.IsZero()
	if cycleKnown {
		cycleRequestTotal, err = usage.QueryRequestCountByAuthSubjectSince(matcher, cycleStart)
		if err != nil {
			return http.StatusInternalServerError, map[string]any{"error": err.Error()}
		}
		cycleCostTotal, err = usage.QueryCostByAuthSubjectSince(matcher, cycleStart)
		if err != nil {
			return http.StatusInternalServerError, map[string]any{"error": err.Error()}
		}
	}

	cycleStartStr := ""
	if cycleKnown {
		cycleStartStr = cycleStart.UTC().Format(time.RFC3339)
	}
	return http.StatusOK, AuthFileTrendResponse{
		AuthIndex:         authIndex,
		Days:              days,
		Hours:             hours,
		RequestTotal:      requestTotal,
		CycleRequestTotal: cycleRequestTotal,
		CycleCostTotal:    cycleCostTotal,
		CycleKnown:        cycleKnown,
		CycleStart:        cycleStartStr,
		DailyUsage:        daily,
		HourlyUsage:       hourly,
		QuotaSeries:       series,
	}
}

func fillDailyCountPoints(points []usage.DailyCountPoint, days int) []usage.DailyCountPoint {
	if days < 1 {
		days = 7
	}
	byDate := make(map[string]int64, len(points))
	for _, point := range points {
		byDate[point.Date] += point.Requests
	}
	start := usage.CutoffStartUTC(days)
	result := make([]usage.DailyCountPoint, 0, days)
	for i := 0; i < days; i++ {
		date := usage.LocalDayKeyAt(start.AddDate(0, 0, i))
		result = append(result, usage.DailyCountPoint{Date: date, Requests: byDate[date]})
	}
	return result
}

func latestWeeklyQuotaCycleStart(series []usage.QuotaSnapshotSeries) (time.Time, bool) {
	var latestPoint *usage.QuotaSnapshotSeriesPoint
	var latestWindow int64
	for i := range series {
		if series[i].WindowSeconds < 604800 {
			continue
		}
		windowSeconds := series[i].WindowSeconds
		for j := range series[i].Points {
			point := &series[i].Points[j]
			if point.ResetAt == nil || point.ResetAt.IsZero() {
				continue
			}
			if latestPoint == nil || point.Timestamp.After(latestPoint.Timestamp) {
				latestPoint = point
				latestWindow = windowSeconds
			}
		}
	}
	if latestPoint == nil || latestWindow <= 0 {
		return time.Time{}, false
	}
	return latestPoint.ResetAt.Add(-time.Duration(latestWindow) * time.Second).UTC(), true
}

func (s *Service) authIndexesForProviderGroup(group string) []string {
	if s == nil || s.authManager == nil {
		return []string{}
	}
	auths := s.authManager.List()
	indexes := make([]string, 0, len(auths))
	for _, auth := range auths {
		if auth == nil {
			continue
		}
		provider := strings.ToLower(strings.TrimSpace(auth.Provider))
		if group != "all" && provider != group {
			continue
		}
		auth.EnsureIndex()
		if idx := strings.TrimSpace(auth.Index); idx != "" {
			indexes = append(indexes, idx)
		}
	}
	return indexes
}

func (s *Service) authByIndex(authIndex string) *coreauth.Auth {
	if s == nil || s.authManager == nil {
		return nil
	}
	authIndex = strings.TrimSpace(authIndex)
	if authIndex == "" {
		return nil
	}
	for _, auth := range s.authManager.List() {
		if auth == nil {
			continue
		}
		auth.EnsureIndex()
		if strings.TrimSpace(auth.Index) == authIndex {
			return auth
		}
	}
	return nil
}

func (s *Service) authSubjectMatcher(auth *coreauth.Auth) usage.AuthSubjectMatcher {
	if auth == nil {
		return usage.AuthSubjectMatcher{}
	}
	auths := []*coreauth.Auth{}
	if s != nil && s.authManager != nil {
		auths = s.authManager.List()
	}
	return usage.BuildAuthSubjectMatcher(auth, auths)
}
