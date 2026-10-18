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
	AuditCertRequestRefusedNetwork,
	AuditCertRequested,
	AuditCertSignFailed,
	AuditEnrollmentCodeCreated,
	AuditEnrollmentExpired,
	AuditEnrollmentNotificationEmailSet,
	AuditEnrollmentReassigned,
	AuditEnrollmentRedeemed,
	AuditLDAPProbed,
	AuditLDAPSyncTriggered,
	AuditNSSGIDCollision,
	AuditNSSIDChangeAccepted,
	AuditNSSIDChangePending,
	AuditNSSKeyCreated,
	AuditNSSKeyExpiredAttempt,
	AuditNSSKeyPreviousCleared,
	AuditNSSKeyRevoked,
	AuditNSSKeyRevokedAttempt,
	AuditNSSKeyRotated,
	AuditNSSKeySourceAlert,
	AuditNSSKeyUpdated,
	AuditNSSLookupForbidden,
	AuditNSSMissRateAlert,
	AuditNSSNameCollision,
	AuditNSSNameMismatch,
	AuditNSSPrivilegedNameLookup,
	AuditNSSShellChanged,
	AuditNSSTombstonePurged,
	AuditNSSUIDCollision,
	AuditUserAutoDisabled,
	AuditUserDisableRefused,
	AuditUserDisabled,
	AuditUserEnabled
} from '#lib/api/generated/auditactions.js';
import * as auditNamespace from '#lib/api/generated/auditnamespaces.js';
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
	// Recorded before anyone signs in, so like cert.expired it reads "The
	// server ...". The address it came from is in source_ip, and the
	// account it named in claimed_username: a claim, not a subject.
	[AuditCertRequestRefusedNetwork]:
		'refused a certificate request from outside the allowed networks',
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
	[AuditAdminAuditViewed]: 'viewed the audit log',
	// The NSS directory's alerts are system events, read as "The server
	// ...", and repeat daily while the condition lasts.
	[AuditNSSUIDCollision]: 'withheld two directory accounts that share a uid',
	[AuditNSSGIDCollision]: 'withheld two directory groups that share a gid',
	[AuditNSSNameCollision]: 'withheld two directory entries that share a name',
	[AuditNSSIDChangePending]: 'held back a changed uid or gid from the directory',
	// The target is the person, so the row reads "The server found that
	// the POSIX name differs from the username of bob".
	[AuditNSSNameMismatch]: 'found that the POSIX name differs from the username of',
	[AuditNSSIDChangeAccepted]: 'accepted a uid or gid change from the directory',
	// System once nss.tombstone_retention has passed, an admin when purged
	// early.
	[AuditNSSTombstonePurged]: 'purged a deleted directory account or group',
	// System events from the NSS lookup endpoints: no session, so each row
	// reads "The server ...". The host's address is in source_ip and the
	// fleet key it presented in key_id.
	[AuditNSSLookupForbidden]: 'refused a directory lookup from outside the allowed networks',
	[AuditNSSKeyRevokedAttempt]: 'refused a directory lookup with a revoked fleet key',
	[AuditNSSKeyExpiredAttempt]: 'refused a directory lookup with an expired fleet key',
	[AuditNSSKeySourceAlert]: 'saw a fleet key used from more addresses than expected',
	[AuditNSSMissRateAlert]: 'saw a fleet key looking up many unknown names',
	[AuditNSSPrivilegedNameLookup]: 'saw a directory lookup for a privileged name',
	[AuditNSSKeyCreated]: 'created a fleet key',
	[AuditNSSKeyUpdated]: 'changed a fleet key',
	[AuditNSSKeyRotated]: 'rotated a fleet key',
	[AuditNSSKeyPreviousCleared]: "ended a fleet key's previous secret early",
	[AuditNSSKeyRevoked]: 'revoked a fleet key',
	[AuditNSSShellChanged]: 'changed their login shell'
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

/**
 * Actions the server writes to the shipped log only (see
 * service.tableSkipped). The feed can never return one, so the action filter
 * does not offer them: choosing one could only ever show an empty list.
 */
const logOnlyAuditActions = [AuditCertIssued];

/** One labelled group of the action filter's options. */
export interface AuditActionGroup {
	label: string;
	actions: string[];
}

/**
 * Words for each namespace of the taxonomy, in the order the action filter
 * lists them: the events a reviewer reaches for most first. A namespace
 * missing here still gets a group, under its raw name, so a new one is never
 * dropped from the filter.
 */
export const auditNamespaceLabels: [string, string][] = [
	[auditNamespace.Cert, 'Certificates'],
	[auditNamespace.Auth, 'Sign-in'],
	[auditNamespace.User, 'Accounts'],
	[auditNamespace.Enrollment, 'Service codes'],
	[auditNamespace.LDAP, 'Directory'],
	[auditNamespace.Admin, 'Admin views'],
	[auditNamespace.NSS, 'NSS directory']
];

/**
 * auditActionGroups builds the action filter's options from the taxonomy:
 * every action the feed can return, grouped by namespace, alphabetical inside
 * a group. Taken from the generated list (generated/auditactions.ts) rather
 * than written out, so a new action is filterable the day it ships.
 */
export function auditActionGroups(taxonomy: string[]): AuditActionGroup[] {
	const filterable = taxonomy.filter(
		(action) => !hiddenAuditActions.includes(action) && !logOnlyAuditActions.includes(action)
	);
	const byNamespace: Record<string, string[]> = {};
	for (const action of filterable) {
		const namespace = action.split('.')[0];
		(byNamespace[namespace] ??= []).push(action);
	}
	const known = auditNamespaceLabels.map(([namespace]) => namespace);
	const order = [
		...known.filter((namespace) => byNamespace[namespace]),
		...Object.keys(byNamespace)
			.filter((namespace) => !known.includes(namespace))
			.sort()
	];
	return order.map((namespace) => ({
		label: auditNamespaceLabels.find(([n]) => n === namespace)?.[1] ?? namespace,
		actions: [...byNamespace[namespace]].sort()
	}));
}

/**
 * isIPAddress reports whether value is one IPv4 or IPv6 address, the only
 * thing the feed's source filter accepts (the server answers anything else
 * with a 400). Not a network, not a host name, not an address with a port.
 *
 * IPv4 by pattern, since the URL parser also accepts shorthand such as
 * "10.1" and would rewrite it. IPv6 through the URL parser, which validates
 * the full grammar, compressed and IPv4-mapped forms included.
 */
export function isIPAddress(value: string): boolean {
	const candidate = value.trim();
	if (candidate === '') return false;
	if (!candidate.includes(':')) {
		const octet = '(25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)';
		return new RegExp(`^${octet}(\\.${octet}){3}$`).test(candidate);
	}
	if (candidate.includes('[') || candidate.includes(']') || candidate.includes('/')) {
		return false;
	}
	try {
		new URL(`http://[${candidate}]/`);
		return true;
	} catch {
		return false;
	}
}

/**
 * auditFilterSearch is the feed's filters as a query string: what the address
 * bar carries, so a filtered view survives a reload and can be shared.
 * Defaults are left out, so the unfiltered feed has a clean address.
 */
export function auditFilterSearch(action: string, sourceIP: string): string {
	const query = new URLSearchParams();
	if (action) query.set('action', action);
	if (sourceIP) query.set('source_ip', sourceIP);
	const search = query.toString();
	return search ? `?${search}` : '';
}
