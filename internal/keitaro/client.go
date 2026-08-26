// Package keitaro reads per-campaign conversion statistics from a Keitaro
// tracker through its Admin API. The service only builds reports; campaigns,
// offers, and streams stay managed inside Keitaro itself.
package keitaro

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ReportMetrics is the fixed metric set one report row carries. Leads are
// tracker registrations and sales are deposits.
var reportMetrics = []string{
	"clicks",
	"campaign_unique_clicks",
	"leads",
	"sales",
	"revenue",
}

type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func NewClient(baseURL, apiKey string, timeout time.Duration) (*Client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	apiKey = strings.TrimSpace(apiKey)
	if baseURL == "" {
		return nil, errors.New("keitaro: base URL is required")
	}
	if apiKey == "" {
		return nil, errors.New("keitaro: API key is required")
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		baseURL:    baseURL,
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: timeout},
	}, nil
}

// ReportRow is one grouped report row. SubID7 carries the Facebook campaign
// ID and SubID3 the campaign name, following the tracker's link template
// sub_id_3={{campaign.name}}&sub_id_7={{campaign.id}}.
type ReportRow struct {
	SubID7       string  `json:"sub_id_7"`
	SubID3       string  `json:"sub_id_3"`
	Clicks       int64   `json:"clicks"`
	UniqueClicks int64   `json:"campaign_unique_clicks"`
	Leads        float64 `json:"leads"`
	Sales        float64 `json:"sales"`
	Revenue      float64 `json:"revenue"`
}

type reportFilter struct {
	Name       string   `json:"name"`
	Operator   string   `json:"operator"`
	Expression []string `json:"expression"`
}

type reportRequest struct {
	Range    map[string]any `json:"range"`
	Grouping []string       `json:"grouping"`
	Filters  []reportFilter `json:"filters,omitempty"`
	Metrics  []string       `json:"metrics"`
	Limit    int            `json:"limit"`
}

type reportResponse struct {
	Rows  []ReportRow `json:"rows"`
	Total int         `json:"total"`
}

// CampaignReport returns all-time stats grouped by (sub_id_7, sub_id_3),
// restricted to the given campaign IDs and campaign names. Either list may be
// empty; with both empty the report request is skipped entirely.
func (c *Client) CampaignReport(ctx context.Context, campaignIDs, campaignNames []string) ([]ReportRow, error) {
	rows := make([]ReportRow, 0, len(campaignIDs)+len(campaignNames))
	if len(campaignIDs) > 0 {
		byID, err := c.buildReport(ctx, reportFilter{Name: "sub_id_7", Operator: "IN_LIST", Expression: campaignIDs})
		if err != nil {
			return nil, err
		}
		rows = append(rows, byID...)
	}
	if len(campaignNames) > 0 {
		byName, err := c.buildReport(ctx, reportFilter{Name: "sub_id_3", Operator: "IN_LIST", Expression: campaignNames})
		if err != nil {
			return nil, err
		}
		rows = append(rows, byName...)
	}
	return rows, nil
}

func (c *Client) buildReport(ctx context.Context, filter reportFilter) ([]ReportRow, error) {
	const pageLimit = 5000
	payload := reportRequest{
		Range:    map[string]any{"interval": "all_time"},
		Grouping: []string{"sub_id_7", "sub_id_3"},
		Filters:  []reportFilter{filter},
		Metrics:  reportMetrics,
		Limit:    pageLimit,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/admin_api/v1/report/build", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Api-Key", c.apiKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("keitaro: report request: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("keitaro: read report response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("keitaro: report returned HTTP %d: %s", response.StatusCode, truncate(raw, 300))
	}
	var decoded reportResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("keitaro: decode report response: %w", err)
	}
	return decoded.Rows, nil
}

func truncate(raw []byte, limit int) string {
	value := strings.TrimSpace(string(raw))
	if len(value) > limit {
		return value[:limit] + "…"
	}
	return value
}
