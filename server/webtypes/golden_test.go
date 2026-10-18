package webtypes_test

// Test methodology: marshal a fully-populated instance of every response
// type and diff it against a checked-in golden file.
//
// These types have three consumers that Go cannot check for us — the
// generated TypeScript in frontend/src/lib/api/generated/, the spec in
// docs/openapi.yaml, and the frontend code reading the fields — so a renamed
// json tag or a changed type compiles fine and breaks all three silently.
// The golden turns that into a failing test with a readable diff.
//
// Run `go test ./server/webtypes/ -update` to accept an intended change,
// then `make types` and `make openapi` — which is what the failure message
// tells you to do.

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/openapidoc"
	"github.com/mnestor/ssoossh/server/webtypes"
)

var update = flag.Bool("update", false, "rewrite the golden files instead of comparing against them")

// issuedAt is a fixed timestamp with a non-UTC offset, so the goldens pin
// the serialization format (RFC 3339, offset preserved) and not just the
// field names. tygo maps time.Time to string on the strength of this.
var issuedAt = time.Date(2026, 8, 14, 9, 30, 0, 0, time.FixedZone("CEST", 2*60*60))

// fullFixtures are instances with every field set to a distinguishable
// non-zero value. Non-zero matters: a field with an omitempty tag vanishes
// from the JSON when it is zero, so a fixture built from a zero value would
// let a new optional field slip in without appearing in any golden.
// assertAllFieldsSet enforces it.
func fullFixtures() map[string]any {
	fipsEnforced := true
	options := webtypes.CertificateOptionsResponse{
		Extensions:      []string{"permit-pty", "permit-agent-forwarding"},
		ForceCommand:    "/usr/local/bin/audit-shell",
		SourceAddresses: []string{"198.51.100.0/24", "203.0.113.7/32"},
		NoTouchRequired: true,
	}

	certificateValidSeconds := 28800

	// One sync run, shared between its own fixture and the status response
	// that embeds it, so the two cannot describe different shapes.
	syncFinishedAt := issuedAt.Add(90 * time.Second)
	syncRun := webtypes.LDAPSyncRunResponse{
		ID:            "6f1d3b2a-8c4e-4f70-9a1b-2c3d4e5f6a7b",
		StartedAt:     issuedAt,
		FinishedAt:    &syncFinishedAt,
		Trigger:       "manual",
		DryRun:        true,
		ActorUsername: "alice",
		Instance:      "ssoosshd-1.example.org",
		UsersSeen:     42,
		Found:         39,
		Missing:       2,
		Failed:        1,
		Disabled:      1,
		Reenabled:     1,
		Error:         "the directory refused the bind",
		NSS: &webtypes.NSSSyncRunResponse{
			Full:       true,
			StartedAt:  issuedAt.Add(2 * time.Minute),
			FinishedAt: &syncFinishedAt,
			Accounts:   webtypes.NSSSyncCountsResponse{Seen: 120, Skipped: 1, Created: 3, Updated: 115, Refused: 1, Failed: 1, Missing: 2, Tombstoned: 1},
			Groups:     webtypes.NSSSyncCountsResponse{Seen: 40, Skipped: 2, Created: 1, Updated: 36, Refused: 1, Failed: 1, Missing: 1, Tombstoned: 1},
			Error:      "another NSS sync was still running",
		},
	}
	pendingUID, pendingGID := uint32(20001), uint32(20002)
	missingSince, deletedAt := issuedAt.Add(-48*time.Hour), issuedAt.Add(-24*time.Hour)
	callerUID, callerGID, callerPID, callerPPID := int64(1000), int64(1000), int64(4242), int64(4200)

	// The NSS fixtures: one account, one rotated and revoked key, and the
	// key's usage and rotation report, sharing timestamps so the shapes
	// cannot describe different moments.
	shellPref := "zsh"
	posixAccount := webtypes.PosixAccountResponse{UID: 10001, Name: "alice", ShellPref: &shellPref}
	keyRotatedAt := issuedAt.Add(24 * time.Hour)
	keyPreviousExpires := keyRotatedAt.Add(720 * time.Hour)
	keyRevokedAt := keyRotatedAt.Add(48 * time.Hour)
	keyRevokedBy := "4b7d0c55-1e2f-4a3b-8c9d-0e1f2a3b4c5d"
	keyLastUsed := keyRotatedAt.Add(time.Hour)
	keyMissRateAlert := keyRotatedAt.Add(30 * time.Minute)
	keySourceAlert := keyRotatedAt.Add(45 * time.Minute)
	nssKey := webtypes.NSSKeyResponse{
		ID:                "nss_k7m4qp2x",
		Name:              "web fleet",
		Description:       "every host in the web tier",
		GroupName:         "web-admins",
		Status:            "revoked",
		CurrentGeneration: 2,
		CreatedBy:         "9c1f0f8e-1d0a-4a37-9d1e-2f6a1b4c5d6e",
		CreatedByUsername: "alice",
		CreatedAt:         issuedAt,
		UpdatedAt:         keyRevokedAt,
		ExpiresAt:         issuedAt.Add(8760 * time.Hour),
		NotificationEmail: "web-admins@example.org",
		RotatedAt:         &keyRotatedAt,
		PreviousExpiresAt: &keyPreviousExpires,
		RevokedAt:         &keyRevokedAt,
		RevokedBy:         &keyRevokedBy,
		RevokedByUsername: "bob",
		Permissions:       []model.NSSKeyPermission{"view", "rotate", "revoke", "edit", "assign_group"},
		LastUsedAt:        &keyLastUsed,
		MissRateAlertAt:   &keyMissRateAlert,
		KeySourceAlertAt:  &keySourceAlert,
	}
	usageCursor := "1792022400.2.192.0.2.10"
	nssKeyExpires := issuedAt.Add(8760 * time.Hour)
	nssNotificationEmail := "web-admins@example.org"
	nssDescription := "every host in the web tier"
	nssGroup := "web-admins"

	return map[string]any{
		"posix_account":    posixAccount,
		"update_shell":     webtypes.UpdateShellBody{ShellPref: &shellPref},
		"nss_shells":       webtypes.NSSShellsResponse{AllowedShells: []string{"sh", "bash", "zsh", "fish"}},
		"nss_key_settings": webtypes.NSSKeySettingsResponse{DefaultLifetimeSeconds: 31536000, RotationGraceSeconds: 604800},
		"nss_key":          nssKey,
		"nss_key_list":     webtypes.NSSKeyListResponse{Keys: []webtypes.NSSKeyResponse{nssKey}, CanCreate: true},
		"nss_key_secret":   webtypes.NSSKeySecretResponse{Key: nssKey, Secret: "ssoossh_nss_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
		"create_nss_key":   webtypes.CreateNSSKeyBody{Name: "web fleet", Description: nssDescription, GroupName: nssGroup, ExpiresAt: &nssKeyExpires, NotificationEmail: &nssNotificationEmail},
		"update_nss_key":   webtypes.UpdateNSSKeyBody{Description: &nssDescription, ExpiresAt: &nssKeyExpires, NotificationEmail: &nssNotificationEmail, GroupName: &nssGroup},
		"nss_key_usage":    webtypes.NSSKeyUsageResponse{Rows: []webtypes.NSSKeyUsageRowResponse{{SourceIP: "192.0.2.10", Generation: 2, Day: time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC), Lookups: 1200, Misses: 7, FirstSeen: keyRotatedAt, LastSeen: keyRotatedAt.Add(time.Hour)}}, Total: 41, NextCursor: &usageCursor},
		"nss_rotation_report": webtypes.NSSRotationReportResponse{
			KeyID: "nss_k7m4qp2x", CurrentGeneration: 2, RotatedAt: &keyRotatedAt, PreviousExpiresAt: &keyPreviousExpires,
			GraceDaysLeft: 29, Switched: 40, NotSwitched: 2, Gone: 1,
			Rows: []webtypes.NSSRotationReportRowResponse{{SourceIP: "192.0.2.10", FirstSeen: issuedAt, LastSeen: keyRotatedAt.Add(time.Hour), Lookups: 1200, LastGeneration: 1, Bucket: "not_switched"}},
		},

		"current_user": webtypes.CurrentUserResponse{
			Subject:         "9c1f0f8e-1d0a-4a37-9d1e-2f6a1b4c5d6e",
			Username:        "alice",
			Email:           "alice@example.org",
			Name:            "Alice Ashworth",
			Groups:          []string{"engineering", "sre"},
			OtherAccounts:   []string{"alice.adm"},
			ServiceAccounts: []string{"svc-deploy"},
			// Wider than ServiceAccounts because
			// cert_options.service.allow_user_accounts is on in this
			// fixture: the approval picker also offers the accounts that
			// are the caller's own, and names which those are.
			ApprovableServiceAccounts: []string{"svc-deploy", "alice", "alice.adm"},
			UserOwnServiceAccounts:    []string{"alice", "alice.adm"},
			Extra: map[string]any{
				"employee_id": "E-12345",
				"department":  "Engineering",
			},
			IsAdmin:       true,
			IsSOC:         true,
			IsAuditor:     true,
			IsNSSKeyAdmin: true,
			NSSEnabled:    true,
			Posix:         &posixAccount,

			DetailLevel: model.DetailLevelAdvanced,
		},
		"certificate_options": options,
		"request_detail": webtypes.RequestDetailResponse{
			ID:            "0b4f2b1a-7c3d-4e5f-8a9b-0c1d2e3f4a5b",
			Type:          model.CertificateTypeUser,
			Status:        model.CertificateRequestStatusPending,
			SourceIP:      "198.51.100.7",
			ViewerIP:      "203.0.113.20",
			LocalUsername: "alice",
			LocalHostname: "alice-laptop",
			TargetAccount: "root",
			Hostname:      "web01",
			PAMService:    "login",
			TTY:           "tty1",
			RemoteHost:    "198.51.100.7",

			RequestingUser:        "alice",
			Process:               "sudo -i",
			CallerUID:             &callerUID,
			CallerGID:             &callerGID,
			CallerPID:             &callerPID,
			CallerPPID:            &callerPPID,
			MachineID:             "3f2c1e0d9b8a7f6e5d4c3b2a19080706",
			OS:                    "Debian GNU/Linux 13 (trixie) Linux 6.12.0",
			Client:                "pam_ssoossh-c/0.3.0",
			ClientMode:            "auto",
			ClientTime:            &issuedAt,
			TrustedCAFingerprints: []string{"SHA256:2Fd4rIWZ8kQnGx0mJvKp1YhLcTzXbA3sNeR5uW7oPqM"},

			ExpiresAt:    issuedAt,
			PublicKey:    "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample alice@laptop",
			Principals:   []string{"alice", "alice@example.org"},
			ValidSeconds: 28800,
			Requested:    options,
			Granted:      options,
			// The ceiling, deliberately wider than what this request was
			// granted: that difference is the whole reason the field
			// exists, so a fixture where they matched would prove nothing.
			SelectableExtensions: []string{"permit-agent-forwarding", "permit-port-forwarding", "permit-pty"},
			// A requesting machine under administrative policy, so the
			// shape of both halves is pinned — including that FIPS is a
			// pointer and so keeps "silent" distinct from "false".
			ClientPolicy: &webtypes.ClientPolicyResponse{
				ForbiddenExtensions: []string{"permit-port-forwarding"},
				FIPS:                &fipsEnforced,
			},
			CreatedAt:     issuedAt,
			ApprovalURL:   "/approve/0b4f2b1a-7c3d-4e5f-8a9b-0c1d2e3f4a5b",
			IsOwnedByYou:  true,
			AlreadyClosed: true,

			DecidedByOutcome:         "approved",
			DecidedBySubject:         "9c1f0f8e-1d0a-4a37-9d1e-2f6a1b4c5d6e",
			DecidedByUsername:        "alice",
			DecidedByEmail:           "alice@example.org",
			DecidedByGroups:          []string{"engineering", "sre"},
			DecidedByOtherAccounts:   []string{"alice.other"},
			DecidedByServiceAccounts: []string{"svc-backup"},
			DecidedSourceIP:          "198.51.100.7",
			DecidedUserAgent:         "curl/8.0.0",
			DecidedAcceptLanguage:    "en-US",
			DecidedForwardedFor:      "198.51.100.7, 10.0.0.1",
			DecidedAt:                &issuedAt,
			DecidedPrincipals:        []string{"alice", "alice.other"},
			DecidedGrantedOptions:    &options,
			DecidedPolicyExplanation: `{"v":1,"cert_type":"user","policy_configured":false,"ceiling":"8h0m0s","effective_duration":"8h0m0s"}`,
		},
		"notification_preferences": webtypes.NotificationPreferencesResponse{
			MailEnabled: true,
			Address:     "alice@example.org",
			Groups: []webtypes.NotificationGroupResponse{
				{
					Group:       "service_codes",
					Title:       "Service codes",
					Description: "The service codes on accounts you hold.",
				},
			},
			Kinds: []webtypes.NotificationKindResponse{
				{
					Kind:        "service_enrollment_created",
					Group:       "service_codes",
					Title:       "Service code created",
					Description: "Sent when you approve a service certificate request.",
					Enabled:     true,
				},
			},
			DetailLevel: model.DetailLevelSimplified,
		},
		"display_preferences": webtypes.DisplayPreferencesResponse{
			DetailLevel: model.DetailLevelSimplified,
		},
		"enrollment_retrievals": webtypes.EnrollmentRetrievalsResponse{
			Retrievals: []webtypes.EnrollmentRetrievalResponse{{
				RetrievedAt:       issuedAt,
				SourceIP:          "198.51.100.44",
				CertificateSerial: 7346115228134082560,
				Succeeded:         true,
			}},
			// Deliberately larger than the page: the truncated case is the
			// one the frontend has to render differently.
			Total: 8760,
		},
		"service_enrollment": webtypes.ServiceEnrollmentResponse{
			ID:                   "3a2b1c0d-9e8f-4a7b-8c6d-5e4f3a2b1c0d",
			ServiceAccount:       "svc-backup",
			ApprovedByUsername:   "alice",
			CertificateRequestID: "0b4f2b1a-7c3d-4e5f-8a9b-0c1d2e3f4a5b",
			Principals:           []string{"svc-backup"},
			KeyID:                "svc-backup/0b4f2b1a",
			PublicKeyFingerprint: "SHA256:2Fd4rIWZ8kQnGx0mJvKp1YhLcTzXbA3sNeR5uW7oPqM",
			Options:              options,
			// A pointer so the zero fixture can show the field vanishing:
			// an enrollment predating the lifetime split reports no
			// certificate duration at all.
			CertificateValidSeconds: &certificateValidSeconds,
			CreatedAt:               issuedAt,
			ExpiresAt:               issuedAt.Add(90 * 24 * time.Hour),
			FirstRedeemedAt:         &issuedAt,
			LastRetrievedAt:         &issuedAt,
			RetrievalCount:          17,
			NotificationEmail:       "backups@example.org",
		},
		"certificate": webtypes.CertificateResponse{
			ID:           "6d5c4b3a-2f1e-4d0c-9b8a-7f6e5d4c3b2a",
			Type:         model.CertificateTypeService,
			SerialNumber: 7346115228134082560,
			KeyID:        "alice@example.org/0b4f2b1a",
			Principals:   "alice,alice@example.org",
			Fingerprint:  "SHA256:2Fd4rIWZ8kQnGx0mJvKp1YhLcTzXbA3sNeR5uW7oPqM",
			IssuedAt:     issuedAt,
			ExpiresAt:    issuedAt.Add(8 * time.Hour),

			RetrievedSourceIP: "198.51.100.44",
			RetrievedAt:       &issuedAt,
			EnrollmentID:      "3a2b1c0d-9e8f-4a7b-8c6d-5e4f3a2b1c0d",

			Extensions:      []string{"permit-pty", "permit-agent-forwarding"},
			CriticalOptions: map[string]string{"force-command": "/usr/local/bin/audit-shell"},

			DecidedByOutcome:         "approved",
			DecidedBySubject:         "9c1f0f8e-1d0a-4a37-9d1e-2f6a1b4c5d6e",
			DecidedByUsername:        "alice",
			DecidedByEmail:           "alice@example.org",
			DecidedByGroups:          []string{"engineering", "sre"},
			DecidedByOtherAccounts:   []string{"alice.other"},
			DecidedByServiceAccounts: []string{"svc-backup"},
			DecidedSourceIP:          "198.51.100.7",
			DecidedUserAgent:         "curl/8.0.0",
			DecidedAcceptLanguage:    "en-US",
			DecidedForwardedFor:      "198.51.100.7, 10.0.0.1",
			DecidedAt:                &issuedAt,
			DecidedPrincipals:        []string{"alice", "alice.other"},
			DecidedGrantedOptions:    &options,
			DecidedPolicyExplanation: `{"v":1,"cert_type":"user","policy_configured":false,"ceiling":"8h0m0s","effective_duration":"8h0m0s"}`,

			ReportedUsername:       "root",
			ReportedHostname:       "rack07.example.org",
			ReportedService:        "sudo",
			ReportedTTY:            "pts/3",
			ReportedRemoteHost:     "203.0.113.9",
			ReportedRequestingUser: "alice",
			ReportedProcess:        "sudo systemctl restart nginx",
			ReportedMachineID:      "9f2c1e7b8a3d4f6099b1c2d3e4f5a6b7",
			ReportedClient:         "pam_ssoossh-c/v1.2.0",
		},
		"ldap_status": webtypes.LDAPStatusResponse{
			Enabled:               true,
			URL:                   "ldaps://dir.example.net",
			BaseDN:                "dc=example,dc=net",
			UserFilter:            "(&(objectClass=person)(uid={{.Username}}))",
			ConfiguredAttributes:  []string{"altSecurityIdentities", "memberOf"},
			SyncIntervalSeconds:   900,
			DisableAfterSeconds:   2700,
			Reenable:              true,
			TLSInsecureSkipVerify: true,
			Running:               true,
			LastRun:               &syncRun,
		},
		"ldap_sync_run": syncRun,
		"nss_pending_id_changes": webtypes.NSSPendingIDChangesResponse{
			Users: []webtypes.NSSPendingUserIDChange{{
				Name: "ada.lovelace", DN: "uid=ada.lovelace,ou=people,dc=example,dc=net", UID: 10001, GID: 10001,
				PendingUID: &pendingUID, PendingGID: &pendingGID, PendingSince: issuedAt,
			}},
			Groups: []webtypes.NSSPendingGroupIDChange{{
				Name: "engineering", DN: "cn=engineering,ou=groups,dc=example,dc=net", GID: 5000, PendingGID: 5001, PendingSince: issuedAt,
			}},
		},
		"nss_accepted_user_id_change": webtypes.NSSAcceptedUserIDChangeResponse{
			Name: "ada.lovelace", DN: "uid=ada.lovelace,ou=people,dc=example,dc=net", UID: 20001, GID: 20002,
		},
		"nss_accepted_group_id_change": webtypes.NSSAcceptedGroupIDChangeResponse{
			Name: "engineering", DN: "cn=engineering,ou=groups,dc=example,dc=net", GID: 5001,
		},
		"nss_collisions": webtypes.NSSCollisionsResponse{
			Collisions: []webtypes.NSSCollisionResponse{{
				Kind:  "uid",
				Value: "10001",
				Holder: webtypes.NSSCollisionEntryResponse{
					Name: "ada.lovelace", DN: "uid=ada.lovelace,ou=people,dc=example,dc=net", Number: 10001,
					Stored: true, Tombstone: true, MissingSince: &missingSince, DeletedAt: &deletedAt,
				},
				Other: webtypes.NSSCollisionEntryResponse{
					Name: "ada", DN: "uid=ada,ou=contractors,dc=example,dc=net", Number: 10001,
					Stored: true, Tombstone: true, MissingSince: &missingSince, DeletedAt: &deletedAt,
				},
				FirstSeenAt: issuedAt.Add(-time.Hour),
				LastSeenAt:  issuedAt,
			}},
		},
		"nss_purged_tombstone": webtypes.NSSPurgedTombstoneResponse{
			Name: "ada.lovelace", DN: "uid=ada.lovelace,ou=people,dc=example,dc=net", Number: 10001,
		},
		"ldap_probe": webtypes.LDAPProbeResponse{
			BaseDN:      "dc=example,dc=net",
			FilterSent:  "(&(objectClass=person)(uid=alice))",
			Mode:        "template",
			Attributes:  []string{"*", "entryUUID"},
			Matched:     1,
			IDAttribute: "entryUUID",
			DirectoryID: "8f14e45f-ea8f-4f2d-9c1b-3a7b5d2e6c40",
			Entry: &webtypes.LDAPProbeEntry{
				DN: "uid=alice,ou=People,dc=example,dc=net",
				Attributes: []webtypes.LDAPProbeAttribute{{
					Name:            "memberOf",
					Values:          []string{"cn=ssh-users,ou=Groups,dc=example,dc=net"},
					Configured:      true,
					TruncatedValues: 3,
				}},
			},
			Fields: []webtypes.LDAPProbeField{{
				Name:             "groups",
				Attribute:        "memberOf",
				AttributePresent: true,
				AttributeValues:  []string{"cn=ssh-users,ou=Groups,dc=example,dc=net"},
				Searches: []webtypes.LDAPProbeSearch{{
					Name:       "owned-services",
					BaseDN:     "ou=Services,dc=example,dc=net",
					FilterSent: "(owner=uid=alice,ou=People,dc=example,dc=net)",
					Value:      "uid",
					Entries:    1,
					Values:     []string{"svc-deploy"},
					Error:      "the search hit its size limit",
				}},
				Values: []string{"ssh-users"},
				Error:  "one search failed",
			}},
			Merge: []webtypes.LDAPProbeMerge{{
				Name:    "groups",
				Action:  "persist-groups",
				Kept:    []string{"ssh-users"},
				Dropped: []string{"vpn-legacy"},
				Note:    "stored as user_groups rows",
			}},
			Suggestions: []webtypes.LDAPProbeSuggestion{{
				Reason: "employeeType is on the entry but no configured field reads it",
				YAML:   "ldap:\n  fields:\n    employee_type: employeeType",
			}},
			ElapsedMS:             34,
			TimeoutMS:             5000,
			TLSInsecureSkipVerify: true,
			Wrote:                 true,
		},
	}
}

