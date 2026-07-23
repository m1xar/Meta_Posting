package meta

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

type PublishedObjectKind string

const (
	PublishedObjectCampaign PublishedObjectKind = "campaign"
	PublishedObjectAdSet    PublishedObjectKind = "ad_set"
	PublishedObjectCreative PublishedObjectKind = "creative"
	PublishedObjectAd       PublishedObjectKind = "ad"
)

type RemotePublishedObject struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	CampaignID      string `json:"campaign_id,omitempty"`
	AdSetID         string `json:"adset_id,omitempty"`
	Status          string `json:"status,omitempty"`
	EffectiveStatus string `json:"effective_status,omitempty"`
	CreatedTime     string `json:"created_time,omitempty"`
}

func (o RemotePublishedObject) ParentID(kind PublishedObjectKind) string {
	switch kind {
	case PublishedObjectAdSet:
		return o.CampaignID
	case PublishedObjectAd:
		return o.AdSetID
	default:
		return ""
	}
}

// FindPublishedObjectByName reconciles the outcome of a previous create whose
// response could not be durably checkpointed. Publishing uses a globally
// unique suffix in each object name, so an exact match is a stable upstream
// idempotency marker. The account edge is scanned only on job retries.
func (c *Client) FindPublishedObjectByName(
	ctx context.Context,
	accessToken string,
	accountID string,
	kind PublishedObjectKind,
	name string,
	parentID string,
) (RemotePublishedObject, bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return RemotePublishedObject{}, false, errors.New("meta: reconciliation name is required")
	}

	edge, fields, err := reconciliationEdge(kind)
	if err != nil {
		return RemotePublishedObject{}, false, err
	}
	query := url.Values{
		"fields": {fields},
		"limit":  {"500"},
	}
	objects, err := CollectPages[RemotePublishedObject](
		ctx,
		c,
		"/"+AdAccountNodeID(accountID)+"/"+edge,
		accessToken,
		query,
	)
	if err != nil {
		return RemotePublishedObject{}, false, fmt.Errorf("meta: reconcile %s: %w", kind, err)
	}

	var matches []RemotePublishedObject
	for _, object := range objects {
		if object.Name != name {
			continue
		}
		if parentID != "" && object.ParentID(kind) != parentID {
			continue
		}
		matches = append(matches, object)
	}
	switch len(matches) {
	case 0:
		return RemotePublishedObject{}, false, nil
	case 1:
		return matches[0], true, nil
	default:
		return RemotePublishedObject{}, false, fmt.Errorf(
			"meta: reconciliation found %d %s objects named %q; refusing an ambiguous retry",
			len(matches),
			kind,
			name,
		)
	}
}

func reconciliationEdge(kind PublishedObjectKind) (edge string, fields string, err error) {
	switch kind {
	case PublishedObjectCampaign:
		return "campaigns", "id,name,status,effective_status,created_time", nil
	case PublishedObjectAdSet:
		return "adsets", "id,name,campaign_id,status,effective_status,created_time", nil
	case PublishedObjectCreative:
		return "adcreatives", "id,name,status,created_time", nil
	case PublishedObjectAd:
		return "ads", "id,name,adset_id,status,effective_status,created_time", nil
	default:
		return "", "", fmt.Errorf("meta: unsupported reconciliation object kind %q", kind)
	}
}
