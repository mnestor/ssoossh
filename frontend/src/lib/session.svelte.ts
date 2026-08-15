import { ApiError } from '$lib/api/client';
import { getCurrentUser } from '$lib/api/endpoints';
import type { CurrentUser } from '$lib/api/types';

/**
 * Session holds the caller's identity for the whole app.
 *
 * One instance, loaded once by the root layout: every screen wants to know
 * who it is acting as, and re-fetching /api/users/me per navigation would
 * add a round trip to each one for an answer that only changes at login and
 * logout.
 *
 * A 401 is recorded as "not signed in" rather than raised, because the
 * layout renders for signed-out visitors too — /login is a page like any
 * other, and the approval page decides for itself when to bounce to the IdP.
 */
export class Session {
	user = $state<CurrentUser | null>(null);
	/** True once a load attempt has finished, successfully or not. */
	resolved = $state(false);
	/** Set only for failures that are not "simply not signed in". */
	error = $state<string | null>(null);

	/** signedIn reports whether there is an identity to act as. */
	get signedIn(): boolean {
		return this.user !== null;
	}

	/**
	 * load fetches the current identity. Safe to call repeatedly; callers
	 * that only want it once should check `resolved` first.
	 */
	async load(): Promise<void> {
		try {
			this.user = await getCurrentUser();
			this.error = null;
		} catch (cause) {
			this.user = null;
			// Anything other than 401 is a real failure worth surfacing —
			// the server being unreachable should not look like being
			// logged out, or the user will loop through login forever.
			this.error =
				cause instanceof ApiError && cause.isUnauthenticated
					? null
					: cause instanceof Error
						? cause.message
						: 'failed to load the current user';
		} finally {
			this.resolved = true;
		}
	}

	/** clear drops the cached identity after a logout. */
	clear(): void {
		this.user = null;
		this.error = null;
		this.resolved = true;
	}
}

/** The app-wide session, loaded by the root layout. */
export const session = new Session();