// zeroFixtures are the same types with nothing set. They document which
// fields disappear under omitempty, which is exactly the set the generated
// TypeScript marks optional — a field that becomes optional without the
// frontend learning about it is how a "cannot read property of undefined"
// reaches production.
func zeroFixtures() map[string]any {
	return map[string]any{
		"current_user":                 webtypes.CurrentUserResponse{},
		"certificate_options":          webtypes.CertificateOptionsResponse{},
		"request_detail":               webtypes.RequestDetailResponse{},
		"certificate":                  webtypes.CertificateResponse{},
		"service_enrollment":           webtypes.ServiceEnrollmentResponse{},
		"enrollment_retrievals":        webtypes.EnrollmentRetrievalsResponse{},
		"notification_preferences":     webtypes.NotificationPreferencesResponse{},
		"display_preferences":          webtypes.DisplayPreferencesResponse{},
		"ldap_status":                  webtypes.LDAPStatusResponse{},
		"ldap_sync_run":                webtypes.LDAPSyncRunResponse{},
		"ldap_probe":                   webtypes.LDAPProbeResponse{},
		"nss_pending_id_changes":       webtypes.NSSPendingIDChangesResponse{},
		"nss_accepted_user_id_change":  webtypes.NSSAcceptedUserIDChangeResponse{},
		"nss_accepted_group_id_change": webtypes.NSSAcceptedGroupIDChangeResponse{},
		"nss_collisions":               webtypes.NSSCollisionsResponse{},
		"nss_purged_tombstone":         webtypes.NSSPurgedTombstoneResponse{},
		"posix_account":                webtypes.PosixAccountResponse{},
		"update_shell":                 webtypes.UpdateShellBody{},
		"nss_shells":                   webtypes.NSSShellsResponse{},
		"nss_key":                      webtypes.NSSKeyResponse{},
		"nss_key_settings":             webtypes.NSSKeySettingsResponse{},
		"nss_key_list":                 webtypes.NSSKeyListResponse{},
		"nss_key_secret":               webtypes.NSSKeySecretResponse{},
		"create_nss_key":               webtypes.CreateNSSKeyBody{},
		"update_nss_key":               webtypes.UpdateNSSKeyBody{},
		"nss_key_usage":                webtypes.NSSKeyUsageResponse{},
		"nss_rotation_report":          webtypes.NSSRotationReportResponse{},
	}
}

