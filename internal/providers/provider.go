// Package providers is the seam between Shipyard and a hosting account the
// operator already owns. Shipyard does not run hosting of its own.
package providers

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"
)

var (
	// ErrNotSupported means this build refuses the operation.
	ErrNotSupported = errors.New("not supported")

	// ErrNotConnected means the operator has not connected this provider.
	ErrNotConnected = errors.New("provider is not connected")

	// ErrNotFound means the named app is not on the provider.
	ErrNotFound = errors.New("app not found")
)

// appName matches Cloud Run service ids: start with a letter, end with a
// letter or digit, at most 49 characters.
var appName = regexp.MustCompile(`^[a-z](?:[a-z0-9-]{0,47}[a-z0-9])?$`)

// ValidateAppName reports whether name can be used as an app id.
func ValidateAppName(name string) error {
	if !appName.MatchString(name) {
		return fmt.Errorf("app name %q must be 1-49 characters, lowercase letters, digits, and hyphens, and must start with a letter", name)
	}
	return nil
}

// Credentials are the operator's own provider credentials.
// They are written only to the local credential store.
type Credentials struct {
	Token        string            `json:"token,omitempty"`
	RefreshToken string            `json:"refreshToken,omitempty"`
	Project      string            `json:"project,omitempty"`
	Extra        map[string]string `json:"extra,omitempty"`
	// ServiceAccountJSON is a GCP service-account key the operator supplied.
	ServiceAccountJSON []byte `json:"serviceAccountJSON,omitempty"`
}

// CredentialsReady reports whether stored credentials are enough to try a
// provider. It does not call the provider.
func CredentialsReady(provider string, c Credentials) bool {
	switch provider {
	case "fake":
		return c.Token != ""
	case "vercel":
		return c.Token != ""
	case "gcp":
		return c.Project != "" && (c.Token != "" || len(c.ServiceAccountJSON) > 0)
	default:
		return false
	}
}

// DeployRequest is one deploy of a local directory.
type DeployRequest struct {
	Name      string
	Dir       string
	Region    string
	Framework string
	Image     string
}

// Deployment is the provider's record of a deploy.
type Deployment struct {
	ID        string
	AppName   string
	URL       string
	Status    string
	Provider  string
	Region    string
	Framework string
	Message   string
}

// App is one hosted app.
type App struct {
	Name     string
	ID       string
	URL      string
	Provider string
	Region   string
	Status   string
}

// AppStatus is a point-in-time view of one app.
type AppStatus struct {
	Name      string
	ID        string
	URL       string
	Status    string
	Region    string
	Provider  string
	UpdatedAt time.Time
	Message   string
}

// LogLine is one provider log line.
type LogLine struct {
	Time    time.Time
	Level   string
	Message string
}

// LogOptions narrows a log read.
type LogOptions struct {
	Limit int
	Since time.Time
}

// Account would be a hosting account Shipyard had created.
// No implementation creates one.
type Account struct {
	ID    string
	Email string
}

// ProvisionRequest would ask a provider to open a new account.
type ProvisionRequest struct {
	Email  string
	Region string
}

// Provider deploys to one kind of hosting account.
//
// Connect accepts credentials for an account the operator already owns
// (an OAuth grant or an API token). Implementations must not create accounts.
type Provider interface {
	Name() string
	Connect(ctx context.Context, creds Credentials) error
	Deploy(ctx context.Context, req DeployRequest) (*Deployment, error)
	Status(ctx context.Context, app string) (*AppStatus, error)
	Logs(ctx context.Context, app string, opts LogOptions) ([]LogLine, error)
	SetSecret(ctx context.Context, app, key, value string) error
	ListApps(ctx context.Context) ([]App, error)
	// ProvisionAccount is a stub. See RejectProvisionAccount.
	ProvisionAccount(ctx context.Context, req ProvisionRequest) (*Account, error)
}

// RejectProvisionAccount is the only account-creation behavior in this tree.
//
// TODO: do not implement automatic account creation. It would break the
// providers' terms and be blocked by captchas and KYC checks. Operators
// connect an account they already own with `shipyard connect`.
func RejectProvisionAccount() (*Account, error) {
	return nil, ErrNotSupported
}

// LimitLines keeps the newest lines, in chronological order.
// lines must already be chronological. A non-positive limit means 100.
func LimitLines(lines []LogLine, opts LogOptions) []LogLine {
	if !opts.Since.IsZero() {
		kept := make([]LogLine, 0, len(lines))
		for _, line := range lines {
			if !line.Time.Before(opts.Since) {
				kept = append(kept, line)
			}
		}
		lines = kept
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}
	if len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	if lines == nil {
		return []LogLine{}
	}
	return lines
}
