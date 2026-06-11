package usage

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

func QueryDailyCallsByAuthSubject(matcher AuthSubjectMatcher, days int) ([]DailyCountPoint, error) {
	db := getDB()
	if db == nil {
		return []DailyCountPoint{}, nil
	}
	if days < 1 {
		days = 7
	}

	matchSQL, matchArgs := buildAuthSubjectMatchClause(matcher, "source", "channel_name")
	if matchSQL == "" {
		return []DailyCountPoint{}, nil
	}

	args := make([]interface{}, 0, len(matchArgs)+1)
	args = append(args, CutoffStartUTC(days).Format(time.RFC3339))
	args = append(args, matchArgs...)

	rows, err := db.Query(fmt.Sprintf(`
		SELECT timestamp
		FROM request_logs
		WHERE timestamp >= ? AND (%s)
		ORDER BY timestamp ASC
	`, matchSQL), args...)
	if err != nil {
		return nil, fmt.Errorf("usage: daily calls by auth subject query: %w", err)
	}
	defer rows.Close()

	byDate := make(map[string]int64, days)
	for rows.Next() {
		var ts string
		if err := rows.Scan(&ts); err != nil {
			return nil, fmt.Errorf("usage: daily calls by auth subject scan: %w", err)
		}
		parsed, ok := parseStoredTime(ts)
		if !ok {
			continue
		}
		byDate[localDayKeyAt(parsed)]++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	result := make([]DailyCountPoint, 0, len(byDate))
	for date, requests := range byDate {
		result = append(result, DailyCountPoint{Date: date, Requests: requests})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Date < result[j].Date
	})
	return result, nil
}

func QueryHourlyCallsByAuthSubject(matcher AuthSubjectMatcher, hours int) ([]HourlyCountPoint, error) {
	db := getDB()
	if db == nil {
		return []HourlyCountPoint{}, nil
	}
	if hours < 1 {
		hours = 5
	}
	if hours > 24 {
		hours = 24
	}

	matchSQL, matchArgs := buildAuthSubjectMatchClause(matcher, "source", "channel_name")
	if matchSQL == "" {
		return []HourlyCountPoint{}, nil
	}

	loc := getUsageLocation()
	now := time.Now().In(loc).Truncate(time.Hour)
	start := now.Add(-time.Duration(hours-1) * time.Hour)
	buckets := make([]HourlyCountPoint, 0, hours)
	byKey := make(map[string]*HourlyCountPoint, hours)
	for i := 0; i < hours; i++ {
		key := start.Add(time.Duration(i) * time.Hour).Format("2006-01-02 15:00")
		buckets = append(buckets, HourlyCountPoint{Hour: key})
		byKey[key] = &buckets[len(buckets)-1]
	}

	args := make([]interface{}, 0, len(matchArgs)+1)
	args = append(args, start.UTC().Format(time.RFC3339))
	args = append(args, matchArgs...)

	rows, err := db.Query(fmt.Sprintf(`
		SELECT timestamp
		FROM request_logs
		WHERE timestamp >= ? AND (%s)
	`, matchSQL), args...)
	if err != nil {
		return nil, fmt.Errorf("usage: hourly calls by auth subject query: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var ts string
		if err := rows.Scan(&ts); err != nil {
			return nil, fmt.Errorf("usage: hourly calls by auth subject scan: %w", err)
		}
		parsed, ok := parseStoredTime(ts)
		if !ok {
			continue
		}
		key := parsed.In(loc).Truncate(time.Hour).Format("2006-01-02 15:00")
		if bucket := byKey[key]; bucket != nil {
			bucket.Requests++
		}
	}
	return buckets, rows.Err()
}

func QueryRequestCountByAuthSubjectSince(matcher AuthSubjectMatcher, since time.Time) (int64, error) {
	return queryCountByAuthSubjectSince(matcher, since, "COUNT(*)")
}

func QueryCostByAuthSubjectSince(matcher AuthSubjectMatcher, since time.Time) (float64, error) {
	db := getDB()
	if db == nil {
		return 0, nil
	}

	matchSQL, matchArgs := buildAuthSubjectMatchClause(matcher, "source", "channel_name")
	if matchSQL == "" {
		return 0, nil
	}

	args := make([]interface{}, 0, len(matchArgs)+1)
	args = append(args, since.UTC().Format(time.RFC3339))
	args = append(args, matchArgs...)

	var total float64
	err := db.QueryRow(fmt.Sprintf(`
		SELECT COALESCE(SUM(cost), 0)
		FROM request_logs
		WHERE timestamp >= ? AND (%s)
	`, matchSQL), args...).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("usage: request cost by auth subject query: %w", err)
	}
	return total, nil
}

func queryCountByAuthSubjectSince(matcher AuthSubjectMatcher, since time.Time, aggregate string) (int64, error) {
	db := getDB()
	if db == nil {
		return 0, nil
	}

	matchSQL, matchArgs := buildAuthSubjectMatchClause(matcher, "source", "channel_name")
	if matchSQL == "" {
		return 0, nil
	}

	args := make([]interface{}, 0, len(matchArgs)+1)
	args = append(args, since.UTC().Format(time.RFC3339))
	args = append(args, matchArgs...)

	var total int64
	err := db.QueryRow(fmt.Sprintf(`
		SELECT %s
		FROM request_logs
		WHERE timestamp >= ? AND (%s)
	`, aggregate, matchSQL), args...).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("usage: request count by auth subject query: %w", err)
	}
	return total, nil
}

func buildAuthSubjectMatchClause(matcher AuthSubjectMatcher, sourceColumn string, channelColumn string) (string, []interface{}) {
	subjectID := strings.TrimSpace(matcher.SubjectID)
	authIndexes := dedupeExactStrings(matcher.AuthIndexes)
	sourceAliases := dedupeLowerTrimmedStrings(matcher.SourceAliases)
	channelAliases := dedupeLowerTrimmedStrings(matcher.ChannelAliases)

	clauses := make([]string, 0, 4)
	args := make([]interface{}, 0, 1+len(authIndexes)+len(sourceAliases)+len(channelAliases))

	if subjectID != "" {
		clauses = append(clauses, "trim(coalesce(auth_subject_id, '')) = ?")
		args = append(args, subjectID)
	}

	legacyClauses := make([]string, 0, 3)
	legacyArgs := make([]interface{}, 0, len(authIndexes)+len(sourceAliases)+len(channelAliases))
	if len(authIndexes) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(authIndexes)), ",")
		legacyClauses = append(legacyClauses, "auth_index IN ("+placeholders+")")
		for _, value := range authIndexes {
			legacyArgs = append(legacyArgs, value)
		}
	}
	if len(sourceAliases) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(sourceAliases)), ",")
		legacyClauses = append(legacyClauses, "lower(trim(coalesce("+sourceColumn+", ''))) IN ("+placeholders+")")
		for _, value := range sourceAliases {
			legacyArgs = append(legacyArgs, value)
		}
	}
	if len(channelAliases) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(channelAliases)), ",")
		legacyClauses = append(legacyClauses, "lower(trim(coalesce("+channelColumn+", ''))) IN ("+placeholders+")")
		for _, value := range channelAliases {
			legacyArgs = append(legacyArgs, value)
		}
	}
	if len(legacyClauses) > 0 {
		clauses = append(clauses, "(trim(coalesce(auth_subject_id, '')) = '' AND ("+strings.Join(legacyClauses, " OR ")+"))")
		args = append(args, legacyArgs...)
	}

	return strings.Join(clauses, " OR "), args
}

func dedupeExactStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func dedupeLowerTrimmedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.ToLower(strings.TrimSpace(value))
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}
