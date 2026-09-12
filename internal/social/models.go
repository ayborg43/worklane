package social

import "time"

// Platforms is the fixed, supported set — matches the CHECK constraints on
// social_app_credentials/social_accounts. Only "x" has a working Provider
// today (see x.go); the rest exist as schema/UI slots so adding one later
// is just a new provider file, not a new migration.
var Platforms = []string{"x", "linkedin", "facebook", "instagram", "tiktok"}

func PlatformLabel(platform string) string {
	switch platform {
	case "x":
		return "X (Twitter)"
	case "linkedin":
		return "LinkedIn"
	case "facebook":
		return "Facebook"
	case "instagram":
		return "Instagram"
	case "tiktok":
		return "TikTok"
	default:
		return platform
	}
}

// AppCredentials is the admin-entered OAuth app (client id/secret) for one
// platform — same "singleton row, empty string means unconfigured" shape
// as settings.MailSettings.
type AppCredentials struct {
	Platform      string
	ClientID      string
	ClientSecret  string
	UpdatedAt     time.Time
	UpdatedByName string
}

func (c AppCredentials) Configured() bool {
	return c.ClientID != "" && c.ClientSecret != ""
}

func (c AppCredentials) Label() string {
	return PlatformLabel(c.Platform)
}

// Implemented reports whether this platform has an actual Provider wired
// up yet (only "x" today) — the connections page uses this to show
// "coming soon" instead of a non-functional connect button.
func (c AppCredentials) Implemented() bool {
	return c.Platform == "x"
}

// Account is a connected company account for a platform, able to publish
// posts once created via that platform's OAuth flow.
type Account struct {
	ID                int64
	Platform          string
	Label             string
	ExternalAccountID string
	AccessToken       string
	RefreshToken      string
	TokenExpiresAt    *time.Time
	ConnectedByName   string
	CreatedAt         time.Time
}

// PlatformName disambiguates from the Label field, which is the connected
// account's own handle (e.g. "@worklane"), not the platform's display name.
func (a Account) PlatformName() string {
	return PlatformLabel(a.Platform)
}

type Post struct {
	ID            int64
	Body          string
	CreatedByName string
	ScheduledAt   *time.Time
	CreatedAt     time.Time
	Targets       []PostTarget
}

// IsScheduled is false for a post that was (or will be) sent immediately.
func (p Post) IsScheduled() bool {
	return p.ScheduledAt != nil
}

type PostTarget struct {
	ID           int64
	PostID       int64
	AccountID    int64
	AccountLabel string
	Platform     string
	Status       string // pending, posted, failed
	RemotePostID string
	ErrorMessage string
	PostedAt     *time.Time
}
