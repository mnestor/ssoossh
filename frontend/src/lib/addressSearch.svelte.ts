import { untrack } from 'svelte';
import { afterNavigate, goto, type AfterNavigate } from '$app/navigation';
import { page, type ReadonlyURLSearchParams } from '$app/state';

// Keeping a list's filters in the address bar, so a filtered view survives
// a reload and can be shared, without a SvelteKit navigation per change.
//
// One helper because the obvious way to write this loops (issue #178). A
// shallow update (`goto(url, { shallow: true })`, or the deprecated
// `replaceState`) changes the address bar and assigns a new `page.state`,
// but never `page.url`; the new address goes to `page.shallow.url`. The
// pages used to compare against `page.url.search`, which therefore never
// caught up with their own write, and passed `page.state` along, which made
// every write re-run the effect. Together that is a history write per
// microtask: the main thread never yields, Chrome throttles history
// ("Throttling navigation to prevent the browser from hanging"), and the
// page stops responding, nav rail included.
//
// So the effect tracks the filters and nothing else, and the address it
// compares against is the one the browser shows.

/**
 * writeAddressSearch puts `search` (a query string with its `?`, or '' for
 * none) in the address bar in place of the current one, keeping the path
 * and `page.state`. Does nothing when the address already says it. Replaces
 * rather than pushes: a filter change is not somewhere Back should return
 * to. Reads `page` without tracking it, so it is safe inside an effect.
 */
export function writeAddressSearch(search: string): void {
	if (search === window.location.search) {
		return;
	}
	const { pathname, state } = untrack(() => ({ pathname: page.url.pathname, state: page.state }));
	void (async () => {
		try {
			// A shallow, replace navigation that only changes the query string on
			// the current pathname, not a route id resolve() could check.
			// eslint-disable-next-line svelte/no-navigation-without-resolve
			await goto(`${pathname}${search}`, { shallow: true, replace: true, state });
		} catch {
			// The router refused (it has not started, or a navigation
			// superseded this one). The address stays as it was; the filters
			// themselves still apply, and the next change writes again.
		}
	})();
}

/**
 * syncAddressSearch keeps the address bar's query string and the page's
 * filters in step for as long as the calling component lives. Call it
 * during component initialisation.
 *
 * Filters to address: whenever `search()` changes, the address is rewritten
 * to it. Only what `search` reads is tracked, so a write cannot schedule
 * another. The first run writes nothing: the filters were just read from
 * the address the page opened at, and a write at that moment would land
 * inside the navigation that is mounting the page and cut it short
 * (SvelteKit treats a shallow goto as the newest navigation).
 *
 * Address to filters: a navigation that keeps this page mounted (a nav rail
 * click to the page already on screen, or Back to another of its addresses)
 * hands `follow` the query string it landed on, and `follow` sets every
 * filter from it, a missing key meaning that filter's default. So a click
 * to the bare path starts over (issue #182). This runs from the router's
 * after-navigation callback, never from an effect, so nothing that tracks
 * `page.url` or `page.state` writes the address. The filters it sets
 * describe the address already showing, so the write their change
 * triggers finds nothing to do (writeAddressSearch compares first).
 */
export function syncAddressSearch(
	search: () => string,
	follow: (params: ReadonlyURLSearchParams) => void
): void {
	let opened = false;
	$effect(() => {
		const next = search();
		if (!opened) {
			opened = true;
			return;
		}
		untrack(() => writeAddressSearch(next));
	});
	afterNavigate((navigation) => {
		const params = landedSearch(navigation);
		if (params) {
			untrack(() => follow(params));
		}
	});
}

/**
 * landedSearch is the query string a navigation left a still-mounted page
 * at, or null when the page has nothing to follow:
 *   - 'enter' is the navigation that mounted it; the filters were read from
 *     that very address.
 *   - a shallow goto is an address write (writeAddressSearch's own, or
 *     another in-place update), not somewhere new; following it would round
 *     the filters through the address and trim a search box mid-word.
 *   - a different route means this page is being left, not revisited.
 * `to.url` is the address the browser shows, the shallow one included
 * when Back lands on an entry a filter write replaced.
 */
export function landedSearch(navigation: AfterNavigate): ReadonlyURLSearchParams | null {
	const { type, shallow, from, to } = navigation;
	if (type === 'enter' || (type === 'goto' && shallow) || !to) {
		return null;
	}
	if (from?.route.id !== to.route.id) {
		return null;
	}
	return to.url.searchParams;
}
