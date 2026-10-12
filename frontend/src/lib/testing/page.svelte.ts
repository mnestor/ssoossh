import type { AfterNavigate, NavigationType } from '$app/navigation';

/**
 * A reactive stand-in for `$app/state`'s `page`, for tests that drive
 * shallow routing.
 *
 * The important detail is what a shallow `goto` here does NOT do:
 * SvelteKit's real `goto(url, { shallow: true, state })` updates the
 * address bar and `page.state`, but never reassigns `page.url`. A fake that updated the URL would let a page read
 * its shallow-routing data out of the search parameters and pass, then fail
 * in a browser. This one is deliberately as unhelpful as the real thing.
 */
class FakePage {
	// A plain URL, not SvelteURL, on purpose: the real page.url is a plain
	// URL, and this fake exists to behave exactly like the real thing. It is
	// only ever reassigned, never mutated in place, so $state carries the
	// reactivity without needing a reactive URL.
	url = $state(new URL('http://localhost/'));
	state = $state<App.PageState>({});
	// params are route parameters extracted from the URL path
	params = $state<Record<string, string>>({});
}

export const fakePage = new FakePage();

/** reset puts the fake back to a freshly-navigated page at `href`. */
export function resetFakePage(href: string): void {
	// eslint-disable-next-line svelte/prefer-svelte-reactivity -- see FakePage.url
	fakePage.url = new URL(href);
	fakePage.state = {};
}

/**
 * fakeGoto mirrors SvelteKit's shallow routing: state changes, URL does not.
 * A full navigation is not faked; it rejects so a test that reaches one
 * fails instead of passing on a page that never moved.
 *
 * The state is copied, as SvelteKit does (it serializes and parses it), so
 * every shallow goto assigns a new object even when handed `page.state`
 * itself. Assigning the same object would notify nobody, and an effect that
 * re-ran on its own write (#178) would pass here and hang a browser.
 */
export async function fakeGoto(
	url: string | URL,
	opts?: { shallow?: boolean; state?: App.PageState }
): Promise<void> {
	if (!opts?.shallow) {
		throw new Error('fakeGoto: only shallow navigation is faked');
	}
	const from = fakePage.url;
	fakePage.state = JSON.parse(JSON.stringify(opts.state ?? {}));
	// SvelteKit 3 runs after-navigation callbacks for a shallow goto too,
	// with the address it wrote as `to`. A page that followed that would
	// round its filters through its own write; firing it here catches one.
	// eslint-disable-next-line svelte/prefer-svelte-reactivity -- see FakePage.url
	runAfterNavigate('goto', true, from, new URL(url, from));
}

/** The after-navigation callbacks of whatever is mounted, as SvelteKit keeps them. */
// eslint-disable-next-line svelte/prefer-svelte-reactivity -- a registry, nothing renders from it
const afterNavigateCallbacks = new Set<(navigation: AfterNavigate) => void>();

/**
 * fakeAfterNavigate stands in for `$app/navigation`'s `afterNavigate`: it
 * keeps `callback` until the component (or effect root) that registered it
 * goes away. It does not fire the 'enter' navigation on mount; nothing
 * that uses it acts on one.
 */
export function fakeAfterNavigate(callback: (navigation: AfterNavigate) => void): void {
	afterNavigateCallbacks.add(callback);
	$effect(() => () => afterNavigateCallbacks.delete(callback));
}

/**
 * fakeNavigate is a full client-side navigation to `href` that keeps the
 * mounted page, such as a nav rail click to the page already on screen:
 * `page.url` moves, `page.state` is reset, and the after-navigation
 * callbacks run with the address left and the address reached.
 */
export function fakeNavigate(href: string, type: NavigationType = 'link'): void {
	const from = fakePage.url;
	// eslint-disable-next-line svelte/prefer-svelte-reactivity -- see FakePage.url
	const to = new URL(href, from);
	fakePage.url = to;
	fakePage.state = {};
	runAfterNavigate(type, false, from, to);
}

/**
 * runAfterNavigate calls every registered callback with a navigation from
 * `from` to `to`. The route id is the path, which is what it is for every
 * page these fakes serve.
 */
function runAfterNavigate(type: NavigationType, shallow: boolean, from: URL, to: URL): void {
	const target = (url: URL) => ({
		params: {},
		route: { id: url.pathname },
		url,
		scroll: null
	});
	const navigation = {
		type,
		shallow,
		from: target(from),
		to: target(to),
		willUnload: false,
		complete: Promise.resolve()
	} as unknown as AfterNavigate;
	for (const callback of [...afterNavigateCallbacks]) {
		callback(navigation);
	}
}
