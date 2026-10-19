import { render, screen, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type {
	LDAPStatusResponse,
	NSSCollision,
	NSSCollisionEntry,
	NSSPendingIDChangesResponse
} from '#lib/api/types.js';
import { session } from '#lib/session.svelte.js';
import { envelope, routeFetch, settle, userFixture, type Route } from '#lib/testing/nss.js';
import Page from './+page.svelte';

// Test methodology: the Directory page with NSS on, its LDAP status
// answered and the two NSS reads routed to fixtures. What is pinned is the
// part that can re-own a host's files: accepting a change or purging a
// tombstone needs the exact name, a refused accept is shown on its own row,
// a live-versus-live collision offers nothing to press, and with NSS off
// neither panel appears.

const status: LDAPStatusResponse = {
	enabled: true,
	url: 'ldaps://dir.corp.example',
	base_dn: 'dc=corp,dc=example',
	user_filter: '(uid={{.Username}})',
	configured_attributes: [],
	sync_interval_seconds: 900,
	disable_after_seconds: 2700,
	reenable: true,
	tls_insecure_skip_verify: false,
	running: false
};

const pending: NSSPendingIDChangesResponse = {
	users: [
		{
			name: 'alice',
			dn: 'uid=alice,ou=People,dc=corp,dc=example',
			uid: 10001,
			gid: 5000,
			pending_uid: 20001,
			pending_since: '2026-10-15T12:00:00Z'
		}
	],
	groups: [
		{
			name: 'ops',
			dn: 'cn=ops,ou=Groups,dc=corp,dc=example',
			gid: 5100,
			pending_gid: 5200,
			pending_since: '2026-10-16T12:00:00Z'
		}
	]
};

/** side is one entry of a collision. */
function side(name: string, overrides: Partial<NSSCollisionEntry> = {}): NSSCollisionEntry {
	return {
		name,
		dn: `uid=${name},ou=People,dc=corp,dc=example`,
		number: 10001,
		stored: true,
		tombstone: false,
		...overrides
	};
}

const tombstonePair: NSSCollision = {
	kind: 'uid',
	value: '10001',
	holder: side('carol', { tombstone: true, deleted_at: '2026-10-01T00:00:00Z' }),
	other: side('dave', { stored: false }),
	first_seen_at: '2026-10-12T00:00:00Z',
	last_seen_at: '2026-10-18T00:00:00Z'
};

const livePair: NSSCollision = {
	kind: 'user_name',
	value: 'erin',
	holder: side('erin', { number: 10010 }),
	other: side('erin', {
		number: 10011,
		stored: false,
		dn: 'uid=erin,ou=Contractors,dc=corp,dc=example'
	}),
	first_seen_at: '2026-10-12T00:00:00Z',
	last_seen_at: '2026-10-18T00:00:00Z'
};

/** serve answers the page's reads and any action routes a test adds. */
function serve(
	extra: Record<string, Route | Response> = {},
	collisions: NSSCollision[] = [tombstonePair, livePair]
) {
	return routeFetch({
		'GET /api/admin/ldap/status': envelope(status),
		'GET /api/admin/nss/pending-id-changes': envelope(pending),
		'GET /api/admin/nss/collisions': envelope({ collisions }),
		'GET /api/admin/nss/users': envelope({ accounts: [], total: 1234 }),
		'GET /api/admin/nss/groups': envelope({ groups: [], total: 1 }),
		...extra
	});
}

beforeEach(() => {
	session.user = userFixture({
		is_admin: true,
		is_soc: true,
		is_auditor: true,
		is_nss_key_admin: true
	});
	session.resolved = true;
});

afterEach(() => {
	vi.unstubAllGlobals();
	session.clear();
});

/** pendingRow finds the pending row naming `name`. */
async function pendingRow(name: string): Promise<HTMLElement> {
	const rows = await screen.findAllByTestId('nss-pending-row');
	const row = rows.find((candidate) => within(candidate).queryByText(name));
	if (!row) throw new Error(`no pending row for ${name}`);
	return row;
}

/** collisionRow finds the collision row whose holder is `name`. */
async function collisionRow(name: string): Promise<HTMLElement> {
	const rows = await screen.findAllByTestId('nss-collision-row');
	const row = rows.find((candidate) => within(candidate).queryAllByText(name).length > 0);
	if (!row) throw new Error(`no collision row for ${name}`);
	return row;
}

describe('Directory page NSS panels', () => {
	it('should hide both panels when NSS is off', async () => {
		session.user = userFixture({
			is_admin: true,
			is_soc: true,
			is_auditor: true,
			nss_enabled: false
		});
		const calls = serve();
		render(Page);
		await screen.findByTestId('ldap-sync-card');

		expect(calls.some((call) => call.url.includes('/admin/nss/'))).toBe(false);
	});

	it('should not render the pending panel when NSS is off', async () => {
		session.user = userFixture({
			is_admin: true,
			is_soc: true,
			is_auditor: true,
			nss_enabled: false
		});
		serve();
		render(Page);
		await screen.findByTestId('ldap-sync-card');

		expect(screen.queryByTestId('nss-pending')).not.toBeInTheDocument();
	});

	it('should link to the NSS keys page from the heading when NSS is on', async () => {
		serve();
		render(Page);

		expect(await screen.findByTestId('directory-nss-keys-link')).toHaveAttribute(
			'href',
			'/nss-keys'
		);
	});

	describe('pending id changes', () => {
		it('should list the stored and pending numbers for each entry', async () => {
			serve();
			render(Page);

			expect(await pendingRow('alice')).toHaveTextContent('uid 10001 to 20001');
		});

		it('should list a group change beside the account changes', async () => {
			serve();
			render(Page);

			expect(await pendingRow('ops')).toHaveTextContent('gid 5100 to 5200');
		});

		it('should say none for a stored gid the server did not send', async () => {
			serve({
				'GET /api/admin/nss/pending-id-changes': envelope({
					users: [
						{
							name: 'sv-build',
							dn: 'uid=sv-build,ou=People,dc=corp,dc=example',
							uid: 9001,
							pending_gid: 9001,
							pending_since: '2026-10-15T12:00:00Z'
						}
					],
					groups: []
				})
			});
			render(Page);

			expect(await pendingRow('sv-build')).toHaveTextContent('gid none to 9001');
		});

		it('should say there is nothing pending when the list is empty', async () => {
			routeFetch({
				'GET /api/admin/ldap/status': envelope(status),
				'GET /api/admin/nss/pending-id-changes': envelope({ users: [], groups: [] }),
				'GET /api/admin/nss/collisions': envelope({ collisions: [] })
			});
			render(Page);

			expect(await screen.findByTestId('nss-pending-empty')).toHaveTextContent(
				'No pending changes'
			);
		});

		it('should offer Try again when the pending list fails to load', async () => {
			serve({ 'GET /api/admin/nss/pending-id-changes': envelope(null, 500, 'boom') });
			render(Page);

			expect(await screen.findByTestId('nss-pending-error-retry')).toBeInTheDocument();
		});

		/** openAccept opens the dialog for alice's row. */
		async function openAccept() {
			render(Page);
			await userEvent.click(within(await pendingRow('alice')).getByTestId('nss-pending-accept'));
		}

		it('should hold Accept change until the name is typed', async () => {
			serve();
			await openAccept();

			expect(screen.getByTestId('nss-accept-confirm')).toBeDisabled();
		});

		it('should hold Accept change when a different name is typed', async () => {
			serve();
			await openAccept();
			await userEvent.type(screen.getByTestId('nss-accept-typed'), 'ops');

			expect(screen.getByTestId('nss-accept-confirm')).toBeDisabled();
		});

		it('should warn that files owned by the old number are not moved', async () => {
			serve();
			await openAccept();

			expect(screen.getByRole('dialog')).toHaveTextContent(
				'Files owned by the old number are not moved'
			);
		});

		it('should accept by the stored uid when the exact name is typed', async () => {
			const calls = serve({
				'POST /api/admin/nss/users/10001/accept-id-change': envelope({
					name: 'alice',
					dn: 'uid=alice,ou=People,dc=corp,dc=example',
					uid: 20001,
					gid: 5000
				})
			});
			await openAccept();
			await userEvent.type(screen.getByTestId('nss-accept-typed'), 'alice');
			await userEvent.click(screen.getByTestId('nss-accept-confirm'));

			expect(calls.some((call) => call.url === '/api/admin/nss/users/10001/accept-id-change')).toBe(
				true
			);
		});

		it('should show a 409 on the row it came from', async () => {
			serve({
				'POST /api/admin/nss/users/10001/accept-id-change': envelope(
					null,
					409,
					'uid 20001 is held by bob'
				)
			});
			await openAccept();
			await userEvent.type(screen.getByTestId('nss-accept-typed'), 'alice');
			await userEvent.click(screen.getByTestId('nss-accept-confirm'));
			await settle();

			expect(
				within(await pendingRow('alice')).getByTestId('nss-pending-row-error')
			).toHaveTextContent('uid 20001 is held by bob');
		});

		it('should accept a group by its stored gid', async () => {
			const calls = serve({
				'POST /api/admin/nss/groups/5100/accept-id-change': envelope({
					name: 'ops',
					dn: 'cn=ops,ou=Groups,dc=corp,dc=example',
					gid: 5200
				})
			});
			render(Page);
			await userEvent.click(within(await pendingRow('ops')).getByTestId('nss-pending-accept'));
			await userEvent.type(screen.getByTestId('nss-accept-typed'), 'ops');
			await userEvent.click(screen.getByTestId('nss-accept-confirm'));

			expect(calls.some((call) => call.url === '/api/admin/nss/groups/5100/accept-id-change')).toBe(
				true
			);
		});
	});

	describe('collisions', () => {
		it('should offer Purge tombstone on a row whose holder is a tombstone', async () => {
			serve();
			render(Page);

			expect(
				within(await collisionRow('carol')).queryByTestId('nss-collision-purge')
			).not.toBeNull();
		});

		it('should offer no button on a live pair', async () => {
			serve();
			render(Page);

			expect(within(await collisionRow('erin')).queryByTestId('nss-collision-purge')).toBeNull();
		});

		it('should point a live pair at the directory', async () => {
			serve();
			render(Page);

			expect(within(await collisionRow('erin')).getByTestId('nss-collision-fix')).toHaveTextContent(
				'Fix one entry in the directory'
			);
		});

		it('should mark the tombstone side', async () => {
			serve();
			render(Page);

			expect(
				within(await collisionRow('carol')).getByTestId('collision-holder-tombstone')
			).toBeInTheDocument();
		});

		it('should say there are no collisions when the list is empty', async () => {
			serve({}, []);
			render(Page);

			expect(await screen.findByTestId('nss-collisions-empty')).toBeInTheDocument();
		});

		/** openPurge opens the purge dialog for carol's tombstone. */
		async function openPurge() {
			render(Page);
			await userEvent.click(within(await collisionRow('carol')).getByTestId('nss-collision-purge'));
		}

		it('should hold Purge tombstone until the name is typed', async () => {
			serve();
			await openPurge();

			expect(screen.getByTestId('nss-purge-confirm')).toBeDisabled();
		});

		it('should hold Purge tombstone when the contending name is typed instead', async () => {
			serve();
			await openPurge();
			await userEvent.type(screen.getByTestId('nss-purge-typed'), 'dave');

			expect(screen.getByTestId('nss-purge-confirm')).toBeDisabled();
		});

		it('should purge the tombstone by its uid when the exact name is typed', async () => {
			const calls = serve({
				'DELETE /api/admin/nss/users/10001/tombstone': envelope({
					name: 'carol',
					dn: 'uid=carol,ou=People,dc=corp,dc=example',
					number: 10001
				})
			});
			await openPurge();
			await userEvent.type(screen.getByTestId('nss-purge-typed'), 'carol');
			await userEvent.click(screen.getByTestId('nss-purge-confirm'));

			expect(
				calls.some(
					(call) => call.method === 'DELETE' && call.url === '/api/admin/nss/users/10001/tombstone'
				)
			).toBe(true);
		});

		it('should purge a group tombstone through the groups route', async () => {
			const groupPair: NSSCollision = {
				...tombstonePair,
				kind: 'gid',
				value: '5100',
				holder: side('legacy', { number: 5100, tombstone: true }),
				other: side('ops', { number: 5100, stored: false })
			};
			const calls = serve(
				{
					'DELETE /api/admin/nss/groups/5100/tombstone': envelope({
						name: 'legacy',
						dn: 'cn=legacy',
						number: 5100
					})
				},
				[groupPair]
			);
			render(Page);
			await userEvent.click(
				within(await collisionRow('legacy')).getByTestId('nss-collision-purge')
			);
			await userEvent.type(screen.getByTestId('nss-purge-typed'), 'legacy');
			await userEvent.click(screen.getByTestId('nss-purge-confirm'));

			expect(calls.some((call) => call.url === '/api/admin/nss/groups/5100/tombstone')).toBe(true);
		});
	});

	describe('stored entries', () => {
		it('should count the stored accounts and groups when both lists answer', async () => {
			serve();
			render(Page);

			expect(await screen.findByTestId('nss-stored-counts')).toHaveTextContent(
				'1234 stored accounts and 1 stored group.'
			);
		});

		it('should read each count with a single row', async () => {
			const calls = serve();
			render(Page);
			await screen.findByTestId('nss-stored-counts');

			expect(calls.filter((call) => call.url.endsWith('?limit=1')).map((call) => call.url)).toEqual(
				['/api/admin/nss/users?limit=1', '/api/admin/nss/groups?limit=1']
			);
		});

		const links = [
			{ testid: 'nss-stored-accounts-link', href: '/admin/nss/accounts' },
			{ testid: 'nss-stored-groups-link', href: '/admin/nss/groups' }
		];

		for (const { testid, href } of links) {
			it(`should link to ${href} when the counts load`, async () => {
				serve();
				render(Page);

				expect(await screen.findByTestId(testid)).toHaveAttribute('href', href);
			});
		}

		it('should offer Try again when a count fails to load', async () => {
			serve({ 'GET /api/admin/nss/groups': envelope(null, 500, 'boom') });
			render(Page);

			expect(await screen.findByTestId('nss-stored-error-retry')).toBeInTheDocument();
		});

		it('should keep the pending panel when the counts fail to load', async () => {
			serve({ 'GET /api/admin/nss/users': envelope(null, 500, 'boom') });
			render(Page);
			await screen.findByTestId('nss-stored-error');

			expect(await pendingRow('alice')).toBeInTheDocument();
		});

		it('should keep the sync section when the counts fail to load', async () => {
			serve({ 'GET /api/admin/nss/users': envelope(null, 500, 'boom') });
			render(Page);
			await screen.findByTestId('nss-stored-error');

			expect(screen.getByTestId('ldap-sync-card')).toBeInTheDocument();
		});

		it('should count again when Try again is pressed', async () => {
			let fail = true;
			serve({
				'GET /api/admin/nss/users': () =>
					fail ? envelope(null, 500, 'boom') : envelope({ accounts: [], total: 7 })
			});
			render(Page);
			const retry = await screen.findByTestId('nss-stored-error-retry');
			fail = false;
			await userEvent.click(retry);

			expect(await screen.findByTestId('nss-stored-counts')).toHaveTextContent('7 stored accounts');
		});

		it('should not count anything when NSS is off', async () => {
			session.user = userFixture({
				is_admin: true,
				is_soc: true,
				is_auditor: true,
				nss_enabled: false
			});
			serve();
			render(Page);
			await screen.findByTestId('ldap-sync-card');

			expect(screen.queryByTestId('nss-stored')).toBeNull();
		});
	});

	describe('links into the viewer', () => {
		it('should link a pending account to its page by the stored uid', async () => {
			serve();
			render(Page);

			expect(within(await pendingRow('alice')).getByTestId('nss-pending-link')).toHaveAttribute(
				'href',
				'/admin/nss/accounts/10001'
			);
		});

		it('should link a pending group to its page by the stored gid', async () => {
			serve();
			render(Page);

			expect(within(await pendingRow('ops')).getByTestId('nss-pending-link')).toHaveAttribute(
				'href',
				'/admin/nss/groups/5100'
			);
		});

		it('should link a stored collision side to its account', async () => {
			serve();
			render(Page);

			expect(
				within(await collisionRow('carol')).getByTestId('collision-holder-link')
			).toHaveAttribute('href', '/admin/nss/accounts/10001');
		});

		it('should not link a collision side the sync refused to store', async () => {
			serve();
			render(Page);

			expect(within(await collisionRow('carol')).queryByTestId('collision-other-link')).toBeNull();
		});

		it('should link a stored group collision side to its group', async () => {
			const groupPair: NSSCollision = {
				...tombstonePair,
				kind: 'group_name',
				value: 'legacy',
				holder: side('legacy', { number: 5100 }),
				other: side('legacy', { number: 5101, stored: false })
			};
			serve({}, [groupPair]);
			render(Page);

			expect(
				within(await collisionRow('legacy')).getByTestId('collision-holder-link')
			).toHaveAttribute('href', '/admin/nss/groups/5100');
		});

		it('should bring the section a link names into view once the page has loaded', async () => {
			const scrolled = vi.fn();
			Element.prototype.scrollIntoView = scrolled;
			window.location.hash = '#collisions';
			try {
				serve();
				render(Page);
				await screen.findByTestId('nss-collisions');
				await settle();

				expect(scrolled.mock.contexts[0]).toBe(screen.getByTestId('nss-collisions'));
			} finally {
				window.location.hash = '';
				delete (Element.prototype as { scrollIntoView?: unknown }).scrollIntoView;
			}
		});
	});
});
