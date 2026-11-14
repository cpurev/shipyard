package gcp

import (
	"context"
	"fmt"
	"net/http"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/cpurev/shipyard/internal/providers"
)

// AuthorizeURL builds a Google OAuth URL for an existing Cloud account.
// It does not contact Google.
func AuthorizeURL(clientID, redirectURI, state string) (string, error) {
	if clientID == "" {
		return "", fmt.Errorf("gcp: oauth client id is required")
	}
	if redirectURI == "" {
		return "", fmt.Errorf("gcp: oauth redirect uri is required")
	}
	if state == "" {
		return "", fmt.Errorf("gcp: oauth state is required")
	}
	cfg := &oauth2.Config{
		ClientID:    clientID,
		RedirectURL: redirectURI,
		Scopes:      []string{"https://www.googleapis.com/auth/cloud-platform"},
		Endpoint:    google.Endpoint,
	}
	return cfg.AuthCodeURL(state, oauth2.AccessTypeOffline), nil
}

// Exchange trades an authorization code for an access token.
// tokenURL is the Google token endpoint in production and an httptest server
// in tests.
func Exchange(ctx context.Context, httpClient *http.Client, tokenURL, clientID, clientSecret, code, redirectURI string) (providers.Credentials, error) {
	if clientID == "" || clientSecret == "" || code == "" || redirectURI == "" {
		return providers.Credentials{}, fmt.Errorf("gcp: oauth exchange requires client id, client secret, code, and redirect uri")
	}
	if tokenURL == "" {
		tokenURL = google.Endpoint.TokenURL
	}
	cfg := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURI,
		Scopes:       []string{"https://www.googleapis.com/auth/cloud-platform"},
		Endpoint:     oauth2.Endpoint{TokenURL: tokenURL},
	}
	if httpClient != nil {
		ctx = context.WithValue(ctx, oauth2.HTTPClient, httpClient)
	}
	tok, err := cfg.Exchange(ctx, code)
	if err != nil {
		return providers.Credentials{}, fmt.Errorf("gcp: oauth exchange: %w", err)
	}
	if tok.AccessToken == "" {
		return providers.Credentials{}, fmt.Errorf("gcp: oauth response did not include an access token")
	}
	return providers.Credentials{Token: tok.AccessToken, RefreshToken: tok.RefreshToken}, nil
}
