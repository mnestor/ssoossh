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
vi.mock('$app/navigation', () => ({ goto }));

import { fakeGoto, fakePage, resetFakePage } from '#lib/testing/page.svelte.js';
import { syncAddressSearch, writeAddressSearch } from './addressSearch.svelte.js';

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
 * and returns a setter for it and the teardown.
 */
function mountSync(initial: string) {
	let search = $state(initial);
	const stop = $effect.root(() => {
		syncAddressSearch(() => search);
	});
	flushSync();
	return {
		set(next: string) {
			search = next;
			flushSync();
		},
		stop
	};
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
