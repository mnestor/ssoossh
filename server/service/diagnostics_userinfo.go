package service

import (
	"context"
	"fmt"
	"time"

	"github.com/mnestor/ssoossh/server/model"
)

// UserInfoDiagnostics is what the UserInfo check reads. *AuthService
// implements it.
type UserInfoDiagnostics interface {
	// UserInfoEnabled reports authentication.userinfo.
	UserInfoEnabled() bool
	// UserInfoEndpoint is discovery's userinfo_endpoint, or "".
	UserInfoEndpoint() string
	// UserInfoState is the shared health row.
	UserInfoState(ctx context.Context) (model.UserInfoHealth, error)
}

// UserInfoState implements UserInfoDiagnostics: the shared health row, or
// the healthy zero state when none is wired.
func (s *AuthService) UserInfoState(ctx context.Context) (model.UserInfoHealth, error) {
	if s.userInfoHealth == nil {
		return model.UserInfoHealth{ID: model.UserInfoHealthID}, nil
	}
	return s.userInfoHealth.State(ctx)
}

// SetUserInfo attaches the UserInfo check. Without it the report has four
// checks, as it had before UserInfo existed.
func (s *DiagnosticsService) SetUserInfo(u UserInfoDiagnostics) { s.userInfo = u }

// checkUserInfo reports the UserInfo endpoint: whether the config calls
// it, whether discovery names one, and its shared health: healthy,
// failing N in a row, or paused since when with the next probe, plus the
// last error and the last success.
func (s *DiagnosticsService) checkUserInfo(ctx context.Context) DiagnosticCheck {
	check := DiagnosticCheck{ID: "userinfo", Title: "OIDC UserInfo endpoint"}
	u := s.userInfo

	if !u.UserInfoEnabled() {
		check.Status = DiagnosticSkipped
		check.Summary = "authentication.userinfo is off, so logins read the ID token alone."
		check.Findings = []string{"authentication.userinfo: off"}
		return check
	}
	endpoint := u.UserInfoEndpoint()
	if endpoint == "" {
		check.Status = DiagnosticSkipped
		check.Summary = "The provider's discovery document names no userinfo_endpoint, so there is nothing to call."
		check.Findings = []string{"authentication.userinfo: on", "userinfo_endpoint: not in discovery"}
		return check
	}

	check.Findings = []string{"authentication.userinfo: on", "userinfo_endpoint: " + endpoint}
	state, err := u.UserInfoState(ctx)
	if err != nil {
		check.Status = DiagnosticSkipped
		check.Summary = "Could not read the endpoint's health from the database."
		check.Findings = append(check.Findings, err.Error())
		return check
	}

	switch {
	case state.Paused:
		check.Status = DiagnosticCritical
		check.Summary = fmt.Sprintf("Paused since %s: logins read the ID token alone.", formatDiagnosticTime(state.PausedAt))
		check.Findings = append(check.Findings, "Next probe: "+formatDiagnosticTime(state.NextProbeAt))
		check.Remediation = "Fix what the last error names, at the provider or on the network between them. The first login after the next probe time retries the call, and the calls resume by themselves once it succeeds."
	case state.ConsecutiveFailures > 0:
		check.Status = DiagnosticWarn
		check.Summary = fmt.Sprintf("Failing: %d call(s) in a row; the calls pause at %d.", state.ConsecutiveFailures, DefaultUserInfoPolicy().PauseAfter)
		check.Remediation = "Fix what the last error names. A success resets the count."
	default:
		check.Status = DiagnosticOK
		check.Summary = "Healthy."
	}
	if state.LastError != "" {
		check.Findings = append(check.Findings, "Last error: "+state.LastError+" (at "+formatDiagnosticTime(state.LastFailureAt)+")")
	}
	check.Findings = append(check.Findings, "Last success: "+formatDiagnosticTime(state.LastSuccessAt))
	return check
}

// formatDiagnosticTime renders an optional time for a finding.
func formatDiagnosticTime(t *time.Time) string {
	if t == nil {
		return "never"
	}
	return t.UTC().Format(time.RFC3339)
}
