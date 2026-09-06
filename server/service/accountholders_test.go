package service

// Test methodology: unit tests over usersHoldingAccount against in-memory
// SQLite, because it is the one answer two features share -- the "Who has
// access" panel and enrollment notification fan-out -- and it has to match a
// third, IdentityService.RefreshIdentity, which is what actually authorizes
// a request. The cases are therefore the places the two sources disagree:
// an account only the directory knows about, one only the claim column
// knows about, and one the directory has taken away.

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
)

// holdersDB is an in-memory database with the two tables the resolver reads.
func holdersDB(t *testing.T) *gorm.DB {
	t.Helper()

	db := newTestDB(t)
	if err := db.AutoMigrate(&model.User{}, &model.UserLDAP{}); err != nil {
		t.Fatalf("failed to migrate test tables: %v", err)
	}
	return db
}

// addHolder inserts one users row. serviceAccounts and otherAccounts are
// stored verbatim so a case can plant "null" -- which is what login writes
// for an identity whose claim carried no accounts -- or malformed JSON.
func addHolder(t *testing.T, db *gorm.DB, username, serviceAccounts, otherAccounts string) model.User {
	t.Helper()

	user := model.User{
		ID:              uuid.NewString(),
		Subject:         "sub-" + username,
		Username:        username,
		Email:           username + "@example.com",
		ServiceAccounts: serviceAccounts,
		OtherAccounts:   otherAccounts,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to create user %s: %v", username, err)
	}
	return user
}

// addDirectoryRow records what the directory sync last read for a user,
// stored verbatim so a case can plant an unreadable value.
func addDirectoryRow(t *testing.T, db *gorm.DB, userID, attributes string) {
	t.Helper()

	row := model.UserLDAP{
		UserID:     userID,
		DN:         "uid=" + userID + ",ou=people,dc=example,dc=com",
		Attributes: attributes,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("failed to create directory row: %v", err)
	}
}

// holdingUsernames flattens a resolution for comparison.
func holdingUsernames(holdings []accountHolding) []string {
	out := make([]string, 0, len(holdings))
	for _, holding := range holdings {
		out = append(out, holding.User.Username)
	}
	return out
}

