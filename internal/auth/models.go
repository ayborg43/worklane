package auth

import "time"

// AllModules is every module an admin can independently allow or block per
// user via Settings → User Access (see Repo.SetModuleAccess). Deliberately
// excludes Settings itself and Social's connections/OAuth management —
// both stay purely admin-or-not (RequireAdmin), never reachable by a
// non-admin regardless of this table.
var AllModules = []string{"projects", "crm", "helpdesk", "timesheets", "timeoff", "calendar", "social", "chat"}

var ModuleLabels = map[string]string{
	"projects":   "Projects",
	"crm":        "CRM",
	"helpdesk":   "Helpdesk",
	"timesheets": "Timesheets",
	"timeoff":    "Time Off",
	"calendar":   "Calendar",
	"social":     "Social Posts",
	"chat":       "Discuss",
}

// IsModule reports whether module is one of AllModules — used to validate
// path values before they ever reach a SQL query.
func IsModule(module string) bool {
	for _, m := range AllModules {
		if m == module {
			return true
		}
	}
	return false
}

type User struct {
	ID           int64
	Email        string
	Name         string
	PasswordHash string
	IsAdmin      bool
	CreatedAt    time.Time
	UpdatedAt    time.Time

	// RestrictedModules is only populated for the currently-authenticated
	// user (see Middleware.LoadUser / Repo.GetUserBySessionToken) — nil
	// for every other User value (e.g. ListUsers' DM picker), which
	// CanAccess treats as "nothing restricted" since reading a missing key
	// from a nil map returns the zero value.
	RestrictedModules map[string]bool
}

// CanAccess reports whether u may use module. A nil user is never
// accessible (an unauthenticated request has no business asking); anything
// not explicitly restricted defaults to accessible.
func (u *User) CanAccess(module string) bool {
	if u == nil {
		return false
	}
	return !u.RestrictedModules[module]
}

type Session struct {
	ID        string
	UserID    int64
	CreatedAt time.Time
	ExpiresAt time.Time
	UserAgent string
	IPAddress string
}
