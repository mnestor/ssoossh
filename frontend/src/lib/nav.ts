// The admin entries below use plain hrefs rather than resolve(). This model
// names all admin sections, but each section's page lands on its own feature
// branch, and resolve() is typed against the routes that exist in THIS tree.
// Using it would force a placeholder page for every absent route, and a
// placeholder sharing a path with another branch's real page is a merge
// waiting to resolve the wrong way. Carried over from the admin layout's
// tab row, which this model replaced. The lint rule that wants resolve()
// fires where the href reaches the DOM, so the exemption lives in
// RailItem.svelte rather than here.
import { resolve } from '$app/paths';

/** One destination in the rail. */
export interface NavItem {
	/** A final URL, not a route id: the admin entries cannot be resolved. */
	href: string;
	label: string;
	/** Icon name from Icon.svelte's map. */
	icon: string;
}

/**
 * primaryNav is what any signed-in identity can reach.
 *
 * A function rather than a constant because resolve() reads SvelteKit's
 * configured base path, which is not settled at module-evaluation time in
 * every environment the app is built for.
 */
export function primaryNav(): NavItem[] {
	return [
		{ href: resolve('/dashboard'), label: 'Dashboard', icon: 'layout-dashboard' },
		{ href: resolve('/logs/me'), label: 'History', icon: 'history' },
		{ href: resolve('/service-codes'), label: 'Service codes', icon: 'key' },
		// The console code box needs an entry point that is not a
		// transcribed URL: the whole premise is that the machine in front of
		// the user cannot print a link anyone will copy, so somebody already
		// signed in has to be able to find this from the app itself.
		{ href: resolve('/console'), label: 'Console login', icon: 'keyboard' }
	];
}

/** adminNav is the auditor-only group. See the file header on why these are
 * plain hrefs. */
export const adminNav: readonly NavItem[] = [
	{ href: '/admin/users', label: 'Users', icon: 'users' },
	{ href: '/admin/certificates', label: 'Certificates', icon: 'file-certificate' },
	// The caller's own codes take `key` and the registry of every code
	// takes `circle-key`: one noun, two enclosures. A collapsed rail drops
	// the group's indent and rule (see RailGroup.svelte), so an admin row
	// has no visual parent at that width and the two scopes cannot share
	// a glyph without becoming indistinguishable.
	{ href: '/admin/service-codes', label: 'Service codes', icon: 'circle-key' },
	{ href: '/admin/config', label: 'Config', icon: 'adjustments-horizontal' },
	{ href: '/admin/directory', label: 'Directory', icon: 'address-book' },
	{ href: '/admin/identity/echo', label: 'Claims echo', icon: 'braces' },
	{ href: '/admin/audit', label: 'Audit log', icon: 'logs' },
	{ href: '/admin/diagnostics', label: 'Diagnostics', icon: 'activity-heartbeat' }
] as const;

/** accountNav sits in the rail's footer, below the destinations. */
export function accountNav(): NavItem[] {
	return [{ href: resolve('/preferences'), label: 'Preferences', icon: 'user-cog' }];
}

/**
 * Routes that render without the rail.
 *
 * These are the screens a visitor reaches without an identity, or before
 * one is established: a sign-in form, a console code transcribed off a
 * machine that cannot print a link, and the notice that says an approval
 * link is no longer good for anything. A navigation column on any of them
 * offers destinations the visitor cannot follow.
 *
 * The approval screen itself is deliberately not here any more. It used to
 * be, on the grounds that a rail beside a decision invites wandering off
 * mid-decision — but reaching it means signing in first, so its reader is
 * inside the app by the time they see it, and a page with no chrome read as
 * a different site rather than as a focused one. Nothing is lost by
 * leaving: the approval link is still there when they come back, and the
 * decision is recorded on the server, not in the page.
 */
const focusRoutes = ['/login', '/c', '/approval-unavailable'];

/**
 * isFocusRoute reports whether pathname is one of the screens that renders
 * without the rail.
 *
 * Matches a segment boundary rather than a bare prefix, so "/console" is not
 * treated as the "/c" console-code screen.
 */
export function isFocusRoute(pathname: string): boolean {
	return focusRoutes.some((route) => pathname === route || pathname.startsWith(route + '/'));
}

/**
 * isCurrent reports whether item names the page at pathname.
 *
 * Matches a section's own page and everything under it, so a detail page
 * (/admin/users/<id>) keeps its section marked rather than leaving the rail
 * with nothing selected. The segment boundary is what stops
 * /admin/certificates from lighting up for /admin/certificates-archive.
 */
export function isCurrent(href: string, pathname: string): boolean {
	return pathname === href || pathname.startsWith(href + '/');
}

/** isAdminRoute reports whether pathname is inside the admin area, which is
 * what opens the rail's admin group on arrival. */
export function isAdminRoute(pathname: string): boolean {
	return isCurrent('/admin', pathname);
}
