package controller

// Test methodology: drive the disable and re-enable routes through the
// real-middleware router and read disabled_source back from the row. What
// matters is the column the directory sync acts on: an operator's disable
// always carries the operator's role, never a source left over from an
// earlier automatic disable, and a re-enable leaves no source behind for
// the next disable to inherit. The sync's side of that contract is pinned
// in server/service.

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/test/testdb"
)

// seedSourceUsers writes the target and the two operators the disable
// tests act as.
func seedSourceUsers(t *testing.T, db *gorm.DB) {
	t.Helper()

	for _, u := range []model.User{
		{ID: "u-target", Subject: "sub-target", Username: "target"},
		{ID: "u-admin", Subject: "sub-admin", Username: "admin"},
		{ID: "u-soc", Subject: "sub-soc", Username: "soc"},
	} {
		u.CreatedAt = time.Now()
		u.UpdatedAt = time.Now()
		if err := db.Create(&u).Error; err != nil {
			t.Fatalf("seed %s: %v", u.ID, err)
		}
	}
}

// storedSource reads the target's disabled_source back, nil when unset.
func storedSource(t *testing.T, db *gorm.DB) *model.DisabledSource {
	t.Helper()

	var user model.User
	if err := db.First(&user, "id = ?", "u-target").Error; err != nil {
		t.Fatalf("reload target: %v", err)
	}
	return user.DisabledSource
}

func TestDisableUserHandler_ShouldRecordTheRoleItWasTakenUnder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// subject and groups are the caller's.
		subject string
		groups  []string
		// prior is the target's state before the disable: nil leaves it
		// enabled with no source.
		prior map[string]any
		want  model.DisabledSource
	}{
		{
			name:    "should record admin when an admin disables",
			subject: "sub-admin",
			groups:  []string{"ssh-admins"},
			want:    model.DisabledSourceAdmin,
		},
		{
			name:    "should record soc when a SOC member disables",
			subject: "sub-soc",
			groups:  []string{"ssh-soc"},
			want:    model.DisabledSourceSOC,
		},
		{
			name:    "should record admin when the caller holds both groups",
			subject: "sub-admin",
			groups:  []string{"ssh-soc", "ssh-admins"},
			want:    model.DisabledSourceAdmin,
		},
		{
			// The sync would clear a disable still marked as its own.
			name:    "should take over a disable the directory sync performed",
			subject: "sub-soc",
			groups:  []string{"ssh-soc"},
			prior: map[string]any{
				"disabled_at":     time.Now().Add(-time.Hour),
				"disabled_source": model.DisabledSourceLDAPSync,
				"disabled_reason": "directory entry not found",
			},
			want: model.DisabledSourceSOC,
		},
		{
			// What a re-enable used to leave behind.
			name:    "should replace a stale ldap_sync source on an enabled account",
			subject: "sub-soc",
			groups:  []string{"ssh-soc"},
			prior:   map[string]any{"disabled_source": model.DisabledSourceLDAPSync},
			want:    model.DisabledSourceSOC,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := testdb.New(t)
			seedSourceUsers(t, db)
			if tt.prior != nil {
				if err := db.Model(&model.User{}).Where("id = ?", "u-target").
					Updates(tt.prior).Error; err != nil {
					t.Fatalf("set the prior state: %v", err)
				}
			}

			r := routerWithAuth(t, newTestConfig(t), db, &service.Identity{
				Subject: tt.subject, Username: "operator", Groups: tt.groups,
			})

			w := httptest.NewRecorder()
			r.ServeHTTP(w, jsonPatch(t, "/admin/users/u-target/disable", `{"reason":"containment, SEC-1234"}`))
			if w.Code != http.StatusOK {
				t.Fatalf("PATCH .../disable = %d, want 200, body: %s", w.Code, w.Body.String())
			}

			got := storedSource(t, db)
			if got == nil || *got != tt.want {
				t.Errorf("disabled_source = %v, want %q", got, tt.want)
			}
		})
	}
}

func TestEnableUserHandler_ShouldClearTheDisableSource(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedSourceUsers(t, db)
	if err := db.Model(&model.User{}).Where("id = ?", "u-target").
		Updates(map[string]any{
			"disabled_at":     time.Now().Add(-time.Hour),
			"disabled_source": model.DisabledSourceLDAPSync,
			"disabled_reason": "directory entry not found",
		}).Error; err != nil {
		t.Fatalf("disable the target: %v", err)
	}

	r := routerWithAuth(t, newTestConfig(t), db, &service.Identity{
		Subject: "sub-admin", Username: "admin", Groups: []string{"ssh-admins"},
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonPatch(t, "/admin/users/u-target/enable", `{"reason":"entry restored, SEC-1234"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH .../enable = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	if got := storedSource(t, db); got != nil {
		t.Errorf("disabled_source = %q, want NULL after a re-enable", *got)
	}
}
