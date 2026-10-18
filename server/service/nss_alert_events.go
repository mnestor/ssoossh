package service

import (
	"context"
	"strconv"

	"github.com/mnestor/ssoossh/server/model"
)

// Alert kinds, as nss_alerts records them. Each is raised at most once per
// nssAlertWindow per subject across every instance (claimNSSAlert).
const (
	nssAlertUIDCollision    = "uid_collision"
	nssAlertGIDCollision    = "gid_collision"
	nssAlertNameCollision   = "name_collision"
	nssAlertIDChangePending = "id_change_pending"
	nssAlertNameMismatch    = "name_mismatch"
)

// raise writes event as an alert of kind about subject, unless one was
// raised inside the window by this or another instance. An alert is a log
// line at warning and an audit event. A failed claim is logged and the
// alert skipped rather than risk raising it on every instance: the
// condition is read again at the next sync, which raises it then.
func (s *NSSDirectoryService) raise(ctx context.Context, kind, subject string, event AuditEvent) {
	claimed, err := claimNSSAlert(ctx, s.db, kind, subject, s.now(), nssAlertWindow)
	if err != nil {
		s.log.WarnContext(ctx, "failed to claim an NSS alert; it is raised again at the next read",
			"kind", kind, "subject", subject, "error", err)
		return
	}
	if !claimed {
		return
	}
	s.log.WarnContext(ctx, "nss directory alert", "action", string(event.Action), "subject", subject, "detail", event.Detail)
	if s.auditor != nil {
		event.System = true
		event.OccurredAt = s.now()
		s.auditor.Record(ctx, event)
	}
}

// raiseCollisionAlerts raises the daily alert for each collision written,
// one per contested value: nss.uid_collision, nss.gid_collision or
// nss.name_collision.
func (s *NSSDirectoryService) raiseCollisionAlerts(ctx context.Context, t nssTable, pairs []model.NSSCollision) {
	for _, p := range pairs {
		kind, action, subject := nssAlertNameCollision, AuditNSSNameCollision, t.label+":"+p.Value
		switch p.Kind {
		case model.NSSCollisionKindUID:
			kind, action, subject = nssAlertUIDCollision, AuditNSSUIDCollision, p.Value
		case model.NSSCollisionKindGID:
			kind, action, subject = nssAlertGIDCollision, AuditNSSGIDCollision, p.Value
		}

		detail := map[string]any{
			"table":        t.table,
			"kind":         string(p.Kind),
			"value":        p.Value,
			"other_dn":     p.OtherDN,
			"other_name":   p.OtherName,
			"other_number": p.OtherNumber,
			"other_stored": p.OtherID != nil,
		}
		if holders, err := t.refs(s.db.WithContext(ctx), "id = ?", p.HolderID); err == nil && len(holders) > 0 {
			detail["holder_dn"] = holders[0].DN
			detail["holder_name"] = holders[0].Name
			detail["holder_number"] = holders[0].Number
		}
		s.raise(ctx, kind, subject, AuditEvent{Action: action, Detail: detail})
	}
}

// pendingDetail describes a pending change for the alert: the stored
// number and the one waiting, for each that is waiting.
func pendingDetail(number uint32, pending *uint32, gid uint32, pendingGID *uint32) map[string]any {
	detail := map[string]any{}
	if pending != nil {
		detail["stored"] = number
		detail["pending"] = *pending
	}
	if pendingGID != nil {
		detail["stored_gid"] = gid
		detail["pending_gid"] = *pendingGID
	}
	return detail
}

// raisePendingAlert raises nss.id_change_pending for row id at most daily:
// the directory gives a stored uid or gid a different value, the stored one
// is still served, and an admin has to accept the change.
func (s *NSSDirectoryService) raisePendingAlert(ctx context.Context, t nssTable, id, name, dn string, detail map[string]any) {
	detail["table"] = t.table
	detail["name"] = name
	detail["dn"] = dn
	s.raise(ctx, nssAlertIDChangePending, t.label+":"+id, AuditEvent{Action: AuditNSSIDChangePending, Detail: detail})
}

// raiseNameMismatch raises nss.name_mismatch for the person userID at most
// daily: their username is not the name of the account linked to them, so
// a certificate for their own name can never resolve on a host.
func (s *NSSDirectoryService) raiseNameMismatch(ctx context.Context, target *AuditSubject, row *model.PosixUser) {
	s.raise(ctx, nssAlertNameMismatch, target.UserID, AuditEvent{
		Action: AuditNSSNameMismatch,
		Target: target,
		Detail: map[string]any{
			"username":   target.Username,
			"posix_name": row.Name,
			"uid":        strconv.FormatUint(uint64(row.UID), 10),
			"dn":         row.DN,
		},
	})
}
