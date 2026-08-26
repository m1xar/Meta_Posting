// Package fieldaudit compares this service's typed Meta specifications
// against what the Graph API itself reports.
//
// The point is to stop guessing. Meta's documentation drifts from the live
// API, but every node answers ?metadata=1 with its own field list, and the
// Insights edge names any field it rejects. Both are authoritative in a way
// the docs are not.
package fieldaudit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"strings"

	"github.com/watchers-factory/raze-ads/internal/meta"
)

// Coverage classifies one field Meta reports.
type Coverage string

const (
	// CoverageTyped means the field has a dedicated struct field, so it is
	// validated and discoverable.
	CoverageTyped Coverage = "typed"
	// CoverageRaw means it is reachable only through the RawFields escape
	// hatch: usable, but unvalidated and invisible to /v1/capabilities.
	CoverageRaw Coverage = "raw"
)

// FieldReport is one node's comparison.
type FieldReport struct {
	Node     string              `json:"node"`
	Total    int                 `json:"total"`
	Typed    []string            `json:"typed"`
	RawOnly  []string            `json:"raw_only"`
	Unknown  []string            `json:"unknown_to_meta"`
	ByField  map[string]Coverage `json:"-"`
	Warnings []string            `json:"warnings,omitempty"`
}

// SpecFieldNames returns the JSON field names a specification struct models.
//
// It walks embedded structs and reads the json tag, which is what actually
// reaches Meta, rather than the Go field name.
func SpecFieldNames(spec any) []string {
	seen := map[string]struct{}{}
	collectSpecFields(reflect.TypeOf(spec), seen)
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func collectSpecFields(specType reflect.Type, seen map[string]struct{}) {
	for specType != nil && specType.Kind() == reflect.Pointer {
		specType = specType.Elem()
	}
	if specType == nil || specType.Kind() != reflect.Struct {
		return
	}
	for index := 0; index < specType.NumField(); index++ {
		field := specType.Field(index)
		if field.Anonymous {
			collectSpecFields(field.Type, seen)
			continue
		}
		tag := field.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if name == "" {
			continue
		}
		// The escape hatch is not itself a Meta field.
		if name == "raw" && field.Type == reflect.TypeOf(meta.RawFields(nil)) {
			continue
		}
		seen[name] = struct{}{}
	}
}

// metadataResponse is the shape of a ?metadata=1 answer.
type metadataResponse struct {
	Metadata struct {
		Fields []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Type        string `json:"type"`
		} `json:"fields"`
	} `json:"metadata"`
}

// NodeFields asks a live object what fields it has.
//
// This is the API's own answer, which is why it beats reading the docs: a
// field added or removed in v25.0 shows up here immediately.
func NodeFields(
	ctx context.Context,
	client *meta.Client,
	accessToken string,
	objectID string,
) ([]string, error) {
	var response metadataResponse
	query := url.Values{"metadata": {"1"}, "fields": {"id"}}
	if err := client.Get(ctx, "/"+strings.TrimPrefix(objectID, "/"), accessToken, query, &response); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(response.Metadata.Fields))
	for _, field := range response.Metadata.Fields {
		names = append(names, field.Name)
	}
	sort.Strings(names)
	return names, nil
}

// Compare diffs the fields Meta reports against those the spec models.
func Compare(node string, metaFields, specFields []string) FieldReport {
	typed := map[string]struct{}{}
	for _, name := range specFields {
		typed[name] = struct{}{}
	}
	report := FieldReport{
		Node:    node,
		Total:   len(metaFields),
		ByField: map[string]Coverage{},
	}
	reported := map[string]struct{}{}
	for _, name := range metaFields {
		reported[name] = struct{}{}
		if _, ok := typed[name]; ok {
			report.Typed = append(report.Typed, name)
			report.ByField[name] = CoverageTyped
			continue
		}
		report.RawOnly = append(report.RawOnly, name)
		report.ByField[name] = CoverageRaw
	}
	// A field this service models that Meta no longer reports is the more
	// urgent direction: it may have been removed or renamed.
	for _, name := range specFields {
		if _, ok := reported[name]; !ok {
			report.Unknown = append(report.Unknown, name)
		}
	}
	sort.Strings(report.Typed)
	sort.Strings(report.RawOnly)
	sort.Strings(report.Unknown)
	if len(report.Unknown) > 0 {
		report.Warnings = append(report.Warnings, fmt.Sprintf(
			"%d field(s) are modelled here but not reported by Meta; verify they still exist",
			len(report.Unknown),
		))
	}
	return report
}

