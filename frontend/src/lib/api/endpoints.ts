import { isInternalPath } from '$lib/paths';

import { ApiError, request } from './client';
import type {
	ApproveResult,
	CertificateListResponse,
	CertificateRecord,
	CurrentUser,
	DenyResult,
	RequestDetail
} from './types';

/** GET /api/users/me. */
export function getCurrentUser(signal?: AbortSignal): Promise<CurrentUser> {
	return request<CurrentUser>('/users/me', { signal });
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

/** POST /api/certs/requests/:id/approve. */
export function approveRequest(id: string): Promise<ApproveResult> {
	return request<ApproveResult>(`/certs/requests/${encodeURIComponent(id)}/approve`, {
		method: 'POST'
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
