package service

import (
	"context"
	"fmt"
	"sort"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/notify"
)

// The admin mail about pending uid and gid changes: a stored NSS number the
// directory now gives differently, held until an admin accepts it on the
// directory page. Two kinds, both addressed to every admin (see
// NotificationService.AdminRecipients) and gated by each admin's own
// preference at delivery:
//
//   - nss_id_change_pending lists what is new. A row's change is claimed
//     with a guarded UPDATE of its pending_notified_at, the way the fleet
//     key mail claims a send on nss_keys, so a restart, a second instance
//     or the next sync never mails it again. The sync mails what it made
//     pending at the end of each run; a scheduled sweep mails what on-demand
//     and login reads made pending since the last send.
//   - nss_id_change_reminder lists everything still waiting, once a week,
//     claimed on nss_alerts the way the daily alerts are.
//
// Accepting a change, the directory reverting it, or a different number
// turning pending clears pending_notified_at (nssPendingUpdates and the
// accept paths), so only a change still pending and not yet mailed is ever
// sent as new. All of it is off when mail.enabled is false.

// Pending id change mail tuning. Fixed rather than configurable, as the
// fleet key mail's is.
const (
	// NSSIDChangeMailInterval is how often the sweep mails changes that
	// on-demand and login reads made pending. A sync run mails its own at
	// once.
	NSSIDChangeMailInterval = 5 * time.Minute

	// NSSIDChangeReminderWeekday and NSSIDChangeReminderHour are the fixed
	// cadence of the weekly reminder: Monday at 09:00 in the server's local
	// time zone.
	NSSIDChangeReminderWeekday = time.Monday
	NSSIDChangeReminderHour    = 9

	// nssIDChangeReminderWindow is how long one reminder claim lasts. Under
	// a week, so the next Monday's run is never refused by a claim a few
	// seconds younger than seven days, and long enough that every instance
	// running the same Monday's job sends one reminder between them.
	nssIDChangeReminderWindow = 6 * 24 * time.Hour

	// nssIDChangeListLimit caps the changes one message lists; the
	// directory page has the rest.
	nssIDChangeListLimit = 50

	// nssAlertIDChangeReminder and nssIDChangeReminderSubject are the
	// reminder's claim row in nss_alerts. One row for the whole directory:
	// the reminder is one message about everything waiting.
	nssAlertIDChangeReminder   = "id_change_reminder"
	nssIDChangeReminderSubject = "directory"

	// nssPendingDirectoryPath is where the pending changes are accepted.
	nssPendingDirectoryPath = "/admin/directory#pending-id-changes"
)

// SetNotifications attaches what the pending id change mail needs: the
// publisher, and who the admins are. Optional and wired at startup, as the
// auditor is; until both are attached the mail is off, and nothing is
// claimed, so attaching them later still mails what is waiting. Safe on a
// nil service, which is a disabled directory.
func (s *NSSDirectoryService) SetNotifications(n Notifier, admins AdminRecipientResolver) {
	if s != nil && n != nil && admins != nil {
		s.notifier, s.admins = n, admins
	}
}

// pendingMailOn reports whether the pending id change mail can be sent:
// mail is on and a publisher and the admin resolver are attached.
func (s *NSSDirectoryService) pendingMailOn() bool {
	return s != nil && s.config.Mail.Enabled && s.notifier != nil && s.admins != nil
}

// SweepPendingIDChangeMail mails the admins the pending id changes nobody
// has been told about yet: the ones on-demand and login reads made, and any
// a sync run left unmailed. A scheduled job. While a sync runs on this
// instance it does nothing, because the run mails its whole batch when it
// ends and a sweep in between would split it.
func (s *NSSDirectoryService) SweepPendingIDChangeMail(ctx context.Context) error {
	if !s.pendingMailOn() || s.SyncRunning() {
		return nil
	}
	return s.mailPendingIDChanges(ctx)
}

// mailNewSyncChanges mails what a finished sync run made pending. A failure
// is logged, not returned: the run itself succeeded, and the sweep tries
// the same rows again.
func (s *NSSDirectoryService) mailNewSyncChanges(ctx context.Context) {
	if !s.pendingMailOn() {
		return
	}
	if err := s.mailPendingIDChanges(ctx); err != nil {
		s.log.WarnContext(ctx, "failed to mail the admins the new NSS id changes; the next sweep tries again", "error", err)
	}
}

