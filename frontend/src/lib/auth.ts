import { ApiError } from '$lib/api/client';
import { loginURL } from '$lib/api/endpoints';
import { isInternalPath } from '$lib/paths';

/**
 * startLogin sends the browser to the server's OIDC entry point, asking to
 * come back to returnTo afterwards.
 *
 * This is the jump to the identity provider itself, and /login is the only
 * caller: it is the screen that shows a deployment's consent notice, and
 * signing in is what happens after that notice is accepted. Anything else
 * that finds it needs an identity calls goToLogin instead.
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
 * loginPageURL builds the address of the app's own /login screen, carrying
 * returnTo so that signing in lands back where the user started.
 *
 * returnTo is filtered by the same rule the server's open-redirect guard
 * uses (see isInternalPath), so an off-site value falls back to a bare
 * /login rather than being carried into the sign-in that follows.
 *
 * Split out from goToLogin so that filtering can be tested on its own.
 * window.location's properties are unforgeable, so a test can neither
 * replace it nor spy on assign, and the navigation itself is unobservable.
 */
export function loginPageURL(returnTo?: string): string {
	if (!isInternalPath(returnTo)) {
		return '/login';
	}
	return `/login?return_to=${encodeURIComponent(returnTo)}`;
}

/**
 * goToLogin sends the browser to the app's own /login screen.
 *
 * Every "you need to sign in" path goes through here rather than calling
 * startLogin directly. /login is where branding.login_notice is shown, and
 * it can only block a sign-in it stands in front of — a page that jumps
 * straight to the identity provider signs the user in without the notice
 * ever appearing. That includes the approval page, which is the screen where
 * it matters most: a certificate request URL is how most people arrive at
 * this app at all.
 */
export function goToLogin(returnTo?: string): void {
	window.location.assign(loginPageURL(returnTo));
}

/**
 * currentPath returns the path the browser is on, for use as a return_to.
 * Includes the query string so a link into a filtered view survives login.
 */
export function currentPath(): string {
	return `${window.location.pathname}${window.location.search}`;
}

/**
 * redirectIfUnauthenticated sends the browser to /login when error is a 401,
 * and reports whether it did.
 *
 * Callers use the return value to decide whether to render an error at all:
 * during a redirect the page is about to be replaced, so showing "not
 * authenticated" would only flash a message the user cannot act on.
 */
export function redirectIfUnauthenticated(error: unknown): boolean {
	if (error instanceof ApiError && error.isUnauthenticated) {
		goToLogin(currentPath());
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
