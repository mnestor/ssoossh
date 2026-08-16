import { ApiError } from '$lib/api/client';
import { loginURL } from '$lib/api/endpoints';

/**
 * startLogin sends the browser to the server's OIDC entry point, asking to
 * come back to returnTo afterwards.
 *
 * A full document navigation rather than SvelteKit's goto(): /auth/login is
 * a server route that answers with a 302 to the identity provider, so the
 * client-side router must not try to resolve it as a page. The server
 * revalidates return_to with its own open-redirect guard
 * (server/controller/auth.go's isSafeReturnURL), and loginURL only sends
 * paths, so nothing here can steer the browser off-site.
 */
export function startLogin(returnTo?: string): void {
	window.location.assign(loginURL(returnTo));
}

/**
 * currentPath returns the path the browser is on, for use as a return_to.
 * Includes the query string so a link into a filtered view survives login.
 */
export function currentPath(): string {
	return `${window.location.pathname}${window.location.search}`;
}

/**
 * redirectIfUnauthenticated starts a login round trip when error is a 401,
 * and reports whether it did.
 *
 * Callers use the return value to decide whether to render an error at all:
 * during a redirect the page is about to be replaced, so showing "not
 * authenticated" would only flash a message the user cannot act on.
 */
export function redirectIfUnauthenticated(error: unknown): boolean {
	if (error instanceof ApiError && error.isUnauthenticated) {
		startLogin(currentPath());
		return true;
	}
	return false;
}

/** errorMessage extracts a displayable message from an unknown throw. */
export function errorMessage(error: unknown): string {
	if (error instanceof ApiError) {
		return error.message;
	}
	if (error instanceof Error) {
		return error.message;
	}
	return 'something went wrong';
}
