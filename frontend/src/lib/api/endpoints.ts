import { isInternalPath } from '#lib/paths.js';

import { ApiError, request } from './client';
import type {
	AccountHoldersResponse,
	AdminEnrollment,
	AdminEnrollmentsResponse,
	AdminUserDetail,
	AdminUsersListResponse,
	ApproveResult,
	CreateNSSKeyBody,
	CertificateListAdminResponse,
	CertificateListResponse,
	DeniedRequestListResponse,
	CertificateResponse,
	CurrentUser,
	DenyResult,
	DetailLevel,
	DisplayPreferences,
	DiagnosticsResponse,
	DisableUserConsequences,
	AuditEventsResponse,
	DisableUserRequestBody,
	EffectiveConfigResponse,
	EnrollmentRetrievalResponse,
	EnrollmentRetrievalsResponse,
	IdentityEchoStartResponse,
	LDAPProbeRequestBody,
	LDAPProbeResponse,
	LDAPStatusResponse,
	LDAPSyncRequestBody,
	LDAPSyncRunResponse,
	NotificationPreferences,
	NSSAcceptedGroupIDChangeResponse,
	NSSAcceptedUserIDChangeResponse,
	NSSCollisionsResponse,
	NSSKey,
	NSSKeyListResponse,
	NSSKeySecret,
	NSSKeySettings,
	NSSKeyUsageResponse,
	NSSPendingIDChangesResponse,
	NSSPurgedTombstoneResponse,
	NSSRotationReport,
	NSSShellsResponse,
	PosixAccount,
	ReEnableUserRequestBody,
	RequestDetail,
	ResolveCodeResult,
	ServiceEnrollmentsResponse,
	SetNotificationEmailRequestBody,
	UpdateNSSKeyBody
} from './types';

/** GET /api/users/me. */
export function getCurrentUser(signal?: AbortSignal): Promise<CurrentUser> {
	return request<CurrentUser>('/users/me', { signal });
}

/** GET /api/users/me/notifications — the caller's own notification preferences. */
export function getNotificationPreferences(signal?: AbortSignal): Promise<NotificationPreferences> {
	return request<NotificationPreferences>('/users/me/notifications', { signal });
}

/**
 * PUT /api/users/me/notifications.
 *
 * Sends only the kinds being changed. The server leaves every other kind
 * alone, so a tab loaded before an upgrade cannot reset preferences for
 * kinds it has never heard of, and answers with the preferences as stored.
 * detailLevel, when given, changes the email detail level in the same save.
 */
export function updateNotificationPreferences(
	kinds: Record<string, boolean>,
	detailLevel?: DetailLevel
): Promise<NotificationPreferences> {
	return request<NotificationPreferences>('/users/me/notifications', {
		method: 'PUT',
		body: detailLevel ? { kinds, detail_level: detailLevel } : { kinds }
	});
}

/** PUT /api/users/me/display — the caller's web UI detail level, answered as stored. */
export function updateDisplayPreferences(detailLevel: DetailLevel): Promise<DisplayPreferences> {
	return request<DisplayPreferences>('/users/me/display', {
		method: 'PUT',
		body: { detail_level: detailLevel }
	});
}

/**
 * GET /api/certs/requests/:id.
 *
 * This is also the call that binds the request to the caller server-side —
 * the first authenticated view claims it (see
 * service.CertRequestService.Detail). A second person loading the same page
 * gets 403 here rather than after clicking approve.
 */
export function getRequestDetail(id: string, signal?: AbortSignal): Promise<RequestDetail> {
	return request<RequestDetail>(`/certs/requests/${encodeURIComponent(id)}`, { signal });
}

/**
 * POST /api/certs/requests/:id/approve.
 * For service-type requests, serviceAccount names which of the approver's
 * service accounts the certificate principal should be. For user-type requests,
 * principals is the list of principals to include on the certificate.
 */
