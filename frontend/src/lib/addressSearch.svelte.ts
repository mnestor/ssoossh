import { untrack } from 'svelte';
import { goto } from '$app/navigation';
import { page } from '$app/state';

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
			await goto(`${pathname}${search}`, { shallow: true, replace: true, state });
		} catch {
			// The router refused (it has not started, or a navigation
			// superseded this one). The address stays as it was; the filters
			// themselves still apply, and the next change writes again.
		}
	})();
}

/**
 * syncAddressSearch keeps the address bar's query string equal to
 * `search()` for as long as the calling component lives. Call it during
 * component initialisation.
 *
 * Only what `search` reads is tracked, so a write cannot schedule another.
 * The first run writes nothing: the filters were just read from the address
 * the page opened at, and a write at that moment would land inside the
 * navigation that is mounting the page and cut it short (SvelteKit treats a
 * shallow goto as the newest navigation).
 */
export function syncAddressSearch(search: () => string): void {
	let opened = false;
	$effect(() => {
		const next = search();
		if (!opened) {
			opened = true;
			return;
		}
		untrack(() => writeAddressSearch(next));
	});
}