func TestUsersHoldingAccount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// seed plants the users and directory rows for the case.
		seed             func(t *testing.T, db *gorm.DB)
		account          string
		ldapEnabled      bool
		allowUserAccount bool
		want             []string
	}{
		{
			name: "should find the holder named by the claim column",
			seed: func(t *testing.T, db *gorm.DB) {
				addHolder(t, db, "alice", `["svc-a"]`, `[]`)
				addHolder(t, db, "bob", `["svc-b"]`, `[]`)
			},
			account: "svc-a",
			want:    []string{"alice"},
		},
		// The bug this resolver exists for. A directory deployment writes
		// the accounts it resolves to user_ldap and leaves the claim column
		// as login found it, which is "null" whenever the OIDC token
		// carried no service_accounts claim. Reading the users row alone
		// answered "nobody holds this" for every account in the deployment.
		{
			name: "should find the holder the directory names when the claim column is empty",
			seed: func(t *testing.T, db *gorm.DB) {
				alice := addHolder(t, db, "alice", `null`, `null`)
				addDirectoryRow(t, db, alice.ID, `{"service_accounts":["svc-a"],"other_accounts":[]}`)
			},
			account:     "svc-a",
			ldapEnabled: true,
			want:        []string{"alice"},
		},
		// applyLDAPValues assigns rather than merges, so a directory that
		// answers for the field answers for all of it. Someone the claim
		// column still names has lost the account, and their session says
		// so on its next refresh.
		{
			name: "should drop a holder the directory no longer names",
			seed: func(t *testing.T, db *gorm.DB) {
				alice := addHolder(t, db, "alice", `["svc-a"]`, `[]`)
				addDirectoryRow(t, db, alice.ID, `{"service_accounts":[]}`)
			},
			account:     "svc-a",
			ldapEnabled: true,
			want:        []string{},
		},
		// A directory row that carries no answer for the field leaves the
		// claim column as the answer, the same way the overlay does.
		{
			name: "should keep the claim when the directory row does not carry the field",
			seed: func(t *testing.T, db *gorm.DB) {
				alice := addHolder(t, db, "alice", `["svc-a"]`, `[]`)
				addDirectoryRow(t, db, alice.ID, `{"title":["ops"]}`)
			},
			account:     "svc-a",
			ldapEnabled: true,
			want:        []string{"alice"},
		},
		// With the directory switched off nothing refreshes these rows and
		// nothing removes them, so they are frozen where they were -- the
		// same reason GroupRecipients ignores directory-sourced group rows.
		{
			name: "should ignore directory attributes while the directory is off",
			seed: func(t *testing.T, db *gorm.DB) {
				alice := addHolder(t, db, "alice", `null`, `null`)
				addDirectoryRow(t, db, alice.ID, `{"service_accounts":["svc-a"]}`)
			},
			account:     "svc-a",
			ldapEnabled: false,
			want:        []string{},
		},
		{
			name: "should not match an account name that merely contains the one asked for",
			seed: func(t *testing.T, db *gorm.DB) {
				addHolder(t, db, "alice", `["svc-a"]`, `[]`)
				addHolder(t, db, "bob", `["svc-append"]`, `[]`)
			},
			account: "svc-a",
			want:    []string{"alice"},
		},
		{
			name: "should skip a user whose stored accounts do not parse",
			seed: func(t *testing.T, db *gorm.DB) {
				addHolder(t, db, "alice", `["svc-a"]`, `[]`)
				addHolder(t, db, "bob", `not json at all "svc-a"`, `[]`)
			},
			account: "svc-a",
			want:    []string{"alice"},
		},
		// An unreadable directory row must not decide the answer either
		// way: the user keeps whatever their claim column says, which is
		// what a session with an unreadable overlay also keeps.
		{
			name: "should fall back to the claim when the directory row does not parse",
			seed: func(t *testing.T, db *gorm.DB) {
				alice := addHolder(t, db, "alice", `["svc-a"]`, `[]`)
				addDirectoryRow(t, db, alice.ID, `{"service_accounts": nope}`)
			},
			account:     "svc-a",
			ldapEnabled: true,
			want:        []string{"alice"},
		},
		{
			name: "should ignore a personal account while allow_user_accounts is off",
			seed: func(t *testing.T, db *gorm.DB) {
				addHolder(t, db, "alice", `[]`, `["svc-a"]`)
			},
			account: "svc-a",
			want:    []string{},
		},
		{
			name: "should find the person whose own username is the account",
			seed: func(t *testing.T, db *gorm.DB) {
				addHolder(t, db, "svc-a", `[]`, `[]`)
			},
			account:          "svc-a",
			allowUserAccount: true,
			want:             []string{"svc-a"},
		},
		{
			name: "should find the person whose other accounts carry the account",
			seed: func(t *testing.T, db *gorm.DB) {
				addHolder(t, db, "alice", `[]`, `["svc-a"]`)
			},
			account:          "svc-a",
			allowUserAccount: true,
			want:             []string{"alice"},
		},
		// The directory replaces other_accounts on the same terms.
		{
			name: "should drop a personal account the directory no longer names",
			seed: func(t *testing.T, db *gorm.DB) {
				alice := addHolder(t, db, "alice", `[]`, `["svc-a"]`)
				addDirectoryRow(t, db, alice.ID, `{"other_accounts":[]}`)
			},
			account:          "svc-a",
			ldapEnabled:      true,
			allowUserAccount: true,
			want:             []string{},
		},
		{
			name: "should list every holder in username order",
			seed: func(t *testing.T, db *gorm.DB) {
				addHolder(t, db, "carol", `["svc-a"]`, `[]`)
				alice := addHolder(t, db, "alice", `null`, `null`)
				addDirectoryRow(t, db, alice.ID, `{"service_accounts":["svc-a"]}`)
				addHolder(t, db, "bob", `["svc-a"]`, `[]`)
			},
			account:     "svc-a",
			ldapEnabled: true,
			want:        []string{"alice", "bob", "carol"},
		},
		{
			name:    "should return nothing for an empty account name",
			seed:    func(t *testing.T, db *gorm.DB) { addHolder(t, db, "alice", `["svc-a"]`, `[]`) },
			account: "",
			want:    []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := holdersDB(t)
			tt.seed(t, db)

			got, err := usersHoldingAccount(t.Context(), db, tt.account, tt.ldapEnabled, tt.allowUserAccount)
			if err != nil {
				t.Fatalf("usersHoldingAccount() error = %v", err)
			}
			names := holdingUsernames(got)
			if len(names) != len(tt.want) {
				t.Fatalf("holders = %v, want %v", names, tt.want)
			}
			for i, want := range tt.want {
				if names[i] != want {
					t.Fatalf("holders = %v, want %v", names, tt.want)
				}
			}
		})
	}
}

// The two ways of holding an account are labelled differently on the page,
// and a person can have it both ways at once.
func TestUsersHoldingAccount_ShouldLabelHowTheAccountIsHeld(t *testing.T) {
	t.Parallel()

	db := holdersDB(t)
	addHolder(t, db, "alice", `["svc-a"]`, `[]`)
	addHolder(t, db, "bob", `[]`, `["svc-a"]`)
	addHolder(t, db, "carol", `["svc-a"]`, `["svc-a"]`)

	got, err := usersHoldingAccount(t.Context(), db, "svc-a", false, true)
	if err != nil {
		t.Fatalf("usersHoldingAccount() error = %v", err)
	}

	labels := map[string]accountHolding{}
	for _, holding := range got {
		labels[holding.User.Username] = holding
	}

	if !labels["alice"].Claimed || labels["alice"].Own {
		t.Errorf("alice = %+v, want claimed and not own", labels["alice"])
	}
	if labels["bob"].Claimed || !labels["bob"].Own {
		t.Errorf("bob = %+v, want own and not claimed", labels["bob"])
	}
	// Claimed wins where both are true: the claim is the stronger
	// statement, and "this is their own account" beside a claimed one would
	// read as the weaker claim being the whole story.
	if !labels["carol"].Claimed || labels["carol"].Own {
		t.Errorf("carol = %+v, want the claim to win the label", labels["carol"])
	}
}
