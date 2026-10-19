import { render, screen, waitFor, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { axe, toHaveNoViolations } from 'jest-axe';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { NSSAdminKey, NSSAdminKeysResponse } from '#lib/api/types.js';
import { session } from '#lib/session.svelte.js';
import { envelope, routeFetch, settle, userFixture, type Call } from '#lib/testing/nss.js';

// Test methodology: the admin key list with its one read routed to fixture
// pages and the address driven by the fake page. What is pinned is what an
// investigation relies on: the search reads text or an address and says
// which, a bad address is refused in the field without a request (and the
// server's 400 lands in the same place), every filter goes to the server
// and into the address, each row opens the key's own page with the way
// back, every empty state says why, and Load more keeps what is shown. The
// component fetches on mount, so its axe pass is here rather than in the
// a11y sweep.

expect.extend(toHaveNoViolations);

const { goto } = vi.hoisted(() => ({ goto: vi.fn() }));
vi.mock('$app/state', async () => {
	const { fakePage } = await import('#lib/testing/page.svelte.js');
	return { page: fakePage };
});
vi.mock('$app/navigation', async () => {
	const { fakeAfterNavigate } = await import('#lib/testing/page.svelte.js');
	return { goto, afterNavigate: fakeAfterNavigate };
});

import { fakeGoto, resetFakePage } from '#lib/testing/page.svelte.js';
import NSSKeyAdminList from './NSSKeyAdminList.svelte';

const PATH = '/api/admin/nss/keys';
const LIST = '/admin/nss/keys';
const shallowReplace = expect.objectContaining({ shallow: true, replace: true });
const now = new Date('2026-10-18T12:00:00Z');

/** adminKey is one key on the admin list, active and in use. */
function adminKey(name: string, overrides: Partial<NSSAdminKey> = {}): NSSAdminKey {
	return {
		id: `nss_${name
			.replace(/[^a-z0-9]/g, '')
			.padEnd(8, '0')
			.slice(0, 8)}`,
		name,
		group_name: 'web-admins',
		status: 'active',
		current_generation: 2,
		expires_at: '2027-10-18T12:00:00Z',
		created_at: '2026-01-01T00:00:00Z',
		creator: { user_id: 'u-kim', username: 'kim', email: 'kim@example.org', exists: true },
		hosts_7d: 14,
		last_seen_at: '2026-10-18T10:00:00Z',
		...overrides
	};
}

/** listPage answers one page of keys. */
function listPage(keys: NSSAdminKey[], extra: Partial<NSSAdminKeysResponse> = {}): Response {
	return envelope({ keys, total: keys.length, usage_retention_seconds: 7776000, ...extra });
}

/** requested is the query of every list read made. */
function requested(calls: Call[]): URLSearchParams[] {
	return calls
		.filter((call) => call.url.split('?')[0] === PATH)
		.map((call) => new URLSearchParams(call.url.split('?')[1] ?? ''));
}

/** typeSearch types into the search box and presses Enter. */
async function typeSearch(text: string) {
	const box = screen.getByTestId('nss-admin-keys-search');
	await userEvent.clear(box);
	await userEvent.type(box, `${text}{Enter}`);
}

beforeEach(() => {
	vi.useFakeTimers({ now, toFake: ['Date'] });
	resetFakePage(`http://localhost${LIST}`);
	goto.mockReset();
	goto.mockImplementation(fakeGoto);
	session.user = userFixture({ is_auditor: true });
	session.resolved = true;
});

afterEach(() => {
	vi.useRealTimers();
	vi.unstubAllGlobals();
	session.clear();
});

describe('NSS admin key list rows', () => {
	it('should open each key on its own page with the way back when a row is shown', async () => {
		resetFakePage(`http://localhost${LIST}?q=web&unused=1`);
		routeFetch({ [`GET ${PATH}`]: listPage([adminKey('web fleet')]) });
		render(NSSKeyAdminList);

		expect(await screen.findByTestId('nss-admin-keys-link')).toHaveAttribute(
			'href',
			`/nss-keys/nss_webfleet?from=admin&back=${encodeURIComponent('?q=web&unused=1')}`
		);
	});

	it('should name the group under the key when the key has one', async () => {
		routeFetch({ [`GET ${PATH}`]: listPage([adminKey('web fleet')]) });
		render(NSSKeyAdminList);

		const row = await screen.findByTestId('nss-admin-keys-row');
		expect(within(row).getByText('web-admins')).toBeInTheDocument();
	});

	it('should say no group when the key has none', async () => {
		routeFetch({ [`GET ${PATH}`]: listPage([adminKey('bastions', { group_name: '' })]) });
		render(NSSKeyAdminList);

		const row = await screen.findByTestId('nss-admin-keys-row');
		expect(within(row).getByText('no group')).toBeInTheDocument();
	});

	const creatorCases = [
		{
			name: 'the creator has an account',
			key: adminKey('web fleet'),
			want: 'kimkim@example.org'
		},
		{
			name: 'the creator account is gone',
			key: adminKey('web fleet', {
				creator: { user_id: 'u-gone-1234', username: '', email: '', exists: false }
			}),
			want: 'unknown useru-gone-1234'
		}
	];
	for (const { name, key, want } of creatorCases) {
		it(`should show ${want} as the creator when ${name}`, async () => {
			routeFetch({ [`GET ${PATH}`]: listPage([key]) });
			render(NSSKeyAdminList);

			expect(
				(await screen.findByTestId('nss-admin-keys-creator')).textContent?.replace(/\s/g, '')
			).toBe(want.replace(/\s/g, ''));
		});
	}

	it('should count the hosts of the last 7 days when the search is text', async () => {
		routeFetch({ [`GET ${PATH}`]: listPage([adminKey('web fleet')]) });
		render(NSSKeyAdminList);

		expect(await screen.findByTestId('nss-admin-keys-hosts')).toHaveTextContent('14');
	});

	const seenCases = [
		{ name: 'a host used the key two hours ago', key: adminKey('a'), want: '2h ago' },
		{
			name: 'no host has used the key',
			key: adminKey('a', { last_seen_at: undefined }),
			want: 'Never'
		}
	];
	for (const { name, key, want } of seenCases) {
		it(`should say ${want} under last seen when ${name}`, async () => {
			routeFetch({ [`GET ${PATH}`]: listPage([key]) });
			render(NSSKeyAdminList);

			expect(await screen.findByTestId('nss-admin-keys-last-seen')).toHaveTextContent(want);
		});
	}

	const expiryCases = [
		{
			name: 'the key expires within 30 days',
			key: adminKey('a', { expires_at: '2026-10-28T12:00:00Z' }),
			want: '10d left'
		},
		{
			name: 'the key never expires',
			key: adminKey('a', { expires_at: undefined }),
			want: 'never expires'
		},
		{
			name: 'the key is revoked',
			key: adminKey('a', { status: 'revoked', revoked_at: '2026-10-01T12:00:00Z' }),
			want: 'revoked'
		}
	];
	for (const { name, key, want } of expiryCases) {
		it(`should say ${want} under the status when ${name}`, async () => {
			routeFetch({ [`GET ${PATH}`]: listPage([key]) });
			render(NSSKeyAdminList);

			expect(await screen.findByTestId('nss-admin-keys-expiry')).toHaveTextContent(want);
		});
	}

	it('should name a recent alert under the status when one fired', async () => {
		routeFetch({
			[`GET ${PATH}`]: listPage([adminKey('a', { key_source_alert_at: '2026-10-17T00:00:00Z' })])
		});
		render(NSSKeyAdminList);

		expect(await screen.findByTestId('nss-admin-keys-alert')).toHaveTextContent('Key source alert');
	});

	it('should have no axe violations when rows are shown', async () => {
		routeFetch({
			[`GET ${PATH}`]: listPage([adminKey('a')], { next_cursor: 'nss_a0000000', total: 3 })
		});
		const { container } = render(NSSKeyAdminList);
		await screen.findByTestId('nss-admin-keys-table');

		vi.useRealTimers();
		expect(await axe(container)).toHaveNoViolations();
	});
});

describe('NSS admin key list search', () => {
	it('should send the search the address names when the page opens', async () => {
		resetFakePage(`http://localhost${LIST}?q=kim`);
		const calls = routeFetch({ [`GET ${PATH}`]: listPage([]) });
		render(NSSKeyAdminList);
		await settle();

		expect(requested(calls)[0].get('q')).toBe('kim');
	});

	it('should write a typed search back to the address when Enter is pressed', async () => {
		routeFetch({ [`GET ${PATH}`]: listPage([adminKey('a')]) });
		render(NSSKeyAdminList);
		await screen.findByTestId('nss-admin-keys-table');
		await typeSearch('kim');

		await waitFor(() => expect(goto).toHaveBeenCalledWith(`${LIST}?q=kim`, shallowReplace));
	});

	it('should name the key id among what the field searches when the field is shown', async () => {
		routeFetch({ [`GET ${PATH}`]: listPage([]) });
		render(NSSKeyAdminList);
		await settle();

		expect(screen.getByTestId('nss-admin-keys-search')).toHaveAccessibleDescription(
			'A name, group, creator or key id, or an address (10.0.4.7) or range (10.0.4.0/24).'
		);
	});

	it('should send a pasted key id to the server as text when Enter is pressed', async () => {
		const calls = routeFetch({ [`GET ${PATH}`]: listPage([adminKey('a')]) });
		render(NSSKeyAdminList);
		await screen.findByTestId('nss-admin-keys-table');
		await typeSearch('nss_5vqsreyx');

		await waitFor(() => expect(requested(calls).at(-1)?.get('q')).toBe('nss_5vqsreyx'));
	});

	it('should say which addresses are searched over the server window when q is a range', async () => {
		resetFakePage(`http://localhost${LIST}?q=10.0.4.7/24`);
		routeFetch({
			[`GET ${PATH}`]: listPage([adminKey('a')], {
				address_search: '10.0.4.0/24',
				usage_retention_seconds: 30 * 86400
			})
		});
		render(NSSKeyAdminList);

		expect(await screen.findByTestId('nss-admin-keys-searching')).toHaveTextContent(
			'Keys used from 10.0.4.0/24 in the last 30 days.'
		);
	});

	it('should list the addresses the key was used from when q is an address', async () => {
		resetFakePage(`http://localhost${LIST}?q=10.0.4.0/24`);
		routeFetch({
			[`GET ${PATH}`]: listPage(
				[
					adminKey('a', {
						matched: {
							addresses: [
								{
									source_ip: '10.0.4.7',
									last_seen: '2026-10-18T11:00:00Z',
									generation: 2,
									secret: 'current'
								},
								{
									source_ip: '10.0.4.9',
									last_seen: '2026-10-16T12:00:00Z',
									generation: 1,
									secret: 'previous'
								}
							],
							total: 7
						}
					})
				],
				{ address_search: '10.0.4.0/24' }
			)
		});
		render(NSSKeyAdminList);

		const cell = await screen.findByTestId('nss-admin-keys-used-from');
		const lines = [...cell.children].map((line) => line.textContent?.replace(/\s+/g, ' ').trim());
		expect(lines).toEqual([
			'10.0.4.7 1h ago, current secret',
			'10.0.4.9 2d ago, previous secret',
			'and 5 more'
		]);
	});

	it('should replace the host count with the addresses when q is an address', async () => {
		resetFakePage(`http://localhost${LIST}?q=10.0.4.7`);
		routeFetch({
			[`GET ${PATH}`]: listPage([adminKey('a', { matched: { addresses: [], total: 0 } })], {
				address_search: '10.0.4.7'
			})
		});
		render(NSSKeyAdminList);
		await screen.findByTestId('nss-admin-keys-table');

		expect(screen.getByRole('columnheader', { name: 'Used from' })).toBeInTheDocument();
	});

	const invalidCases = [
		{ q: '10.0.4.0/33', want: '10.0.4.0/33 is not a valid range: an IPv4 prefix is 0 to 32.' },
		{
			q: '10.0.4',
			want: '10.0.4 is not a valid address: an IPv4 address has four parts, like 10.0.4.7.'
		}
	];
	for (const { q, want } of invalidCases) {
		it(`should refuse ${q} in the field without asking the server when Enter is pressed`, async () => {
			const calls = routeFetch({ [`GET ${PATH}`]: listPage([adminKey('a')]) });
			render(NSSKeyAdminList);
			await screen.findByTestId('nss-admin-keys-table');
			await typeSearch(q);
			await settle();

			expect(screen.getByTestId('nss-admin-keys-search-error')).toHaveTextContent(want);
			expect(requested(calls).some((params) => params.has('q'))).toBe(false);
		});
	}

	it('should mark the field invalid and point it at the error when the term is refused', async () => {
		routeFetch({ [`GET ${PATH}`]: listPage([adminKey('a')]) });
		render(NSSKeyAdminList);
		await screen.findByTestId('nss-admin-keys-table');
		await typeSearch('10.0.4.0/33');

		const box = screen.getByTestId('nss-admin-keys-search');
		const error = screen.getByTestId('nss-admin-keys-search-error');
		expect([
			box.getAttribute('aria-invalid'),
			box.getAttribute('aria-describedby')?.split(' ')[0]
		]).toEqual(['true', error.id]);
	});

	it('should not call out a half-typed address while typing settles', async () => {
		routeFetch({ [`GET ${PATH}`]: listPage([adminKey('a')]) });
		render(NSSKeyAdminList);
		await screen.findByTestId('nss-admin-keys-table');
		await userEvent.type(screen.getByTestId('nss-admin-keys-search'), '10.0.');
		await new Promise((resolve) => setTimeout(resolve, 350));

		expect(screen.queryByTestId('nss-admin-keys-search-error')).not.toBeInTheDocument();
	});

	it('should show the server refusal in the field when the server answers 400', async () => {
		resetFakePage(`http://localhost${LIST}?q=1::2::3`);
		routeFetch({
			[`GET ${PATH}`]: (call) =>
				call.url.includes('q=')
					? envelope(null, 400, '1::2::3 is not a valid address: it is not an IPv6 address.')
					: listPage([adminKey('a')])
		});
		render(NSSKeyAdminList);

		expect(await screen.findByTestId('nss-admin-keys-search-error')).toHaveTextContent(
			'1::2::3 is not a valid address: it is not an IPv6 address.'
		);
	});

	it('should have no axe violations when the field shows an error', async () => {
		routeFetch({ [`GET ${PATH}`]: listPage([adminKey('a')]) });
		const { container } = render(NSSKeyAdminList);
		await screen.findByTestId('nss-admin-keys-table');
		await typeSearch('10.0.4.0/33');

		vi.useRealTimers();
		expect(await axe(container)).toHaveNoViolations();
	});
});

describe('NSS admin key list filters', () => {
	const addressCases = [
		{ name: 'the status', search: '?status=revoked', param: 'status', want: 'revoked' },
		{ name: 'the expiry window', search: '?expiring=7', param: 'expiring_within', want: '7' },
		{ name: 'unused', search: '?unused=1', param: 'unused', want: '1' },
		{ name: 'alerts', search: '?alerts=1', param: 'alerts', want: '1' }
	];
	for (const { name, search, param, want } of addressCases) {
		it(`should send ${name} the address names when the page opens`, async () => {
			resetFakePage(`http://localhost${LIST}${search}`);
			const calls = routeFetch({ [`GET ${PATH}`]: listPage([]) });
			render(NSSKeyAdminList);
			await settle();

			expect(requested(calls)[0].get(param)).toBe(want);
		});
	}

	const unknownCases = [
		{ name: 'a status that does not exist', search: '?status=gone', param: 'status' },
		{ name: 'a window the page does not offer', search: '?expiring=5', param: 'expiring_within' },
		{ name: 'an unused flag that is not 1', search: '?unused=yes', param: 'unused' }
	];
	for (const { name, search, param } of unknownCases) {
		it(`should drop ${name} when the address names it`, async () => {
			resetFakePage(`http://localhost${LIST}${search}`);
			const calls = routeFetch({ [`GET ${PATH}`]: listPage([]) });
			render(NSSKeyAdminList);
			await settle();

			expect(requested(calls)[0].has(param)).toBe(false);
		});
	}

	it('should ask for keys expiring within 30 days when Expiring is turned on', async () => {
		const calls = routeFetch({ [`GET ${PATH}`]: listPage([adminKey('a')]) });
		render(NSSKeyAdminList);
		await screen.findByTestId('nss-admin-keys-table');
		await userEvent.click(screen.getByTestId('nss-admin-keys-expiring'));
		await settle();

		expect(requested(calls).at(-1)?.get('expiring_within')).toBe('30');
	});

	it('should offer the window beside Expiring only when Expiring is on', async () => {
		routeFetch({ [`GET ${PATH}`]: listPage([adminKey('a')]) });
		render(NSSKeyAdminList);
		await screen.findByTestId('nss-admin-keys-table');
		const before = screen.queryByTestId('nss-admin-keys-window');
		await userEvent.click(screen.getByTestId('nss-admin-keys-expiring'));

		expect([before, screen.queryByTestId('nss-admin-keys-window') !== null]).toEqual([null, true]);
	});

	it('should mark a toggle pressed when it is on', async () => {
		routeFetch({ [`GET ${PATH}`]: listPage([adminKey('a')]) });
		render(NSSKeyAdminList);
		await screen.findByTestId('nss-admin-keys-table');
		await userEvent.click(screen.getByTestId('nss-admin-keys-unused'));

		expect(screen.getByTestId('nss-admin-keys-unused')).toHaveAttribute('aria-pressed', 'true');
	});

	it('should write the filters back to the address when they change', async () => {
		routeFetch({ [`GET ${PATH}`]: listPage([adminKey('a')]) });
		render(NSSKeyAdminList);
		await screen.findByTestId('nss-admin-keys-table');
		await userEvent.click(screen.getByTestId('nss-admin-keys-alerts'));

		await waitFor(() => expect(goto).toHaveBeenCalledWith(`${LIST}?alerts=1`, shallowReplace));
	});

	it('should read the unfiltered list again when Clear filters is pressed', async () => {
		resetFakePage(`http://localhost${LIST}?q=nobody&status=expired&unused=1`);
		const calls = routeFetch({ [`GET ${PATH}`]: listPage([]) });
		render(NSSKeyAdminList);
		await userEvent.click(await screen.findByTestId('nss-admin-keys-clear'));
		await settle();

		expect(requested(calls).at(-1)?.toString()).toBe('limit=50');
	});
});

describe('NSS admin key list states', () => {
	it('should show skeleton rows while the first page loads', async () => {
		routeFetch({ [`GET ${PATH}`]: () => new Promise<Response>(() => {}) as unknown as Response });
		render(NSSKeyAdminList);

		// LoadingBlock holds its bars back for 300ms, so a quick answer
		// never flickers.
		expect(
			await screen.findByTestId('nss-admin-keys-loading', {}, { timeout: 1000 })
		).toBeInTheDocument();
	});

	const emptyCases = [
		{
			name: 'there are no keys and the viewer may create them',
			user: { is_auditor: true, is_nss_key_admin: true },
			want: 'Create one with New key.'
		},
		{
			name: 'there are no keys and the viewer may not create them',
			user: { is_auditor: true },
			want: 'A fleet key admin creates keys.'
		}
	];
	for (const { name, user, want } of emptyCases) {
		it(`should say ${want} when ${name}`, async () => {
			session.user = userFixture(user);
			routeFetch({ [`GET ${PATH}`]: listPage([]) });
			render(NSSKeyAdminList);

			expect(await screen.findByTestId('nss-admin-keys-empty')).toHaveTextContent(want);
		});
	}

	it('should say why a host needs a key when there are no keys', async () => {
		routeFetch({ [`GET ${PATH}`]: listPage([]) });
		render(NSSKeyAdminList);

		expect(await screen.findByTestId('nss-admin-keys-empty')).toHaveTextContent(
			'A host needs a fleet key to look up accounts and groups here'
		);
	});

	it('should name the search and offer Clear filters when nothing matches', async () => {
		resetFakePage(`http://localhost${LIST}?q=nobody`);
		routeFetch({ [`GET ${PATH}`]: listPage([]) });
		render(NSSKeyAdminList);

		const empty = await screen.findByTestId('nss-admin-keys-empty-filtered');
		expect([
			empty.textContent?.includes('No keys match “nobody”'),
			within(empty).queryByText('Clear filters') !== null
		]).toEqual([true, true]);
	});

	it('should say what an address search covers when no key was used from it', async () => {
		resetFakePage(`http://localhost${LIST}?q=10.0.4.7`);
		routeFetch({ [`GET ${PATH}`]: listPage([], { address_search: '10.0.4.7' }) });
		render(NSSKeyAdminList);

		const empty = await screen.findByTestId('nss-admin-keys-empty-address');
		expect(empty.textContent?.replace(/\s+/g, ' ')).toContain(
			'No key was used from 10.0.4.7 in the last 90 days, the usage the server keeps. The search matches the address the server observed for each lookup: for a host behind a reverse proxy listed in http.trusted_proxies, the forwarded address'
		);
	});

	it('should name the other filters when an address search with them finds nothing', async () => {
		resetFakePage(`http://localhost${LIST}?q=10.0.4.7&status=revoked`);
		routeFetch({ [`GET ${PATH}`]: listPage([], { address_search: '10.0.4.7' }) });
		render(NSSKeyAdminList);

		expect(await screen.findByTestId('nss-admin-keys-empty-address')).toHaveTextContent(
			'No key used from 10.0.4.7 matches these filters'
		);
	});

	it('should offer Try again when the list fails to load', async () => {
		routeFetch({ [`GET ${PATH}`]: envelope(null, 500, 'database unavailable') });
		render(NSSKeyAdminList);

		expect(await screen.findByTestId('nss-admin-keys-error-retry')).toBeInTheDocument();
	});

	it('should load the list when Try again is pressed after a failure', async () => {
		let fail = true;
		routeFetch({
			[`GET ${PATH}`]: () => (fail ? envelope(null, 500, 'boom') : listPage([adminKey('a')]))
		});
		render(NSSKeyAdminList);
		const retry = await screen.findByTestId('nss-admin-keys-error-retry');
		fail = false;
		await userEvent.click(retry);

		expect(await screen.findByTestId('nss-admin-keys-table')).toBeInTheDocument();
	});

	it('should say NSS is off without asking the server when the session says so', async () => {
		session.user = userFixture({ is_auditor: true, nss_enabled: false });
		const calls = routeFetch({});
		render(NSSKeyAdminList);
		await settle();

		expect([
			calls.length,
			screen.getByTestId('nss-admin-keys-off').textContent?.includes('nss.enabled')
		]).toEqual([0, true]);
	});

	it('should say NSS is off when the server answers 404', async () => {
		routeFetch({
			[`GET ${PATH}`]: envelope(null, 404, 'the NSS directory is not enabled on this server')
		});
		render(NSSKeyAdminList);

		expect(await screen.findByTestId('nss-admin-keys-off')).toBeInTheDocument();
	});

	it('should append the next page when Load more is pressed', async () => {
		routeFetch({
			[`GET ${PATH}`]: (call) =>
				call.url.includes('after=')
					? listPage([adminKey('c')], { total: 3 })
					: listPage([adminKey('a'), adminKey('b')], { next_cursor: 'nss_b0000000', total: 3 })
		});
		render(NSSKeyAdminList);
		await userEvent.click(await screen.findByTestId('nss-admin-keys-load-more'));

		await waitFor(() => expect(screen.getAllByTestId('nss-admin-keys-row')).toHaveLength(3));
	});

	it('should keep the loaded rows when Load more fails', async () => {
		routeFetch({
			[`GET ${PATH}`]: (call) =>
				call.url.includes('after=')
					? envelope(null, 500, 'boom')
					: listPage([adminKey('a'), adminKey('b')], { next_cursor: 'nss_b0000000', total: 3 })
		});
		render(NSSKeyAdminList);
		await userEvent.click(await screen.findByTestId('nss-admin-keys-load-more'));
		await screen.findByTestId('nss-admin-keys-more-error');

		expect(screen.getAllByTestId('nss-admin-keys-row')).toHaveLength(2);
	});

	it('should put focus on the count line when the last page is in', async () => {
		routeFetch({
			[`GET ${PATH}`]: (call) =>
				call.url.includes('after=')
					? listPage([adminKey('c')], { total: 3 })
					: listPage([adminKey('a'), adminKey('b')], { next_cursor: 'nss_b0000000', total: 3 })
		});
		render(NSSKeyAdminList);
		await userEvent.click(await screen.findByTestId('nss-admin-keys-load-more'));

		await waitFor(() => expect(screen.getByTestId('nss-admin-keys-count')).toHaveFocus());
	});
});
