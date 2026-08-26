package meta

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type OAuthOptions struct {
	RedirectURI string
	State       string
	Scopes      []string
	ConfigID    string
	AuthType    string
	Display     string
}

// AuthorizationURL creates an OAuth URL. Facebook Login for Business uses a
// saved configuration as the source of truth for permissions, whereas classic
// Facebook Login uses the explicit scope parameter.
func (c *Client) AuthorizationURL(options OAuthOptions) (string, error) {
	if strings.TrimSpace(options.RedirectURI) == "" {
		return "", errors.New("meta: OAuth redirect URI is required")
	}
	if strings.TrimSpace(options.State) == "" {
		return "", errors.New("meta: OAuth state is required")
	}
	target := cloneURL(c.oauthBaseURL)
	target.Path = strings.TrimSuffix(target.Path, "/") + "/" + c.apiVersion + "/dialog/oauth"
	query := target.Query()
	query.Set("client_id", c.appID)
	query.Set("redirect_uri", options.RedirectURI)
	query.Set("state", options.State)
	query.Set("response_type", "code")
	if configID := strings.TrimSpace(options.ConfigID); configID != "" {
		query.Set("config_id", configID)
		// Facebook Login for Business configurations can define their own
		// permissions and default response type. The backend consumes an
		// authorization code, so explicitly make response_type=code take
		// precedence. Do not send scope with config_id: Meta rejects it.
		query.Set("override_default_response_type", "true")
	} else if len(options.Scopes) > 0 {
		query.Set("scope", strings.Join(options.Scopes, ","))
	}
	if options.AuthType != "" {
		query.Set("auth_type", options.AuthType)
	}
	if options.Display != "" {
		query.Set("display", options.Display)
	}
	target.RawQuery = query.Encode()
	return target.String(), nil
}

type TokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type,omitempty"`
	ExpiresIn   int64  `json:"expires_in,omitempty"`
}

func (t TokenResponse) ExpiresAt(now time.Time) time.Time {
	if t.ExpiresIn <= 0 {
		return time.Time{}
	}
	return now.Add(time.Duration(t.ExpiresIn) * time.Second)
}

func (c *Client) ExchangeCode(
	ctx context.Context,
	code string,
	redirectURI string,
) (TokenResponse, error) {
	if code == "" || redirectURI == "" {
		return TokenResponse{}, errors.New("meta: OAuth code and redirect URI are required")
	}
	query := url.Values{
		"client_id":     {c.appID},
		"client_secret": {c.appSecret},
		"redirect_uri":  {redirectURI},
		"code":          {code},
	}
	var token TokenResponse
	if err := c.Get(ctx, "/oauth/access_token", "", query, &token); err != nil {
		return TokenResponse{}, err
	}
	if token.AccessToken == "" {
		return TokenResponse{}, errors.New("meta: code exchange returned an empty access token")
	}
	return token, nil
}

func (c *Client) ExchangeLongLivedToken(ctx context.Context, shortLivedToken string) (TokenResponse, error) {
	if shortLivedToken == "" {
		return TokenResponse{}, errors.New("meta: short-lived token is required")
	}
	query := url.Values{
		"grant_type":        {"fb_exchange_token"},
		"client_id":         {c.appID},
		"client_secret":     {c.appSecret},
		"fb_exchange_token": {shortLivedToken},
	}
	var token TokenResponse
	if err := c.Get(ctx, "/oauth/access_token", "", query, &token); err != nil {
		return TokenResponse{}, err
	}
	if token.AccessToken == "" {
		return TokenResponse{}, errors.New("meta: long-lived exchange returned an empty access token")
	}
	return token, nil
}

type DebugTokenResponse struct {
	Data DebugTokenData `json:"data"`
}

type DebugTokenData struct {
	AppID               string          `json:"app_id"`
	Type                string          `json:"type"`
	Application         string          `json:"application"`
	UserID              string          `json:"user_id"`
	ProfileID           string          `json:"profile_id,omitempty"`
	IsValid             bool            `json:"is_valid"`
	IssuedAt            int64           `json:"issued_at"`
	ExpiresAt           int64           `json:"expires_at"`
	DataAccessExpiresAt int64           `json:"data_access_expires_at"`
	Scopes              []string        `json:"scopes"`
	GranularScopes      []GranularScope `json:"granular_scopes,omitempty"`
	Error               *GraphError     `json:"error,omitempty"`
}

type GranularScope struct {
	Scope     string   `json:"scope"`
	TargetIDs []string `json:"target_ids,omitempty"`
}

func (d DebugTokenData) ExpirationTime() time.Time {
	if d.ExpiresAt <= 0 {
		return time.Time{}
	}
	return time.Unix(d.ExpiresAt, 0)
}

func (d DebugTokenData) DataAccessExpirationTime() time.Time {
	if d.DataAccessExpiresAt <= 0 {
		return time.Time{}
	}
	return time.Unix(d.DataAccessExpiresAt, 0)
}

func (c *Client) DebugToken(ctx context.Context, inputToken string) (DebugTokenData, error) {
	if inputToken == "" {
		return DebugTokenData{}, errors.New("meta: input token is required")
	}
	appToken := c.appID + "|" + c.appSecret
	query := url.Values{"input_token": {inputToken}}
	var response DebugTokenResponse
	if err := c.Get(ctx, "/debug_token", appToken, query, &response); err != nil {
		return DebugTokenData{}, err
	}
	if response.Data.Error != nil {
		return response.Data, response.Data.Error
	}
	if response.Data.AppID != "" && response.Data.AppID != c.appID {
		return response.Data, fmt.Errorf(
			"meta: token belongs to app %s, expected %s",
			strconv.Quote(response.Data.AppID),
			strconv.Quote(c.appID),
		)
	}
	return response.Data, nil
}