export function approveRequest(
	id: string,
	options?: {
		serviceAccount?: string;
		principals?: string[];
		notificationEmail?: string;
		/**
		 * The approver's chosen extension set (service-type requests).
		 * undefined means they were offered no choice and the request's own
		 * set stands; an empty array is a deliberate "no extensions" and is
		 * sent as such, because the server reads absent and empty
		 * differently.
		 */
		extensions?: string[];
	}
): Promise<ApproveResult> {
	// The options object is camelCase for callers; the wire field names are
	// webtypes.ApproveRequestBody's snake_case json tags. Mapping here rather
	// than posting `options` verbatim, which would send `serviceAccount` and
	// silently fail to bind server-side.
	//
	// extensions is tested against undefined rather than for truthiness:
	// an empty array means the approver cleared the set on purpose, which
	// is a legitimate outcome and a different instruction from saying
	// nothing at all. Dropping it here would silently fall back to what the
	// enrollment requested.
	const hasSelection =
		!!options &&
		(!!options.serviceAccount ||
			!!options.principals?.length ||
			!!options.notificationEmail ||
			options.extensions !== undefined);
	const body = hasSelection
		? {
				...(options.serviceAccount ? { service_account: options.serviceAccount } : {}),
				...(options.principals?.length ? { principals: options.principals } : {}),
				...(options.notificationEmail ? { notification_email: options.notificationEmail } : {}),
				...(options.extensions !== undefined ? { extensions: options.extensions } : {})
			}
		: undefined;
	return request<ApproveResult>(`/certs/requests/${encodeURIComponent(id)}/approve`, {
		method: 'POST',
		body
	});
}

/**
 * POST /api/certs/requests/resolve-code.
 *
 * Turns the code a human read off a console screen into the request it
 * names, and claims that request for this session — which is why it is a
 * POST despite reading like a lookup, and why a second session submitting
 * the same code gets 403.
 *
 * The code is sent as typed. Case, the display hyphen, stray spaces and
 * Crockford's decoding aliases (I and L for 1, O for 0) are all normalized
 * server-side, so this never has to be the place that gets it right.
 */
export function resolveConsoleCode(code: string): Promise<ResolveCodeResult> {
	return request<ResolveCodeResult>('/certs/requests/resolve-code', {
		method: 'POST',
		body: { code }
	});
}

/** POST /api/certs/requests/:id/deny. */
export function denyRequest(id: string): Promise<DenyResult> {
	return request<DenyResult>(`/certs/requests/${encodeURIComponent(id)}/deny`, { method: 'POST' });
}

/**
 * HistoryFilter is the narrowing both history endpoints take, in the same
 * names the admin list uses: a free-text term, a certificate type, and
 * live/expired. Empty fields are left off the URL.
 */
export interface HistoryFilter {
	q?: string;
	type?: string;
	status?: string;
}

/** historyParams builds the shared query string for both history endpoints. */
function historyParams(filter?: HistoryFilter, after?: string | null, limit?: number): string {
	const params = new URLSearchParams();
	if (after) {
		params.append('after', after);
	}
	if (limit) {
		params.append('limit', limit.toString());
	}
	if (filter?.q) {
		params.append('q', filter.q);
	}
	if (filter?.type) {
		params.append('type', filter.type);
	}
	if (filter?.status) {
		params.append('status', filter.status);
	}
	return params.toString();
}

/**
 * GET /api/certs — the caller's own issued-certificate history. Cursor
 * pagination, and the same q/type/status filters the admin list takes, so
 * narrowing asks the server rather than sieving whatever has been loaded.
 */
export function listCertificates(
	signal?: AbortSignal,
	after?: string | null,
	limit?: number,
	filter?: HistoryFilter
): Promise<CertificateListResponse> {
	const query = historyParams(filter, after, limit);
	return request<CertificateListResponse>(query ? `/certs?${query}` : '/certs', { signal });
}

/**
 * GET /api/decisions/denied — the requests the caller denied, newest first.
 *
 * Its own endpoint because /api/certs reads the certificates table and a
 * denial never writes a row there. The history page interleaves the two by
 * time.
 */
export function listDeniedRequests(
	signal?: AbortSignal,
	after?: string | null,
	limit?: number,
	filter?: HistoryFilter
): Promise<DeniedRequestListResponse> {
	const query = historyParams(filter, after, limit);
	return request<DeniedRequestListResponse>(
		query ? `/decisions/denied?${query}` : '/decisions/denied',
		{ signal }
	);
}

