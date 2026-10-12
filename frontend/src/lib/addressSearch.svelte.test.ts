import { flushSync } from 'svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// The router is faked the way SvelteKit behaves, which is the whole point
// of these tests (issue #178): a shallow goto moves the address bar and
// reassigns page.state, and never touches page.url. A sync that tracked
// page.state, or compared against page.url, writes again on its own write.
const { goto } = vi.hoisted(() => ({ goto: vi.fn() }));

vi.mock('$app/state', async () => {
	const { fakePage } = await import('#lib/testing/page.svelte.js');
	return { page: fakePage };
});
vi.mock('$app/navigation', async () => {
	const { fakeAfterNavigate } = await import('#lib/testing/page.svelte.js');
	return { goto, afterNavigate: fakeAfterNavigate };
});

import type { AfterNavigate, NavigationType } from '$app/navigation';
import { fakeGoto, fakeNavigate, fakePage, resetFakePage } from '#lib/testing/page.svelte.js';
import { landedSearch, syncAddressSearch, writeAddressSearch } from './addressSearch.svelte.js';

/** browserGoto is fakeGoto plus what the browser does: the address moves. */
async function browserGoto(url: string | URL, opts?: { shallow?: boolean; state?: App.PageState }) {
	window.history.replaceState(null, '', String(url));
	await fakeGoto(url, opts);
}

/** settle lets the fake goto's promise and any effect it scheduled run. */
async function settle() {
	await new Promise((resolve) => setTimeout(resolve, 20));
	flushSync();
}

/**
 * mountSync starts a sync over a reactive search string, as a page would,
 * and returns a setter for it, the teardown, and every query string the
 * sync asked the page to follow. Following sets the search from `status`,
 * the way a page reads its filters out of an address.
 */
function mountSync(initial: string) {
	let search = $state(initial);
	const followed: string[] = [];
	const stop = $effect.root(() => {
		syncAddressSearch(
			() => search,
			(params) => {
				followed.push(params.toString());
				const status = params.get('status');
				search = status ? `?status=${status}` : '';
			}
		);
	});
	flushSync();
	return {
		set(next: string) {
			search = next;
			flushSync();
		},
		get search() {
			return search;
		},
		followed,
		stop
	};
}

/** railClick is a full navigation to href that keeps the page mounted. */
function railClick(href: string) {
	window.history.pushState(null, '', href);
	fakeNavigate(href);
	flushSync();
}

let teardown: (() => void) | undefined;

beforeEach(() => {
	goto.mockReset();
	goto.mockImplementation(browserGoto);
	resetFakePage('http://localhost/logs/me');
	window.history.replaceState(null, '', '/logs/me');
});

afterEach(() => {
	teardown?.();
	teardown = undefined;
});

describe('syncAddressSearch', () => {
	it('should not write the address when the page opens', async () => {
		teardown = mountSync('?status=live').stop;
		await settle();
		expect(goto).not.toHaveBeenCalled();
	});

	it('should write a changed search in place without a navigation', async () => {
		const sync = mountSync('');
		teardown = sync.stop;
		sync.set('?status=live');
		await settle();
		expect(goto).toHaveBeenCalledWith('/logs/me?status=live', {
			shallow: true,
			replace: true,
			state: {}
		});
	});

	it('should write once per change even though each write reassigns page.state', async () => {
		const sync = mountSync('');
		teardown = sync.stop;
		sync.set('?status=live');
		await settle();
		await settle();
		expect(goto).toHaveBeenCalledTimes(1);
	});

	it('should write again on the next change', async () => {
		const sync = mountSync('');
		teardown = sync.stop;
		sync.set('?status=live');
		await settle();
		sync.set('?status=live&type=user');
		await settle();
		expect(goto).toHaveBeenLastCalledWith('/logs/me?status=live&type=user', expect.anything());
	});

	it('should stop writing once the component is gone', async () => {
		const sync = mountSync('');
		sync.stop();
		sync.set('?status=live');
		await settle();
		expect(goto).not.toHaveBeenCalled();
	});
});

