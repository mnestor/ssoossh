package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
	"github.com/mnestor/ssoossh/test/testdb"
)

// detailAdmin is an admin block with both role groups configured, so the
// default can be told apart for each.
func detailAdmin() *config.AdminConfig {
	return &config.AdminConfig{RequireGroup: "admins", SOCGroup: "soc"}
}

// newDetailLevelFixture is a DetailLevelService over a users table holding
// one user, subject "sub-1", with neither level set.
func newDetailLevelFixture(t *testing.T) (*DetailLevelService, *gorm.DB) {
	t.Helper()

	db := testdb.New(t)
	user := model.User{ID: uuid.NewString(), Subject: "sub-1", Username: "alice", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to create the user: %v", err)
	}
	return NewDetailLevelService(db, detailAdmin()), db
}

// storedLevels reads the two columns back as stored, nil for unset.
func storedLevels(t *testing.T, db *gorm.DB, subject string) (web, email *model.DetailLevel) {
	t.Helper()
	var user model.User
	if err := db.First(&user, "subject = ?", subject).Error; err != nil {
		t.Fatalf("failed to read the user: %v", err)
	}
	return user.WebDetailLevel, user.EmailDetailLevel
}

func TestDefaultDetailLevel(t *testing.T) {
	tests := []struct {
		name   string
		groups []string
		want   model.DetailLevel
	}{
		{name: "should start an admin on advanced", groups: []string{"admins"}, want: model.DetailLevelAdvanced},
		{name: "should start a SOC member on advanced", groups: []string{"soc"}, want: model.DetailLevelAdvanced},
		{name: "should start everyone else on simplified", groups: []string{"developers"}, want: model.DetailLevelSimplified},
		{name: "should start someone with no groups on simplified", groups: nil, want: model.DetailLevelSimplified},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DefaultDetailLevel(detailAdmin(), tc.groups); got != tc.want {
				t.Errorf("DefaultDetailLevel(%v) = %q, want %q", tc.groups, got, tc.want)
			}
		})
	}
}

func TestLevels_shouldFillUnsetLevelsFromTheSessionRoles(t *testing.T) {
	svc, db := newDetailLevelFixture(t)

	got, err := svc.Levels(context.Background(), &Identity{Subject: "sub-1", Groups: []string{"soc"}})
	if err != nil {
		t.Fatalf("Levels: %v", err)
	}

	want := DetailLevels{Web: model.DetailLevelAdvanced, Email: model.DetailLevelAdvanced}
	if got != want {
		t.Errorf("Levels = %+v, want %+v", got, want)
	}
	web, email := storedLevels(t, db, "sub-1")
	if web == nil || email == nil {
		t.Errorf("stored web=%v email=%v, want both filled", web, email)
	}
}

// The user's ruling: the default is read once, and a later change of groups
// does not move a level the person already has.
func TestLevels_shouldNotMoveAStoredLevelWhenGroupsChange(t *testing.T) {
	svc, _ := newDetailLevelFixture(t)
	ctx := context.Background()

	if _, err := svc.Levels(ctx, &Identity{Subject: "sub-1", Groups: []string{"developers"}}); err != nil {
		t.Fatalf("first Levels: %v", err)
	}
	got, err := svc.Levels(ctx, &Identity{Subject: "sub-1", Groups: []string{"admins"}})
	if err != nil {
		t.Fatalf("second Levels: %v", err)
	}

	if got.Web != model.DetailLevelSimplified || got.Email != model.DetailLevelSimplified {
		t.Errorf("Levels after promotion = %+v, want both still simplified", got)
	}
}

func TestLevels_shouldRefuseAnIdentityWithNoUserRecord(t *testing.T) {
	svc, _ := newDetailLevelFixture(t)

	_, err := svc.Levels(context.Background(), &Identity{Subject: "nobody"})

	var forbidden *errorresponses.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Errorf("Levels error = %v, want ForbiddenError", err)
	}
}

func TestSetLevels(t *testing.T) {
	tests := []struct {
		name      string
		set       func(*DetailLevelService, context.Context, *Identity, model.DetailLevel) error
		wantWeb   model.DetailLevel
		wantEmail model.DetailLevel
	}{
		{
			name:      "should change only the web level when SetWeb is called",
			set:       (*DetailLevelService).SetWeb,
			wantWeb:   model.DetailLevelAdvanced,
			wantEmail: model.DetailLevelSimplified,
		},
		{
			name:      "should change only the email level when SetEmail is called",
			set:       (*DetailLevelService).SetEmail,
			wantWeb:   model.DetailLevelSimplified,
			wantEmail: model.DetailLevelAdvanced,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newDetailLevelFixture(t)
			ctx := context.Background()
			identity := &Identity{Subject: "sub-1"}

			if err := tc.set(svc, ctx, identity, model.DetailLevelAdvanced); err != nil {
				t.Fatalf("set: %v", err)
			}
			got, err := svc.Levels(ctx, identity)
			if err != nil {
				t.Fatalf("Levels: %v", err)
			}
			if got.Web != tc.wantWeb || got.Email != tc.wantEmail {
				t.Errorf("Levels = %+v, want web %q email %q", got, tc.wantWeb, tc.wantEmail)
			}
		})
	}
}

func TestSetWeb_shouldRejectAnUnknownLevel(t *testing.T) {
	svc, db := newDetailLevelFixture(t)

	err := svc.SetWeb(context.Background(), &Identity{Subject: "sub-1"}, "verbose")

	var invalid *errorresponses.InvalidRequestError
	if !errors.As(err, &invalid) {
		t.Fatalf("SetWeb error = %v, want InvalidRequestError", err)
	}
	if web, _ := storedLevels(t, db, "sub-1"); web != nil {
		t.Errorf("stored web level = %q, want nothing written", *web)
	}
}

func TestSetEmail_shouldRefuseAnIdentityWithNoUserRecord(t *testing.T) {
	svc, _ := newDetailLevelFixture(t)

	err := svc.SetEmail(context.Background(), &Identity{Subject: "nobody"}, model.DetailLevelAdvanced)

	var forbidden *errorresponses.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Errorf("SetEmail error = %v, want ForbiddenError", err)
	}
}

func TestFillDetailLevels_shouldLeaveAChosenLevelAlone(t *testing.T) {
	svc, db := newDetailLevelFixture(t)
	ctx := context.Background()
	if err := svc.SetEmail(ctx, &Identity{Subject: "sub-1"}, model.DetailLevelAdvanced); err != nil {
		t.Fatalf("SetEmail: %v", err)
	}

	if err := fillDetailLevels(ctx, db, "sub-1", model.DetailLevelSimplified); err != nil {
		t.Fatalf("fillDetailLevels: %v", err)
	}

	web, email := storedLevels(t, db, "sub-1")
	if web == nil || *web != model.DetailLevelSimplified {
		t.Errorf("web level = %v, want the unset one filled with simplified", web)
	}
	if email == nil || *email != model.DetailLevelAdvanced {
		t.Errorf("email level = %v, want the chosen advanced kept", email)
	}
}