/** GET /api/certs/:id — a single certificate's full details. */
export function getCertificateDetail(
	id: string,
	signal?: AbortSignal
): Promise<CertificateResponse> {
	return request<CertificateResponse>(`/certs/${encodeURIComponent(id)}`, { signal });
}

/**
 * GET /api/admin/certificates/history — cross-user certificate history for auditor review.
 * Supports search and filtering by type and status, newest first, with
 * cursor paging: `after` is the previous page's `next_cursor`.
 */
export function listAdminCertificates(
	signal?: AbortSignal,
	options?: {
		after?: string;
		limit?: number;
		q?: string;
		type?: string;
		status?: string;
	}
): Promise<CertificateListAdminResponse> {
	const params = new URLSearchParams();
	if (options?.after) {
		params.append('after', options.after);
	}
	if (options?.limit !== undefined) {
		params.append('limit', options.limit.toString());
	}
	if (options?.q) {
		params.append('q', options.q);
	}
	if (options?.type) {
		params.append('type', options.type);
	}
	if (options?.status) {
		params.append('status', options.status);
	}
	const url = params.toString()
		? `/admin/certificates/history?${params.toString()}`
		: '/admin/certificates/history';
	return request<CertificateListAdminResponse>(url, { signal });
}

/**
 * GET /api/certs/requests/:id/retrievals — the retrieval log for a service
 * enrollment, showing every code redemption. Only callable by the enrollment's
 * approver or an auditor; returns 403 otherwise and 404 if no enrollment exists.
 */
export function listRetrievals(
	id: string,
	signal?: AbortSignal
): Promise<EnrollmentRetrievalsResponse> {
	return request<EnrollmentRetrievalsResponse>(
		`/certs/requests/${encodeURIComponent(id)}/retrievals`,
		{
			signal
		}
	);
}

/**
 * GET /api/certs/service/enrollments — the caller's own approved service
 * enrollments, newest first.
 *
 * Never the codes themselves: `service enroll` prints a code once and the
 * server will not hand it back, so this describes what each one grants and
 * how long it lasts. Scoped server-side to the caller, with no parameter to
 * widen it.
 */
export function listServiceEnrollments(signal?: AbortSignal): Promise<ServiceEnrollmentsResponse> {
	return request<ServiceEnrollmentsResponse>('/certs/service/enrollments', { signal });
}

/**
 * GET /api/certs/service/enrollments/:id/holders — everyone known to hold
 * this enrollment's service account, and therefore able to see and manage
 * the code.
 *
 * A code belongs to its service account rather than to whoever approved it,
 * so this is the only place that answers "who else has this". Only users the
 * server has a row for appear -- those who have signed in at least once, or
 * whom the directory sync has seen: the server never enumerates a directory,
 * so the answer is everyone known to hold the account rather than everyone
 * who does.
 *
 * Callable by the account's holders and by auditors; 403 otherwise, 404 for
 * an enrollment that does not exist.
 */
export function listEnrollmentHolders(
	id: string,
	signal?: AbortSignal
): Promise<AccountHoldersResponse> {
	return request<AccountHoldersResponse>(
		`/certs/service/enrollments/${encodeURIComponent(id)}/holders`,
		{ signal }
	);
}

/**
 * PATCH /api/certs/service/enrollments/:id/notification-email.
 *
 * Points every notification about this enrollment at one address instead of
 * fanning out to every holder of its service account. An empty string clears
 * it and restores fan-out, which is why the address is a plain parameter
 * rather than an optional one.
 *
 * Answers with the address as stored — trimmed server-side, so the caller
 * should render what comes back rather than its own input.
 */
export function setEnrollmentNotificationEmail(
	id: string,
	notificationEmail: string
): Promise<SetNotificationEmailRequestBody> {
	return request<SetNotificationEmailRequestBody>(
		`/certs/service/enrollments/${encodeURIComponent(id)}/notification-email`,
		{ method: 'PATCH', body: { notification_email: notificationEmail } }
	);
}

/**
 * GET /api/admin/config — the effective server configuration (admin only).
 */
export function getAdminConfig(signal?: AbortSignal): Promise<EffectiveConfigResponse> {
	return request<EffectiveConfigResponse>('/admin/config', { signal });
}

