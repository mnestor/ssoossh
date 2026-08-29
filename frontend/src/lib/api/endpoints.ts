import { isInternalPath } from '$lib/paths';

import { ApiError, request } from './client';
import type {
	AdminEnrollment,
	AdminEnrollmentsResponse,
	AdminUserDetail,
	AdminUsersListResponse,
	ApproveResult,
	CertificateListAdminResponse,
	CertificateListResponse,
	CertificateResponse,
	CurrentUser,
	DenyResult,
	DisableUserConsequences,
	AuditEventsResponse,
	DisableUserRequestBody,
	EffectiveConfigResponse,
	EnrollmentRetrievalResponse,
	EnrollmentRetrievalsResponse,
	NotificationPreferences,
	ReEnableUserRequestBody,
	RequestDetail,
	ServiceEnrollmentsResponse
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
 */
export function updateNotificationPreferences(
	kinds: Record<string, boolean>
): Promise<NotificationPreferences> {
	return request<NotificationPreferences>('/users/me/notifications', {
		method: 'PUT',
		body: { kinds }
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
	}
): Promise<ApproveResult> {
	// The options object is camelCase for callers; the wire field names are
	// webtypes.ApproveRequestBody's snake_case json tags. Mapping here rather
	// than posting `options` verbatim, which would send `serviceAccount` and
	// silently fail to bind server-side.
	const hasSelection = !!options && (!!options.serviceAccount || !!options.principals?.length);
	const body = hasSelection
		? {
				...(options.serviceAccount ? { service_account: options.serviceAccount } : {}),
				...(options.principals?.length ? { principals: options.principals } : {})
			}
		: undefined;
	return request<ApproveResult>(`/certs/requests/${encodeURIComponent(id)}/approve`, {
		method: 'POST',
		body
	});
}

/** POST /api/certs/requests/:id/deny. */
export function denyRequest(id: string): Promise<DenyResult> {
	return request<DenyResult>(`/certs/requests/${encodeURIComponent(id)}/deny`, { method: 'POST' });
}

/** GET /api/certs — the caller's own issued-certificate history. Supports cursor-based pagination. */
export function listCertificates(
	signal?: AbortSignal,
	after?: string | null,
	limit?: number
): Promise<CertificateListResponse> {
	const params = new URLSearchParams();
	if (after) {
		params.append('after', after);
	}
	if (limit) {
		params.append('limit', limit.toString());
	}
	const url = params.toString() ? `/certs?${params.toString()}` : '/certs';
	return request<CertificateListResponse>(url, { signal });
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
 * Supports search, filtering by type and status, and offset pagination.
 */
export function listAdminCertificates(
	signal?: AbortSignal,
	options?: {
		offset?: number;
		limit?: number;
		q?: string;
		type?: string;
		status?: string;
	}
): Promise<CertificateListAdminResponse> {
	const params = new URLSearchParams();
	if (options?.offset !== undefined) {
		params.append('offset', options.offset.toString());
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
 * GET /api/admin/config — the effective server configuration (auditor-only).
 */
export function getAdminConfig(signal?: AbortSignal): Promise<EffectiveConfigResponse> {
	return request<EffectiveConfigResponse>('/admin/config', { signal });
}

/**
 * GET /api/admin/users — paginated list of all users (auditor-only).
 */
export function getAdminUsers(options?: {
	q?: string;
	limit?: number;
	offset?: number;
}): Promise<AdminUsersListResponse> {
	const params = new URLSearchParams();
	if (options?.q) {
		params.append('q', options.q);
	}
	if (options?.limit) {
		params.append('limit', options.limit.toString());
	}
	if (options?.offset) {
		params.append('offset', options.offset.toString());
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
 * PATCH /api/admin/users/:id/enable — re-enable a user (admin-only).
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
 * and target (auditor-scoped).
 */
export function getUserAudit(
	id: string,
	params: { limit?: number; offset?: number } = {}
): Promise<AuditEventsResponse> {
	const query = new URLSearchParams();
	if (params.limit !== undefined) query.set('limit', String(params.limit));
	if (params.offset !== undefined) query.set('offset', String(params.offset));
	const suffix = query.toString() ? `?${query}` : '';
	return request<AuditEventsResponse>(`/admin/users/${encodeURIComponent(id)}/audit${suffix}`);
}

/**
 * GET /api/admin/audit — the recent-activity feed (auditor-scoped).
 *
 * Deliberately unsearchable: the database copy is a bounded cache of recent
 * events, and searching happens in the shipped audit log.
 */
export function getAuditFeed(
	params: { limit?: number; offset?: number } = {}
): Promise<AuditEventsResponse> {
	const query = new URLSearchParams();
	if (params.limit !== undefined) query.set('limit', String(params.limit));
	if (params.offset !== undefined) query.set('offset', String(params.offset));
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
 * GET /api/admin/enrollments — paged, searchable list of all service
 * enrollments across users, visible to auditors.
 */
export function listAdminEnrollments(
	signal?: AbortSignal,
	limit?: number,
	offset?: number,
	query?: string
): Promise<AdminEnrollmentsResponse> {
	const params = new URLSearchParams();
	if (limit !== undefined) {
		params.append('limit', limit.toString());
	}
	if (offset !== undefined) {
		params.append('offset', offset.toString());
	}
	if (query) {
		params.append('q', query);
	}
	const url = params.toString() ? `/admin/enrollments?${params.toString()}` : '/admin/enrollments';
	return request<AdminEnrollmentsResponse>(url, { signal });
}

/**
 * GET /api/admin/enrollments/:id — full enrollment details including
 * retrieval log and reassignment history, visible to auditors and the owner.
 */
export function getAdminEnrollmentDetail(
	id: string,
	signal?: AbortSignal
): Promise<{
	enrollment: AdminEnrollment;
	retrievals: EnrollmentRetrievalResponse[];
	retrieval_total: number;
}> {
	return request<{
		enrollment: AdminEnrollment;
		retrievals: EnrollmentRetrievalResponse[];
		retrieval_total: number;
	}>(`/admin/enrollments/${encodeURIComponent(id)}`, { signal });
}

/**
 * PATCH /api/admin/enrollments/:id/reassign — transfer ownership of an
 * enrollment to another user. The new owner must have the required service
 * account.
 */
export function reassignEnrollment(id: string, toUserId: string): Promise<{ reassigned: boolean }> {
	return request<{ reassigned: boolean }>(`/admin/enrollments/${encodeURIComponent(id)}/reassign`, {
		method: 'PATCH',
		body: { to_user_id: toUserId }
	});
}

/**
 * PATCH /api/admin/enrollments/:id/expire — immediately expire an
 * enrollment, preventing future service certificate retrievals.
 */
export function expireEnrollment(id: string): Promise<{ expired: boolean }> {
	return request<{ expired: boolean }>(`/admin/enrollments/${encodeURIComponent(id)}/expire`, {
		method: 'PATCH'
	});
}