// mailPendingIDChanges claims every pending change not yet mailed and sends
// one nss_id_change_pending per admin listing them. Nothing is claimed while
// there is no admin to tell, so the changes wait for the first one rather
// than being marked as told. Claim-then-publish, as the fleet key mail does:
// a crash between the two loses a message rather than doubling one, and the
// weekly reminder still lists the change.
func (s *NSSDirectoryService) mailPendingIDChanges(ctx context.Context) error {
	accounts, groups, err := s.pendingRows(ctx, "pending_notified_at IS NULL")
	if err != nil {
		return err
	}
	if len(accounts) == 0 && len(groups) == 0 {
		return nil
	}
	admins, err := s.admins.AdminRecipients(ctx)
	if err != nil {
		return err
	}
	if len(admins) == 0 {
		s.log.InfoContext(ctx, "NSS id changes are waiting, but no admin with an email address has logged in to be told",
			"accounts", len(accounts), "groups", len(groups))
		return nil
	}

	now := s.now().UTC()
	claimedAccounts, claimedGroups, err := s.claimPendingRows(ctx, accounts, groups, now)
	if err != nil {
		return err
	}
	payload := s.idChangesPayload(claimedAccounts, claimedGroups, now)
	if payload.Total == 0 {
		return nil
	}
	for i := range admins {
		s.notifier.Notify(ctx, notify.KindNSSIDChangePending, admins[i].ID, payload)
	}
	s.log.InfoContext(ctx, "queued the mail telling the admins about new NSS id changes",
		"changes", payload.Total, "admins", len(admins))
	return nil
}

// SweepPendingIDChangeReminder sends nss_id_change_reminder to every admin
// when any change they were told about is still pending: all of them,
// with how long each has waited. A scheduled job on the fixed weekly
// cadence; claimed on nss_alerts for nssIDChangeReminderWindow, so every
// instance can run it and one reminder goes out. A change not mailed yet
// is left to SweepPendingIDChangeMail, which sends it as new.
func (s *NSSDirectoryService) SweepPendingIDChangeReminder(ctx context.Context) error {
	if !s.pendingMailOn() {
		return nil
	}
	accounts, groups, err := s.pendingRows(ctx, "pending_notified_at IS NOT NULL")
	if err != nil {
		return err
	}
	if len(accounts) == 0 && len(groups) == 0 {
		return nil
	}
	admins, err := s.admins.AdminRecipients(ctx)
	if err != nil {
		return err
	}
	if len(admins) == 0 {
		return nil
	}

	now := s.now().UTC()
	claimed, err := claimNSSAlert(ctx, s.db, nssAlertIDChangeReminder, nssIDChangeReminderSubject, now, nssIDChangeReminderWindow)
	if err != nil {
		return err
	}
	if !claimed {
		return nil
	}
	payload := s.idChangesPayload(accounts, groups, now)
	for i := range admins {
		s.notifier.Notify(ctx, notify.KindNSSIDChangeReminder, admins[i].ID, payload)
	}
	s.log.InfoContext(ctx, "queued the weekly reminder of waiting NSS id changes",
		"changes", payload.Total, "admins", len(admins))
	return nil
}

// pendingRows reads the accounts and groups with a pending change and the
// mail state where names. pending_since is set exactly while a change is
// pending, and idx_posix_users_pending and idx_posix_groups_pending hold
// only those rows.
func (s *NSSDirectoryService) pendingRows(ctx context.Context, where string) ([]model.PosixUser, []model.PosixGroup, error) {
	db := s.db.WithContext(ctx)
	var accounts []model.PosixUser
	if err := db.Where("pending_since IS NOT NULL").Where(where).
		Order("pending_since, name").Find(&accounts).Error; err != nil {
		return nil, nil, fmt.Errorf("read the pending NSS account id changes: %w", err)
	}
	var groups []model.PosixGroup
	if err := db.Where("pending_since IS NOT NULL").Where(where).
		Order("pending_since, name").Find(&groups).Error; err != nil {
		return nil, nil, fmt.Errorf("read the pending NSS group id changes: %w", err)
	}
	return accounts, groups, nil
}