/**
 * GET /api/admin/users — the user directory (auditor-only), cursor-paged:
 * `after` is the previous page's `next_cursor`.
 */
export function getAdminUsers(options?: {
	q?: string;
	limit?: number;
	after?: string;
	/**
	 * Account state to narrow to: 'active' or 'disabled'. Omitted returns
	 * both. Applied before the count, so the total describes the filtered
	 * set.
	 */
	status?: 'active' | 'disabled';
}): Promise<AdminUsersListResponse> {
	const params = new URLSearchParams();
	if (options?.q) {
		params.append('q', options.q);
	}
	if (options?.limit) {
		params.append('limit', options.limit.toString());
	}
	if (options?.after) {
		params.append('after', options.after);
	}
	if (options?.status) {
		params.append('status', options.status);
	}
	const url = params.toString() ? `/admin/users?${params.toString()}` : '/admin/users';
	return request<AdminUsersListResponse>(url);
}

/**
 * GET /api/admin/users/:id — detailed info for one user (auditor-only).
 */
export function getAdminUser(id: string): Promise<AdminUserDetail> {
	return request<AdminUserDetail>(`/admin/users/${encodeURIComponent(id)}`);
}

/**
 * PATCH /api/admin/users/:id/disable — disable a user (admin or SOC).
 *
 * The body is required, not optional: the server validates a non-empty
 * reason, because the next admin opening this account needs to learn why it
 * was disabled.
 */
export function disableUser(
	id: string,
	body: DisableUserRequestBody
): Promise<DisableUserConsequences> {
	return request<DisableUserConsequences>(`/admin/users/${encodeURIComponent(id)}/disable`, {
		method: 'PATCH',
		body
	});
}

/**
 * PATCH /api/admin/users/:id/enable — re-enable a user (admin or SOC).
 * The reason is required, on the same terms as the disable reason.
 */
export function enableUser(
	id: string,
	body: ReEnableUserRequestBody
): Promise<{ enabled: boolean }> {
	return request<{ enabled: boolean }>(`/admin/users/${encodeURIComponent(id)}/enable`, {
		method: 'PATCH',
		body
	});
}

/**
 * GET /api/admin/users/:id/audit — one user's audit timeline, as both actor
 * and target (auditor-scoped). Cursor-paged: `after` is the previous page's
 * `next_cursor`.
 */
export function getUserAudit(
	id: string,
	params: { limit?: number; after?: string } = {}
): Promise<AuditEventsResponse> {
	const query = new URLSearchParams();
	if (params.limit !== undefined) query.set('limit', String(params.limit));
	if (params.after) query.set('after', params.after);
	const suffix = query.toString() ? `?${query}` : '';
	return request<AuditEventsResponse>(`/admin/users/${encodeURIComponent(id)}/audit${suffix}`);
}

/**
 * GET /api/admin/audit — the recent-activity feed (auditor-scoped).
 *
 * Filters by action and by observed source address, both exact matches, and
 * nothing else: the database copy is a bounded cache of recent events, and
 * any other search happens in the shipped audit log. An empty filter is left
 * off the request. Cursor-paged: `after` is the previous page's
 * `next_cursor`.
 */
export function getAuditFeed(
	params: { limit?: number; after?: string; action?: string; source_ip?: string } = {}
): Promise<AuditEventsResponse> {
	const query = new URLSearchParams();
	if (params.limit !== undefined) query.set('limit', String(params.limit));
	if (params.after) query.set('after', params.after);
	if (params.action) query.set('action', params.action);
	if (params.source_ip) query.set('source_ip', params.source_ip);
	const suffix = query.toString() ? `?${query}` : '';
	return request<AuditEventsResponse>(`/admin/audit${suffix}`);
}

/**
 * POST /auth/logout.
 *
 * Not routed through request(): the auth endpoints sit outside /api because
 * they are browser redirects rather than JSON calls (see
 * bootstrap/router.go), so the /api prefix would 404.
 */
