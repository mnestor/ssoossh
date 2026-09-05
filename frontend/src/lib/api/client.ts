import type { Envelope } from './types';

/**
 * ApiError carries the HTTP status alongside the server's message, because
 * callers here branch on the status: 401 sends the browser to login, 403 on
 * the approval page means the request belongs to someone else, 404 means it
 * never existed. A bare Error would force every call site to pattern-match
 * on message text.
 */
export class ApiError extends Error {
	readonly status: number;

	constructor(status: number, message: string) {
		super(message);
		this.name = 'ApiError';
		this.status = status;
	}

	/** Not signed in, or the session expired. */
	get isUnauthenticated(): boolean {
		return this.status === 401;
	}

	/** Signed in, but not permitted to act on this resource. */
	get isForbidden(): boolean {
		return this.status === 403;
	}

	get isNotFound(): boolean {
		return this.status === 404;
	}

	/**
	 * The thing existed and its own deadline passed — a console login code
	 * whose request timed out, or a certificate that was delivered and is
	 * gone. Distinct from 404 because it sends the user somewhere different:
	 * back to the machine to start over, rather than back to the keyboard to
	 * retype.
	 */
	get isGone(): boolean {
		return this.status === 410;
	}
}

/** The subset of RequestInit this client accepts. */
interface RequestOptions {
	method?: 'GET' | 'POST' | 'PUT' | 'PATCH';
	body?: unknown;
	signal?: AbortSignal;
}

/**
 * request performs one API call and returns the unwrapped `data` half of the
 * server's envelope.
 *
 * Every ssoosshd endpoint answers with `{data, error}` — including SSE event
 * payloads — so unwrapping happens in exactly one place and no caller sees
 * the envelope. `Envelope` in internal/apitypes carries the reasoning for
 * making it universal rather than per-endpoint.
 *
 * Uses fetch rather than a client library on purpose: the app needs cookie
 * credentials and JSON, both of which fetch does natively, and a dependency
 * that wraps it would still need this envelope layer on top.
 */
export async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
	const { method = 'GET', body, signal } = options;

	let response: Response;
	try {
		response = await fetch(`/api${path}`, {
			method,
			signal,
			// Same-origin is fetch's default for cookies, but state-changing
			// calls depend on the session cookie being sent, so it is stated
			// rather than inherited.
			credentials: 'same-origin',
			headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
			body: body === undefined ? undefined : JSON.stringify(body)
		});
	} catch (cause) {
		// fetch rejects only on network-level failure; an HTTP error status is
		// a resolved promise. Distinguish the two so "server unreachable"
		// doesn't get reported as an API error with a made-up status.
		throw new ApiError(0, cause instanceof Error ? cause.message : 'network request failed');
	}

	// 204 and friends carry no body to parse. Nothing in this API returns one
	// today — deny gained a body precisely so it wouldn't — but tolerating it
	// costs a line and avoids a confusing JSON parse error if one appears.
	if (response.status === 204) {
		return undefined as T;
	}

	let envelope: Envelope<T>;
	try {
		envelope = (await response.json()) as Envelope<T>;
	} catch {
		// A non-JSON body means something other than the API answered — a
		// proxy error page, or the SPA fallback serving index.html for a
		// mistyped path. Report the status rather than a parse error.
		throw new ApiError(response.status, `unexpected non-JSON response (HTTP ${response.status})`);
	}

	if (!response.ok) {
		throw new ApiError(
			response.status,
			envelope?.error || `request failed (HTTP ${response.status})`
		);
	}

	// A 200 whose envelope carries an error is a server-side contradiction,
	// but trusting `data` in that case would silently render an empty page.
	if (envelope?.error) {
		throw new ApiError(response.status, envelope.error);
	}

	return envelope.data as T;
}