// Markdown renders a report for docs/.
func (r FieldReport) Markdown() string {
	var out strings.Builder
	fmt.Fprintf(&out, "### %s\n\n", r.Node)
	fmt.Fprintf(&out, "Meta reports %d fields: %d typed, %d raw-only.\n\n",
		r.Total, len(r.Typed), len(r.RawOnly))
	for _, warning := range r.Warnings {
		fmt.Fprintf(&out, "> %s\n>\n> %s\n\n", warning, strings.Join(r.Unknown, ", "))
	}
	if len(r.RawOnly) > 0 {
		out.WriteString("Reachable only through `raw`:\n\n")
		for _, name := range r.RawOnly {
			fmt.Fprintf(&out, "- `%s`\n", name)
		}
		out.WriteString("\n")
	}
	return out.String()
}

// --- insights fields ---

// invalidFieldPattern matches the field name Meta puts in a (#100) rejection,
// e.g. `(#100) foo is not valid for fields param`.
func invalidFieldName(err error) string {
	var graphErr *meta.GraphError
	if !asGraphError(err, &graphErr) {
		return ""
	}
	message := graphErr.Message
	for _, marker := range []string{
		" is not valid for fields param",
		" is not a valid field",
	} {
		if index := strings.Index(message, marker); index > 0 {
			prefix := strings.TrimSpace(message[:index])
			parts := strings.Fields(prefix)
			if len(parts) == 0 {
				continue
			}
			return strings.Trim(parts[len(parts)-1], "\"'`,()")
		}
	}
	return ""
}

func asGraphError(err error, target **meta.GraphError) bool {
	for err != nil {
		if graphErr, ok := err.(*meta.GraphError); ok {
			*target = graphErr
			return true
		}
		unwrapped, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapped.Unwrap()
	}
	return false
}

// InsightFieldResult is the outcome of probing one level.
type InsightFieldResult struct {
	Level    meta.InsightLevel `json:"level"`
	Accepted []string          `json:"accepted"`
	Rejected []string          `json:"rejected"`
}

// ProbeInsightFields determines which of candidates the Insights edge accepts
// at a level.
//
// Insights has no ?metadata=1, but it names the offending field when it
// rejects one, so removing that field and retrying converges on the accepted
// set. Each rejection costs one request, which is why the caller supplies a
// curated candidate list rather than every string in the docs.
func ProbeInsightFields(
	ctx context.Context,
	client *meta.Client,
	accessToken string,
	accountID string,
	level meta.InsightLevel,
	candidates []string,
	maxAttempts int,
) (InsightFieldResult, error) {
	if maxAttempts <= 0 {
		maxAttempts = 60
	}
	result := InsightFieldResult{Level: level}
	remaining := append([]string(nil), candidates...)

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if len(remaining) == 0 {
			break
		}
		_, _, err := client.FetchDailyInsights(ctx, accessToken, meta.DailyInsightRequest{
			AccountID: accountID,
			Level:     level,
			// A one-day window keeps the probe cheap; validity of a field
			// does not depend on the range.
			Since:       "2026-01-01",
			Until:       "2026-01-01",
			Fields:      remaining,
			Attribution: meta.AttributionMode{Unified: true},
			Limit:       1,
		})
		if err == nil {
			result.Accepted = remaining
			sort.Strings(result.Accepted)
			sort.Strings(result.Rejected)
			return result, nil
		}
		bad := invalidFieldName(err)
		if bad == "" {
			return result, fmt.Errorf("probe %s: %w", level, err)
		}
		filtered := remaining[:0]
		found := false
		for _, name := range remaining {
			if name == bad {
				found = true
				continue
			}
			filtered = append(filtered, name)
		}
		if !found {
			return result, fmt.Errorf(
				"probe %s: Meta rejected %q which was not in the candidate set: %w",
				level, bad, err,
			)
		}
		remaining = filtered
		result.Rejected = append(result.Rejected, bad)
	}
	sort.Strings(result.Accepted)
	sort.Strings(result.Rejected)
	return result, fmt.Errorf("probe %s: did not converge within %d attempts", level, maxAttempts)
}

// EncodeJSON renders any report for machine consumption.
func EncodeJSON(value any) (string, error) {
	encoded, err := json.MarshalIndent(value, "", "  ")
	return string(encoded), err
}