// errorFixtures are fully-populated error response envelopes, one for each
// error code. The envelope shape is part of the wire contract: changes to
// error_code or error require regenerating the spec and updating the frontend.
func errorFixtures() map[string]any {
	return map[string]any{
		"error_invalid_request": openapidoc.ErrorEnvelope{
			Data:      nil,
			Error:     "request body was malformed or a validation check failed",
			ErrorCode: apitypes.ErrorCodeInvalidRequest,
		},
		"error_unauthenticated": openapidoc.ErrorEnvelope{
			Data:      nil,
			Error:     "no valid session",
			ErrorCode: apitypes.ErrorCodeUnauthenticated,
		},
		"error_forbidden": openapidoc.ErrorEnvelope{
			Data:      nil,
			Error:     "access denied to this resource",
			ErrorCode: apitypes.ErrorCodeForbidden,
		},
		"error_not_found": openapidoc.ErrorEnvelope{
			Data:      nil,
			Error:     "certificate request \"9f1c0b2a-...\" not found",
			ErrorCode: apitypes.ErrorCodeNotFound,
		},
		"error_unavailable": openapidoc.ErrorEnvelope{
			Data:      nil,
			Error:     "certificate for request \"9f1c0b2a-...\" is no longer available",
			ErrorCode: apitypes.ErrorCodeUnavailable,
		},
		"error_rate_limited": openapidoc.ErrorEnvelope{
			Data:      nil,
			Error:     "too many requests",
			ErrorCode: apitypes.ErrorCodeRateLimited,
		},
		"error_not_implemented": openapidoc.ErrorEnvelope{
			Data:      nil,
			Error:     "not implemented",
			ErrorCode: apitypes.ErrorCodeNotImplemented,
		},
		"error_internal_error": openapidoc.ErrorEnvelope{
			Data:      nil,
			Error:     "internal server error",
			ErrorCode: apitypes.ErrorCodeInternalError,
		},
	}
}