// claimPendingRows marks each row's change as mailed at now, one guarded
// UPDATE per row, and returns the rows this caller won. The guard repeats
// the pending numbers read, so a row whose change moved on since the read
// is left for the next pass to mail as it is now, and another instance
// that claimed the row first leaves it out here.
func (s *NSSDirectoryService) claimPendingRows(ctx context.Context, accounts []model.PosixUser, groups []model.PosixGroup, now time.Time) ([]model.PosixUser, []model.PosixGroup, error) {
	db := s.db.WithContext(ctx)
	var wonAccounts []model.PosixUser
	for i := range accounts {
		row := &accounts[i]
		q := db.Model(&model.PosixUser{}).Where("id = ? AND pending_notified_at IS NULL", row.ID)
		q = whereNullableNumber(whereNullableNumber(q, "pending_uid", row.PendingUID), "pending_gid", row.PendingGID)
		result := q.Update("pending_notified_at", now)
		if result.Error != nil {
			return nil, nil, fmt.Errorf("claim the id change mail for the NSS account %s: %w", row.Name, result.Error)
		}
		if result.RowsAffected == 1 {
			wonAccounts = append(wonAccounts, *row)
		}
	}
	var wonGroups []model.PosixGroup
	for i := range groups {
		row := &groups[i]
		q := db.Model(&model.PosixGroup{}).Where("id = ? AND pending_notified_at IS NULL", row.ID)
		result := whereNullableNumber(q, "pending_gid", row.PendingGID).Update("pending_notified_at", now)
		if result.Error != nil {
			return nil, nil, fmt.Errorf("claim the id change mail for the NSS group %s: %w", row.Name, result.Error)
		}
		if result.RowsAffected == 1 {
			wonGroups = append(wonGroups, *row)
		}
	}
	return wonAccounts, wonGroups, nil
}

// whereNullableNumber adds column = v to q, or column IS NULL for a nil v.
func whereNullableNumber(q *gorm.DB, column string, v *uint32) *gorm.DB {
	if v == nil {
		return q.Where(column + " IS NULL")
	}
	return q.Where(column+" = ?", *v)
}

// idChangesPayload lists the pending changes of accounts and groups, one
// per pending number, oldest first, capped at nssIDChangeListLimit with the
// rest counted in More. Waiting is measured to now.
func (s *NSSDirectoryService) idChangesPayload(accounts []model.PosixUser, groups []model.PosixGroup, now time.Time) *notify.NSSIDChanges {
	var changes []notify.NSSIDChange
	add := func(entry, attribute, name, dn, directoryID string, stored, pending uint32, since *time.Time) {
		c := notify.NSSIDChange{
			Entry: entry, Attribute: attribute, Name: name, DN: dn, DirectoryID: directoryID,
			Stored: stored, Pending: pending,
		}
		if since != nil {
			c.PendingSince = *since
			c.Waiting = now.Sub(*since).Round(time.Second)
		}
		changes = append(changes, c)
	}
	for i := range accounts {
		a := &accounts[i]
		if a.PendingUID != nil {
			add("account", "uid", a.Name, a.DN, a.DirectoryID, a.UID, *a.PendingUID, a.PendingSince)
		}
		if a.PendingGID != nil && a.GID != nil {
			add("account", "gid", a.Name, a.DN, a.DirectoryID, *a.GID, *a.PendingGID, a.PendingSince)
		}
	}
	for i := range groups {
		g := &groups[i]
		if g.PendingGID != nil {
			add("group", "gid", g.Name, g.DN, g.DirectoryID, g.GID, *g.PendingGID, g.PendingSince)
		}
	}
	sort.SliceStable(changes, func(i, j int) bool {
		if !changes[i].PendingSince.Equal(changes[j].PendingSince) {
			return changes[i].PendingSince.Before(changes[j].PendingSince)
		}
		return changes[i].Name < changes[j].Name
	})

	payload := &notify.NSSIDChanges{Total: len(changes), Changes: []notify.NSSIDChange{}}
	if len(changes) > nssIDChangeListLimit {
		payload.More = len(changes) - nssIDChangeListLimit
		changes = changes[:nssIDChangeListLimit]
	}
	payload.Changes = append(payload.Changes, changes...)
	if origin := s.config.HTTP.PublicOrigin(); origin != "" {
		payload.ServerURL = origin
		payload.DirectoryURL = origin + nssPendingDirectoryPath
	}
	return payload
}