export async function logout(): Promise<void> {
	let response: Response;
	try {
		response = await fetch('/auth/logout', { method: 'POST', credentials: 'same-origin' });
	} catch (cause) {
		throw new ApiError(0, cause instanceof Error ? cause.message : 'network request failed');
	}
	if (!response.ok) {
		throw new ApiError(response.status, `logout failed (HTTP ${response.status})`);
	}
}

/**
 * loginURL builds the OIDC entry point, asking the server to send the
 * browser back to returnTo once login completes.
 *
 * returnTo is a path, never an absolute URL: the server validates it with
 * isSafeReturnURL (server/controller/auth.go) and falls back to "/" for
 * anything that could redirect off-site. Passing a path keeps this side
 * honest about what will actually be accepted.
 */
export function loginURL(returnTo?: string): string {
	if (!isInternalPath(returnTo)) {
		return '/auth/login';
	}
	return `/auth/login?return_to=${encodeURIComponent(returnTo)}`;
}

/**
 * GET /api/admin/enrollments — searchable list of all service enrollments
 * across users, newest first, visible to auditors. Cursor-paged: `after` is
 * the previous page's `next_cursor`.
 */
export function listAdminEnrollments(
	signal?: AbortSignal,
	limit?: number,
	after?: string,
	query?: string
): Promise<AdminEnrollmentsResponse> {
	const params = new URLSearchParams();
	if (limit !== undefined) {
		params.append('limit', limit.toString());
	}
	if (after) {
		params.append('after', after);
	}
	if (query) {
		params.append('q', query);
	}
	const url = params.toString() ? `/admin/enrollments?${params.toString()}` : '/admin/enrollments';
	return request<AdminEnrollmentsResponse>(url, { signal });
}

/**
 * AdminEnrollmentDetail is what GET /api/admin/enrollments/:id answers with.
 *
 * Hand-declared rather than generated, because the handler answers with a
 * gin.H of three parts rather than one wire struct. Named so the panel and
 * the page that preloads it can agree on the shape instead of both spelling
 * it out inline.
 */
export interface AdminEnrollmentDetail {
	enrollment: AdminEnrollment;
	retrievals: EnrollmentRetrievalResponse[];
	retrieval_total: number;
}

/**
 * GET /api/admin/enrollments/:id — full enrollment details including
 * retrieval log and any historical reassignments, visible to auditors and to
 * holders of the enrollment's service account.
 *
 * This resolves any enrollment by id. Anything that needs one enrollment
 * should come here rather than page the list and search it in the browser:
 * the list is capped at paging.MaxLimit (100) whatever a caller asks for,
 * so a client-side scan silently stops resolving anything older than the
 * hundred newest codes.
 */
export function getAdminEnrollmentDetail(
	id: string,
	signal?: AbortSignal
): Promise<AdminEnrollmentDetail> {
	return request<AdminEnrollmentDetail>(`/admin/enrollments/${encodeURIComponent(id)}`, { signal });
}

/**
 * PATCH /api/admin/enrollments/:id/expire — immediately expire an
 * enrollment, preventing future service certificate retrievals.
 *
 * The reason is required: enrollment.expired is one of the actions the
 * server validates a reason for, and this call used to send no body at all,
 * so every press of the admin Expire button came back 400.
 */
export function expireEnrollment(id: string, reason: string): Promise<{ expired: boolean }> {
	return request<{ expired: boolean }>(`/admin/enrollments/${encodeURIComponent(id)}/expire`, {
		method: 'PATCH',
		body: { reason }
	});
}

/**
 * PATCH /api/certs/service/enrollments/:id/expire — retire a code for a
 * service account the caller holds.
 *
 * The same outcome as the admin route above, for the people who live with
 * the code: an enrollment belongs to its service account rather than to
 * whoever approved it, so its holders are the ones who know the job behind
 * it has been decommissioned.
 */
export function expireOwnEnrollment(id: string, reason: string): Promise<{ expired: boolean }> {
	return request<{ expired: boolean }>(
		`/certs/service/enrollments/${encodeURIComponent(id)}/expire`,
		{ method: 'PATCH', body: { reason } }
	);
}

/**
 * GET /api/admin/ldap/status — directory sync status and probe target
 * (auditor-readable).
 *
 * Answers "is the sync even running", which before the run record could only
 * be answered by reading a log on whichever instance happened to run it.
 * Names no credential.
 */
