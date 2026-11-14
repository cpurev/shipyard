package vercel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/cpurev/shipyard/internal/providers"
)

const authorizeURL = "https://vercel.com/oauth/authorize"

// AuthorizeURL builds the Vercel OAuth authorization URL for an account the
// operator already owns. It does not contact Vercel.
func AuthorizeURL(clientID, redirectURI, state string) (string, error) {
	if clientID == "" {
		return "", fmt.Errorf("vercel: oauth client id is required")
	}
	if redirectURI == "" {
		return "", fmt.Errorf("vercel: oauth redirect uri is required")
	}
	if state == "" {
		return "", fmt.Errorf("vercel: oauth state is required")
	}
	u, err := url.Parse(authorizeURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("response_type", "code")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Exchange trades an authorization code for an access token.
// tokenURL is https://api.vercel.com/login/oauth/token in production and an
// httptest server in tests.
func Exchange(ctx context.Context, httpClient *http.Client, tokenURL, clientID, clientSecret, code, redirectURI string) (providers.Credentials, error) {
	if clientID == "" || clientSecret == "" || code == "" || redirectURI == "" {
		return providers.Credentials{}, fmt.Errorf("vercel: oauth exchange requires client id, client secret, code, and redirect uri")
	}
	if tokenURL == "" {
		tokenURL = defaultBaseURL + "/login/oauth/token"
	}
	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return providers.Credentials{}, fmt.Errorf("vercel: oauth request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return providers.Credentials{}, fmt.Errorf("vercel: oauth exchange: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return providers.Credentials{}, fmt.Errorf("vercel: oauth response: %w", err)
	}
	if resp.StatusCode >= 300 {
		return providers.Credentials{}, fmt.Errorf("vercel: oauth exchange: %s", truncate(payload))
	}
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TeamID       string `json:"team_id"`
	}
	if err := json.Unmarshal(payload, &out); err != nil {
		return providers.Credentials{}, fmt.Errorf("vercel: oauth decode: %w", err)
	}
	if out.AccessToken == "" {
		return providers.Credentials{}, fmt.Errorf("vercel: oauth response did not include an access token")
	}
	creds := providers.Credentials{Token: out.AccessToken, RefreshToken: out.RefreshToken}
	if out.TeamID != "" {
		creds.Extra = map[string]string{"teamId": out.TeamID}
	}
	return creds, nil
}