describe('syncAddressSearch following the address', () => {
	it('should hand the page the query string a navigation to it landed on', async () => {
		const sync = mountSync('');
		teardown = sync.stop;
		railClick('/logs/me?status=expired');
		expect(sync.followed).toEqual(['status=expired']);
	});

	it('should put the filters back at their defaults when a nav click lands on the bare path', async () => {
		const sync = mountSync('');
		teardown = sync.stop;
		sync.set('?status=live');
		await settle();
		railClick('/logs/me');
		expect(sync.search).toBe('');
	});

	it('should not write the address when a nav click to the bare path resets the filters', async () => {
		const sync = mountSync('');
		teardown = sync.stop;
		sync.set('?status=live');
		await settle();
		goto.mockClear();
		railClick('/logs/me');
		await settle();
		expect(goto).not.toHaveBeenCalled();
	});

	it('should not follow its own address write', async () => {
		const sync = mountSync('');
		teardown = sync.stop;
		sync.set('?status=live');
		await settle();
		expect(sync.followed).toEqual([]);
	});

	it('should stop following once the component is gone', () => {
		const sync = mountSync('');
		sync.stop();
		railClick('/logs/me?status=expired');
		expect(sync.followed).toEqual([]);
	});
});

describe('landedSearch', () => {
	/** navigation builds an after-navigation event between two addresses. */
	function navigation(
		type: NavigationType,
		shallow: boolean,
		from: string | null,
		to: string | null
	): AfterNavigate {
		const target = (href: string) => {
			const url = new URL(href, 'http://localhost');
			return { params: {}, route: { id: url.pathname }, url, scroll: null };
		};
		return {
			type,
			shallow,
			from: from === null ? null : target(from),
			to: to === null ? null : target(to),
			willUnload: false,
			complete: Promise.resolve()
		} as unknown as AfterNavigate;
	}

	it.each([
		{
			why: 'a link to the same page',
			nav: navigation('link', false, '/logs/me?status=live', '/logs/me'),
			want: ''
		},
		{
			why: 'a link to the same page with filters',
			nav: navigation('link', false, '/logs/me', '/logs/me?type=pam'),
			want: 'type=pam'
		},
		{
			why: 'a full goto to the same page',
			nav: navigation('goto', false, '/logs/me?q=x', '/logs/me'),
			want: ''
		},
		{
			why: 'Back to an address a filter write left',
			nav: navigation('popstate', true, '/logs/me', '/logs/me?status=live'),
			want: 'status=live'
		},
		{
			why: 'the navigation that mounted the page',
			nav: navigation('enter', false, null, '/logs/me?status=live'),
			want: null
		},
		{
			why: 'a shallow goto (an address write)',
			nav: navigation('goto', true, '/logs/me', '/logs/me?status=live'),
			want: null
		},
		{
			why: 'a navigation to another page',
			nav: navigation('link', false, '/logs/me', '/dashboard'),
			want: null
		},
		{
			why: 'a navigation from another page',
			nav: navigation('link', false, '/dashboard', '/logs/me'),
			want: null
		},
		{
			why: 'a navigation with nowhere to land',
			nav: navigation('leave', false, '/logs/me', null),
			want: null
		}
	])('should return $want for $why', ({ nav, want }) => {
		expect(landedSearch(nav)?.toString() ?? null).toBe(want);
	});
});

describe('writeAddressSearch', () => {
	it.each([
		{ address: '/logs/me?status=live', search: '?status=live', writes: 0 },
		{ address: '/logs/me', search: '', writes: 0 },
		{ address: '/logs/me', search: '?status=live', writes: 1 },
		{ address: '/logs/me?status=live', search: '', writes: 1 }
	])(
		'should write $writes time(s) when the address is $address and the search is "$search"',
		async ({ address, search, writes }) => {
			window.history.replaceState(null, '', address);
			writeAddressSearch(search);
			await settle();
			expect(goto).toHaveBeenCalledTimes(writes);
		}
	);

	it('should keep the page state it was handed', async () => {
		fakePage.state = { accountName: 'svc-deploy' } as App.PageState;
		writeAddressSearch('?status=live');
		await settle();
		expect(goto).toHaveBeenCalledWith(
			'/logs/me?status=live',
			expect.objectContaining({ state: { accountName: 'svc-deploy' } })
		);
	});

	it('should not throw when the router refuses the write', async () => {
		goto.mockRejectedValue(new Error('navigation aborted'));
		expect(() => writeAddressSearch('?status=live')).not.toThrow();
		await settle();
	});
});