export function getLDAPStatus(signal?: AbortSignal): Promise<LDAPStatusResponse> {
	return request<LDAPStatusResponse>('/admin/ldap/status', { signal });
}

/**
 * POST /api/admin/ldap/sync — run the directory sync now (admin-only).
 *
 * Answers with the run record whatever happened, including a pass that could
 * not reach the directory: that record is what the operator pressed the
 * button to see. A pass already in progress is a 409.
 */
export function runLDAPSync(body: LDAPSyncRequestBody = {}): Promise<LDAPSyncRunResponse> {
	return request<LDAPSyncRunResponse>('/admin/ldap/sync', { method: 'POST', body });
}

/**
 * POST /api/admin/ldap/probe — one read-only directory lookup (admin-only).
 *
 * The connection is not part of the request: the probe always uses the
 * running url, bind credentials and base DN, so it cannot be pointed at
 * another host. It writes nothing.
 */
export function probeLDAP(body: LDAPProbeRequestBody): Promise<LDAPProbeResponse> {
	return request<LDAPProbeResponse>('/admin/ldap/probe', { method: 'POST', body });
}

/**
 * POST /api/admin/diagnostics/run — run the deployment self-checks (admin-only).
 *
 * The edge checks call the server's own public_url back through whatever sits
 * in front of it; the target is always public_url and cannot be supplied by
 * the caller. Nothing is written.
 */
export function runDiagnostics(): Promise<DiagnosticsResponse> {
	return request<DiagnosticsResponse>('/admin/diagnostics/run', { method: 'POST' });
}

/**
 * POST /api/admin/identity/echo/start — begin a claims echo (admin-only).
 *
 * Returns the authorization URL to send the browser to. The result comes
 * back through the OIDC callback as a fragment on /admin/identity/echo, and
 * is never stored anywhere.
 */
export function startIdentityEcho(): Promise<IdentityEchoStartResponse> {
	return request<IdentityEchoStartResponse>('/admin/identity/echo/start', { method: 'POST' });
}

/**
 * PUT /api/users/me/shell: the caller's own login shell, by name from
 * GET /api/nss/shells, or null for the directory's shell. A name outside the
 * list is a 400 whose message starts with shell_not_allowed.
 */
export function updateShellPreference(shell: string | null): Promise<PosixAccount> {
	return request<PosixAccount>('/users/me/shell', {
		method: 'PUT',
		body: { shell_pref: shell }
	});
}

/** GET /api/nss/shells: the shell names a person may choose, in configured order. */
export function listNSSShells(signal?: AbortSignal): Promise<NSSShellsResponse> {
	return request<NSSShellsResponse>('/nss/shells', { signal });
}

/** GET /api/nss/key-settings: a new key's default lifetime and a rotation's grace, in seconds. */
export function getNSSKeySettings(signal?: AbortSignal): Promise<NSSKeySettings> {
	return request<NSSKeySettings>('/nss/key-settings', { signal });
}

/** GET /api/nss/keys: every fleet key the caller may view, each with what they may do to it. */
export function listNSSKeys(signal?: AbortSignal): Promise<NSSKeyListResponse> {
	return request<NSSKeyListResponse>('/nss/keys', { signal });
}

/** GET /api/nss/keys/:id: one fleet key; a key the caller may not view is a 404. */
export function getNSSKey(id: string, signal?: AbortSignal): Promise<NSSKey> {
	return request<NSSKey>(`/nss/keys/${encodeURIComponent(id)}`, { signal });
}

/** POST /api/nss/keys: creates a key and answers its secret, this once. */
export function createNSSKey(body: CreateNSSKeyBody): Promise<NSSKeySecret> {
	return request<NSSKeySecret>('/nss/keys', { method: 'POST', body });
}

/** PATCH /api/nss/keys/:id: changes the fields given; the rest stay. */
export function updateNSSKey(id: string, body: UpdateNSSKeyBody): Promise<NSSKey> {
	return request<NSSKey>(`/nss/keys/${encodeURIComponent(id)}`, { method: 'PATCH', body });
}