func TestFullFixturesShouldMatchTheirGoldenJSON(t *testing.T) {
	for name, fixture := range fullFixtures() {
		t.Run(name, func(t *testing.T) {
			assertAllFieldsSet(t, reflect.ValueOf(fixture), name)
			assertGolden(t, filepath.Join("testdata", name+".full.json"), fixture)
		})
	}
}

func TestZeroFixturesShouldMatchTheirGoldenJSON(t *testing.T) {
	for name, fixture := range zeroFixtures() {
		t.Run(name, func(t *testing.T) {
			assertGolden(t, filepath.Join("testdata", name+".zero.json"), fixture)
		})
	}
}

func TestErrorFixturesShouldMatchTheirGoldenJSON(t *testing.T) {
	for name, fixture := range errorFixtures() {
		t.Run(name, func(t *testing.T) {
			// Error responses intentionally have Data=nil, so skip assertAllFieldsSet.
			// The important part is that error_code doesn't drift from the spec.
			assertGolden(t, filepath.Join("testdata", name+".json"), fixture)
		})
	}
}

// assertGolden compares fixture's JSON against the file at path, or rewrites
// the file when -update is set.
func assertGolden(t *testing.T, path string, fixture any) {
	t.Helper()

	encoded, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal fixture: %v", err)
	}
	encoded = append(encoded, '\n')

	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("failed to create testdata directory: %v", err)
		}
		//nolint:gosec // golden files are committed artifacts meant to be world-readable
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatalf("failed to write golden %s: %v", path, err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read golden %s: %v\nrun: go test ./server/webtypes/ -update", path, err)
	}

	if string(encoded) != string(want) {
		t.Errorf(`the wire shape changed.

golden %s
want:
%s
got:
%s
If this change is intended, then in the same commit:
  1. go test ./server/webtypes/ -update
  2. make types            (regenerates frontend/src/lib/api/generated/)
  3. make openapi          (regenerates docs/openapi.yaml)
Anything less leaves the frontend or the spec describing a response the
server no longer sends.`, path, want, encoded)
	}
}

// assertAllFieldsSet fails when any field reachable from v is still at its
// zero value, naming the path to it. Recurses into nested structs so a field
// added to CertificateOptionsResponse is caught inside RequestDetailResponse
// too.
//
// This is the half that catches additions. The golden diff catches renames
// and retypes on its own, but a new field with a zero value and an omitempty
// tag would produce an identical golden and slip through.
func assertAllFieldsSet(t *testing.T, v reflect.Value, path string) {
	t.Helper()

	if v.Kind() != reflect.Struct {
		return
	}

	for i := 0; i < v.NumField(); i++ {
		field := v.Type().Field(i)
		if !field.IsExported() {
			continue
		}

		value := v.Field(i)
		fieldPath := path + "." + field.Name

		if value.IsZero() {
			t.Errorf("%s is unset in the full fixture; every field has to carry a non-zero value or omitempty could hide it from the golden", fieldPath)
			continue
		}

		assertAllFieldsSet(t, value, fieldPath)
	}
}
