import { render, screen, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { session } from '#lib/session.svelte.js';
import { envelope, keyFixture, routeFetch, userFixture } from '#lib/testing/nss.js';
import Page from './+page.svelte';

// Test methodology: route fetch to fixture keys and read the table the way
// an operator does: which keys, in what state, and whether they can make a
// new one. The clock is pinned so expiry and rotation read the same on
// every run.

vi.mock('$app/state', async () => {
	const { fakePage } = await import('#lib/testing/page.svelte.js');
	return { page: fakePage };
});
// The filters live in the address: a fake page for what it says, and a
// goto that records what the page writes back.
const { goto } = vi.hoisted(() => ({ goto: vi.fn() }));
vi.mock('$app/navigation', async () => {
	const { fakeAfterNavigate } = await import('#lib/testing/page.svelte.js');
	return { goto, afterNavigate: fakeAfterNavigate };
});

import { fakeGoto, resetFakePage } from '#lib/testing/page.svelte.js';

const now = new Date('2026-10-18T12:00:00Z');

beforeEach(() => {
	vi.useFakeTimers({ now, toFake: ['Date'] });
	resetFakePage('http://localhost/nss-keys');
	goto.mockReset();
	goto.mockImplementation(fakeGoto);
	session.user = userFixture();
	session.resolved = true;
});

afterEach(() => {
	vi.useRealTimers();
	vi.unstubAllGlobals();
	session.clear();
});

/** listOf answers GET /api/nss/keys with these keys. */
function listOf(keys = [keyFixture()], canCreate = false) {
	return routeFetch({ 'GET /api/nss/keys': envelope({ keys, can_create: canCreate }) });
}

/** rowNames reads the key names down the table. */
function rowNames(): string[] {
	return screen.getAllByTestId('nss-key-link').map((link) => link.textContent?.trim() ?? '');
}

describe('NSS keys list', () => {
	it('should list each key with its id when keys load', async () => {
		listOf();
		render(Page);

		const row = await screen.findByTestId('nss-key-row');
		expect(within(row).getByText('nss_k7m4qp2x')).toBeInTheDocument();
	});

	it('should link each key to its own page', async () => {
		listOf();
		render(Page);

		expect(await screen.findByTestId('nss-key-link')).toHaveAttribute(
			'href',
			'/nss-keys/nss_k7m4qp2x'
		);
	});

	const lastUsedCases: { name: string; lastUsed?: string; want: string }[] = [
		{ name: 'a host used the key two hours ago', lastUsed: '2026-10-18T10:00:00Z', want: '2h ago' },
		{
			name: 'a host used the key three days ago',
			lastUsed: '2026-10-15T11:00:00Z',
			want: '3d ago'
		},
		{ name: 'no host has used the key', want: 'Never' }
	];
	for (const { name, lastUsed, want } of lastUsedCases) {
		it(`should read ${want} in the last used column when ${name}`, async () => {
			listOf([keyFixture({ last_used_at: lastUsed })]);
			render(Page);

			expect(await screen.findByTestId('nss-key-last-used')).toHaveTextContent(want);
		});
	}

	it('should give the key link the full target height when keys load', async () => {
		listOf();
		render(Page);

		expect(await screen.findByTestId('nss-key-link')).toHaveClass('min-h-target');
	});

	it('should offer one New key when there are no keys and the caller may create them', async () => {
		listOf([], true);
		render(Page);
		await screen.findByTestId('nss-keys-empty');

		expect(screen.getAllByRole('link', { name: 'New key' })).toHaveLength(1);
	});

	it('should offer New key when the caller may create keys', async () => {
		listOf([keyFixture()], true);
		render(Page);

		expect(await screen.findByTestId('nss-keys-new')).toBeInTheDocument();
	});

	it('should not offer New key when the caller may not create keys', async () => {
		listOf();
		render(Page);

		await screen.findByTestId('nss-key-row');
		expect(screen.queryByTestId('nss-keys-new')).not.toBeInTheDocument();
	});

	it('should teach how to start when no key is in the groups and the caller may create keys', async () => {
		listOf([], true);
		render(Page);

		expect(await screen.findByTestId('nss-keys-empty')).toHaveTextContent(
			'Create one key for each fleet of hosts'
		);
	});

	it('should say keys come through a group when no key is in the groups', async () => {
		listOf([], false);
		render(Page);

		expect(await screen.findByTestId('nss-keys-empty')).toHaveTextContent(
			'No NSS keys in your groups'
		);
	});

	it('should say who assigns keys when no key is in the groups and the caller holds no role', async () => {
		listOf([], false);
		render(Page);

		expect(await screen.findByTestId('nss-keys-empty')).toHaveTextContent(
			'An administrator assigns a key to a group.'
		);
	});

	it('should point an auditor at every key when no key is in their groups', async () => {
		session.user = userFixture({ is_auditor: true });
		listOf([], false);
		render(Page);
		await screen.findByTestId('nss-keys-empty');

		expect(screen.getByRole('link', { name: 'All NSS keys' })).toHaveAttribute(
			'href',
			'/admin/nss/keys'
		);
	});

	it('should offer Try again when the list fails to load', async () => {
		routeFetch({ 'GET /api/nss/keys': envelope(null, 500, 'database unavailable') });
		render(Page);

		expect(await screen.findByTestId('nss-keys-error-retry')).toBeInTheDocument();
	});

	it('should say NSS is off when the server answers nss_disabled', async () => {
		routeFetch({ 'GET /api/nss/keys': envelope(null, 404, 'nss_disabled') });
		render(Page);

		expect(await screen.findByTestId('nss-keys-disabled')).toBeInTheDocument();
	});

	describe('status', () => {
		const cases: { name: string; key: ReturnType<typeof keyFixture>; want: string }[] = [
			{ name: 'active', key: keyFixture(), want: 'active' },
			{
				name: 'rotating',
				key: keyFixture({ previous_expires_at: '2026-10-25T12:00:00Z' }),
				want: 'rotating'
			},
			{ name: 'expired', key: keyFixture({ status: 'expired' }), want: 'expired' },
			{ name: 'revoked', key: keyFixture({ status: 'revoked' }), want: 'revoked' }
		];

		for (const { name, key, want } of cases) {
			it(`should badge the key ${want} when it is ${name}`, async () => {
				listOf([key]);
				render(Page);

				expect(await screen.findByTestId('nss-key-status')).toHaveTextContent(want);
			});
		}
	});

	describe('expiry', () => {
		const cases: { name: string; expires: string; tone: string }[] = [
			{ name: 'past', expires: '2026-10-01T12:00:00Z', tone: 'text-danger' },
			{ name: 'within 30 days', expires: '2026-11-01T12:00:00Z', tone: 'text-trimmed' },
			{ name: 'far off', expires: '2027-06-01T12:00:00Z', tone: 'text-ink' }
		];

		for (const { name, expires, tone } of cases) {
			it(`should colour the expiry ${tone} when it is ${name}`, async () => {
				listOf([keyFixture({ expires_at: expires })]);
				render(Page);

				expect(await screen.findByTestId('nss-key-expiry')).toHaveClass(tone);
			});
		}

		it('should say never when the key never expires', async () => {
			listOf([keyFixture({ expires_at: undefined })]);
			render(Page);

			expect(await screen.findByTestId('nss-key-expiry')).toHaveTextContent('never');
		});

		it('should say expired in words when the expiry has passed', async () => {
			listOf([keyFixture({ expires_at: '2026-10-01T12:00:00Z' })]);
			render(Page);

			expect(await screen.findByTestId('nss-key-expiry')).toHaveTextContent('expired');
		});
	});

	describe('filters and sort', () => {
		const keys = [
			keyFixture({ id: 'nss_a', name: 'alpha', expires_at: '2027-05-01T00:00:00Z' }),
			keyFixture({
				id: 'nss_b',
				name: 'bravo',
				group_name: 'db-admins',
				expires_at: '2026-11-01T00:00:00Z'
			}),
			keyFixture({
				id: 'nss_c',
				name: 'charlie',
				status: 'revoked',
				group_name: '',
				expires_at: '2027-01-01T00:00:00Z'
			})
		];

		it('should list keys by name when nothing is chosen', async () => {
			listOf(keys);
			render(Page);
			await screen.findAllByTestId('nss-key-row');

			expect(rowNames()).toEqual(['alpha', 'bravo', 'charlie']);
		});

		it('should list the soonest expiry first when sorted by expiry', async () => {
			listOf(keys);
			render(Page);
			await userEvent.click(await screen.findByTestId('nss-keys-sort-expiry'));

			expect(rowNames()).toEqual(['bravo', 'alpha', 'charlie']);
		});

		it('should list a key that never expires after the live keys that do when sorted by expiry', async () => {
			listOf([keyFixture({ id: 'nss_d', name: 'delta', expires_at: undefined }), ...keys]);
			render(Page);
			await userEvent.click(await screen.findByTestId('nss-keys-sort-expiry'));

			expect(rowNames()).toEqual(['bravo', 'alpha', 'delta', 'charlie']);
		});

		it('should keep only revoked keys when the revoked filter is chosen', async () => {
			listOf(keys);
			render(Page);
			await userEvent.click(await screen.findByTestId('nss-keys-status-filter-revoked'));

			expect(rowNames()).toEqual(['charlie']);
		});

		it('should keep only one group when a group is chosen', async () => {
			listOf(keys);
			render(Page);
			await userEvent.selectOptions(
				await screen.findByTestId('nss-keys-group-filter'),
				'db-admins'
			);

			expect(rowNames()).toEqual(['bravo']);
		});

		const addressCases: { name: string; href: string; want: string[] }[] = [
			{ name: 'the address names the revoked filter', href: '?status=revoked', want: ['charlie'] },
			{ name: 'the address names a group', href: '?group=db-admins', want: ['bravo'] },
			{ name: 'the address names keys with no group', href: '?no_group=1', want: ['charlie'] },
			{
				name: 'the address names the expiry sort',
				href: '?sort=expiry',
				want: ['bravo', 'alpha', 'charlie']
			},
			{
				name: 'the address names an unknown status',
				href: '?status=lost',
				want: ['alpha', 'bravo', 'charlie']
			}
		];
		for (const { name, href, want } of addressCases) {
			it(`should list ${want.join(', ')} when ${name}`, async () => {
				resetFakePage(`http://localhost/nss-keys${href}`);
				listOf(keys);
				render(Page);
				await screen.findAllByTestId('nss-key-row');

				expect(rowNames()).toEqual(want);
			});
		}

		it('should write the chosen filter into the address when a filter is chosen', async () => {
			listOf(keys);
			render(Page);
			await userEvent.click(await screen.findByTestId('nss-keys-status-filter-revoked'));

			expect(goto).toHaveBeenLastCalledWith(
				'/nss-keys?status=revoked',
				expect.objectContaining({ shallow: true, replace: true })
			);
		});

		it('should carry the filters in the key link when a filter is chosen', async () => {
			listOf(keys);
			render(Page);
			await userEvent.click(await screen.findByTestId('nss-keys-status-filter-revoked'));

			expect(screen.getByTestId('nss-key-link')).toHaveAttribute(
				'href',
				`/nss-keys/nss_c?back=${encodeURIComponent('?status=revoked')}`
			);
		});

		it('should offer Clear filters when the filters exclude every key', async () => {
			listOf([keys[0]]);
			render(Page);
			await userEvent.click(await screen.findByTestId('nss-keys-status-filter-revoked'));

			expect(screen.getByTestId('nss-keys-filtered')).toHaveTextContent('Clear filters');
		});
	});
});