/** POST /api/nss/keys/:id/rotate: a new secret, answered this once; the old one stays for the grace. */
export function rotateNSSKey(id: string): Promise<NSSKeySecret> {
	return request<NSSKeySecret>(`/nss/keys/${encodeURIComponent(id)}/rotate`, { method: 'POST' });
}

/** DELETE /api/nss/keys/:id/previous: stops accepting the previous secret before its grace ends. */
export function clearNSSKeyPrevious(id: string): Promise<NSSKey> {
	return request<NSSKey>(`/nss/keys/${encodeURIComponent(id)}/previous`, { method: 'DELETE' });
}

/** POST /api/nss/keys/:id/revoke: ends the key and both its secrets for good. */
export function revokeNSSKey(id: string): Promise<NSSKey> {
	return request<NSSKey>(`/nss/keys/${encodeURIComponent(id)}/revoke`, { method: 'POST' });
}

/** GET /api/nss/keys/:id/usage: one page of lookups per address, generation and day, newest first. */
export function getNSSKeyUsage(
	id: string,
	options: { after?: string; signal?: AbortSignal } = {}
): Promise<NSSKeyUsageResponse> {
	const params = new URLSearchParams();
	if (options.after) params.set('after', options.after);
	const query = params.toString();
	return request<NSSKeyUsageResponse>(
		`/nss/keys/${encodeURIComponent(id)}/usage${query ? `?${query}` : ''}`,
		{ signal: options.signal }
	);
}

/** GET /api/nss/keys/:id/rotation-report: which addresses switched to the current secret. */
export function getNSSRotationReport(id: string, signal?: AbortSignal): Promise<NSSRotationReport> {
	return request<NSSRotationReport>(`/nss/keys/${encodeURIComponent(id)}/rotation-report`, {
		signal
	});
}

/**
 * nssRotationReportCSVURL is the rotation report as a CSV download. A plain
 * URL for a link rather than a request(): the answer is a file, not the
 * JSON envelope request() unwraps.
 */
export function nssRotationReportCSVURL(id: string): string {
	return `/api/nss/keys/${encodeURIComponent(id)}/rotation-report?format=csv`;
}

/** GET /api/admin/nss/pending-id-changes: accounts and groups whose directory number moved. */
export function getNSSPendingIDChanges(signal?: AbortSignal): Promise<NSSPendingIDChangesResponse> {
	return request<NSSPendingIDChangesResponse>('/admin/nss/pending-id-changes', { signal });
}

/** POST /api/admin/nss/users/:uid/accept-id-change: keyed by the stored uid; 409 when the new one is held. */
export function acceptNSSUserIDChange(uid: number): Promise<NSSAcceptedUserIDChangeResponse> {
	return request<NSSAcceptedUserIDChangeResponse>(`/admin/nss/users/${uid}/accept-id-change`, {
		method: 'POST'
	});
}

/** POST /api/admin/nss/groups/:gid/accept-id-change: keyed by the stored gid; 409 when the new one is held. */
export function acceptNSSGroupIDChange(gid: number): Promise<NSSAcceptedGroupIDChangeResponse> {
	return request<NSSAcceptedGroupIDChangeResponse>(`/admin/nss/groups/${gid}/accept-id-change`, {
		method: 'POST'
	});
}

/** GET /api/admin/nss/collisions: every pair of entries contending for one uid, gid or name. */
export function getNSSCollisions(signal?: AbortSignal): Promise<NSSCollisionsResponse> {
	return request<NSSCollisionsResponse>('/admin/nss/collisions', { signal });
}

/** DELETE /api/admin/nss/users/:uid/tombstone: purges a tombstoned account early. */
export function purgeNSSUserTombstone(uid: number): Promise<NSSPurgedTombstoneResponse> {
	return request<NSSPurgedTombstoneResponse>(`/admin/nss/users/${uid}/tombstone`, {
		method: 'DELETE'
	});
}

/** DELETE /api/admin/nss/groups/:gid/tombstone: purges a tombstoned group early. */
export function purgeNSSGroupTombstone(gid: number): Promise<NSSPurgedTombstoneResponse> {
	return request<NSSPurgedTombstoneResponse>(`/admin/nss/groups/${gid}/tombstone`, {
		method: 'DELETE'
	});
}
