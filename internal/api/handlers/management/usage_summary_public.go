package management

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/api/bodyutil"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
)

type usageSummaryResponse struct {
	Found bool           `json:"found"`
	Range string         `json:"range"`
	Stats usageStatsBody `json:"stats"`
}

type usageStatsBody struct {
	TotalCalls int64   `json:"total_calls"`
	QuotaCost  float64 `json:"quota_cost"`
}

// GetPublicUsageSummary returns today's call count and quota cost for an API key.
// This is a lightweight endpoint designed for CC Switch Provider card polling.
func (h *Handler) GetPublicUsageSummary(c *gin.Context) {
	apiKey := ""
	var req publicLookupRequest

	if c.Request.Method == http.MethodPost {
		body, err := bodyutil.ReadRequestBody(c, publicLookupBodyLimit)
		if err != nil {
			if bodyutil.IsTooLarge(err) {
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "request body too large"})
				return
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
			return
		}
		if trimmed := strings.TrimSpace(string(body)); trimmed != "" {
			if err := json.Unmarshal(body, &req); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json body"})
				return
			}
		}
		apiKey = strings.TrimSpace(req.APIKey)
	}

	if apiKey == "" {
		apiKey = strings.TrimSpace(c.Query("api_key"))
	}
	if apiKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "api_key parameter is required"})
		return
	}

	stats, err := usage.QueryStats(usage.LogQueryParams{APIKey: apiKey, Days: 1})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query usage summary"})
		return
	}

	resp := usageSummaryResponse{
		Found: stats.Total > 0,
		Range: "today",
		Stats: usageStatsBody{
			TotalCalls: stats.Total,
			QuotaCost:  stats.TotalCost,
		},
	}
	c.JSON(http.StatusOK, resp)
}
