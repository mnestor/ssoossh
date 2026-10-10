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
 */
export async function fakeGoto(
	_url: string | URL,
	opts?: { shallow?: boolean; state?: App.PageState }
): Promise<void> {
	if (!opts?.shallow) {
		throw new Error('fakeGoto: only shallow navigation is faked');
	}
	fakePage.state = opts.state ?? {};
}
