/**
 * Audit feed display rules, kept out of the components so the timeline and
 * the page that counts its rows cannot disagree about what is shown.
 */

import {
	AuditAdminAuditViewed,
	AuditAdminConfigViewed,
	AuditAdminDiagnosticsRun,
	AuditAdminEnrollmentViewed,
	AuditAdminUserViewed,
	AuditAuthLogin,
	AuditAuthLoginDenied,
	AuditCertApproved,
	AuditCertClaimed,
	AuditCertCodeResolved,
	AuditCertDenied,
	AuditCertExpired,
	AuditCertIssued,
	AuditCertRequested,
	AuditCertSignFailed,
	AuditEnrollmentCodeCreated,
	AuditEnrollmentExpired,
	AuditEnrollmentNotificationEmailSet,
	AuditEnrollmentReassigned,
	AuditEnrollmentRedeemed,
	AuditLDAPProbed,
	AuditLDAPSyncTriggered,
	AuditUserAutoDisabled,
	AuditUserDisableRefused,
	AuditUserDisabled,
	AuditUserEnabled
} from '#lib/api/generated/auditactions.js';
import type { AuditEvent } from '#lib/api/types.js';

/**
 * Actions the web UI does not render.
 *
 * These are the privileged-view events: opening a user record, or reading
 * the log itself. The server no longer writes either to the table the UI
 * reads (see service.tableSkipped) — they go to the shipped archive, where
 * "who looked at this account" is still a real question — but rows recorded
 * before that change stay until the retention sweep clears them, and one per
 * screen an auditor opens is exactly what buries the decisions this view
 * exists to show.
 */
export const hiddenAuditActions = [AuditAdminAuditViewed, AuditAdminUserViewed];

/**
 * visibleAuditEvents drops the hidden actions, and tolerates a response that
 * is not an array at all: an unexpected shape must render "nothing to show"
 * rather than tearing down the page the timeline is embedded in.
 */
export function visibleAuditEvents(events: AuditEvent[]): AuditEvent[] {
	if (!Array.isArray(events)) {
		return [];
	}
	return events.filter((event) => !hiddenAuditActions.includes(event.action));
}

/**
 * dedupeAuditEvents keeps the first occurrence of each event id, preserving
 * order.
 *
 * Offset paging over a live table cannot promise the window holds still: any
 * event written between two requests shifts every row down one, and the last
 * row of the first page arrives again as the first row of the second. The
 * timeline keys its {#each} by id, so a repeat is not a cosmetic duplicate —
 * it throws each_key_duplicate and takes the page down.
 *
 * A plain object rather than a Set: svelte/prefer-svelte-reactivity rejects a
 * built-in Set in a component, and the collection is scratch — rebuilt on
 * every call and discarded once the array is returned.
 */
export function dedupeAuditEvents(events: AuditEvent[]): AuditEvent[] {
	const seen: Record<string, true> = {};
	const out: AuditEvent[] = [];
	for (const event of events) {
		if (seen[event.id]) {
			continue;
		}
		seen[event.id] = true;
		out.push(event);
	}
	return out;
}

/**
 * Human phrasing per action: the verb phrase that sits between the actor and
 * the target, so a row reads "alice disabled an account bob" as a sentence.
 *
 * Every action in the server's taxonomy has an entry. audit.test.ts walks the
 * generated list (generated/auditactions.ts, from
 * server/service/audit_actions.go) and fails, naming the action, for any that
 * is missing here, so a new action cannot ship rendering as its raw name.
 * Keys are the generated constants rather than string literals, so an action
 * renamed or dropped on the server fails the type check here instead of
 * leaving a dead entry behind.
 */
export const auditPhrasing: Record<string, string> = {
	[AuditAuthLogin]: 'signed in',
	[AuditAuthLoginDenied]: 'was refused sign-in',
	[AuditCertRequested]: 'requested a certificate',
	[AuditCertApproved]: 'approved a certificate',
	[AuditCertDenied]: 'denied a certificate request',
	[AuditCertCodeResolved]: 'opened a console login by its code',
	// Recorded before anyone signs in, so it has no actor and reads
	// "Someone opened ...": the user agent in the detail is what tells a
	// person from a mail scanner.
	[AuditCertClaimed]: 'opened a certificate approval link',
	// System events: the row reads "The server ...".
	[AuditCertExpired]: 'expired an unanswered certificate request',
	[AuditCertSignFailed]: 'could not sign an approved certificate',
	// Shipped log only, never in the table the UI reads (see
	// service.tableSkipped), but phrased so the taxonomy has no gaps and an
	// older row, if one ever appears, still reads as a sentence.
	[AuditCertIssued]: 'issued a certificate to',
	[AuditEnrollmentCodeCreated]: 'created a service enrollment',
	[AuditEnrollmentRedeemed]: 'redeemed a service enrollment',
	[AuditEnrollmentExpired]: 'expired a service enrollment',
	[AuditEnrollmentNotificationEmailSet]: "changed a service enrollment's notification address",
	[AuditEnrollmentReassigned]: 'reassigned a service enrollment',
	[AuditUserDisabled]: 'disabled an account',
	[AuditUserEnabled]: 're-enabled an account',
	[AuditUserAutoDisabled]: 'was disabled automatically',
	// Actor and target are the same account, so onWhom adds nothing and
	// the phrase has to carry "self" on its own. Worded to match
	// auth.login_denied, the other refusal on a timeline.
	[AuditUserDisableRefused]: 'was refused a self-disable',
	[AuditLDAPSyncTriggered]: 'ran the directory sync',
	[AuditLDAPProbed]: 'probed the directory',
	// Worded like ldap.sync_triggered, its neighbour on the admin pages.
	[AuditAdminDiagnosticsRun]: 'ran the deployment diagnostics',
	[AuditAdminEnrollmentViewed]: 'viewed an enrollment',
	// No longer emitted, kept so events recorded before that still read
	// as a sentence rather than as a raw action name.
	[AuditAdminConfigViewed]: 'viewed the effective configuration',
	// Not rendered at all any more (see hiddenAuditActions), but kept
	// phrased: the filter is a display rule and this is what the row
	// would read as if it were ever lifted.
	[AuditAdminUserViewed]: 'viewed a user record',
	[AuditAdminAuditViewed]: 'viewed the audit log'
};

/**
 * describeAuditAction returns the phrase for an action. An unknown action
 * falls back to its raw namespaced name rather than being hidden: the
 * taxonomy grows without a wire change, so a client that only rendered known
 * actions would silently drop new ones.
 */
export function describeAuditAction(action: string): string {
	return Object.hasOwn(auditPhrasing, action) ? auditPhrasing[action] : action;
}
