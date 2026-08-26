package meta

import (
	"context"
	"net/url"
	"strings"
)

// PromotablePage is a Facebook Page whose posts can be used as ads in a
// specific ad account. Meta's /promote_pages edge is the authoritative list:
// a page the user can see is not necessarily one this account may advertise.
type PromotablePage struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// PromotablePages returns the pages an ad account may run ads for. accountID is
// the raw Meta ad account id (with or without the act_ prefix).
func (c *Client) PromotablePages(ctx context.Context, accessToken, accountID string) ([]PromotablePage, error) {
	node := AdAccountNodeID(strings.TrimSpace(accountID))
	pages, err := CollectPages[PromotablePage](
		ctx, c, node+"/promote_pages", accessToken,
		url.Values{"fields": {"id,name"}, "limit": {"200"}},
	)
	if err != nil {
		return nil, err
	}
	return pages, nil
}
