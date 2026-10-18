import { render, screen, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { NSSKeyPermission } from '#lib/api/generated/enums.js';
import type { NSSKey } from '#lib/api/types.js';
import { session } from '#lib/session.svelte.js';
import {
	envelope,
	keyFixture,
	reportFixture,
	reportRow,
	routeFetch,
	settle,
	userFixture,
	type Route
} from '#lib/testing/nss.js';

// Test methodology: route fetch to one fixture key and its usage, then
// press what the page offers. The pinned behaviours are the ones that cost
// a fleet: actions appear exactly as the key's permissions allow, revoking
// needs the exact key id, clearing the previous secret says how many hosts
// would break, and a rotated secret is gone from the page once dismissed.

const { guards } = vi.hoisted(() => ({
	guards: [] as Array<(navigation: { cancel: () => void; type: string }) => void>
}));

vi.mock('$app/navigation', () => ({
	goto: vi.fn(() => Promise.resolve()),
	beforeNavigate: (callback: (navigation: { cancel: () => void; type: string }) => void) => {
		guards.push(callback);
	}
}));
vi.mock('$app/state', async () => {
	const { fakePage } = await import('#lib/testing/page.svelte.js');
	return { page: fakePage };
});

import { fakePage } from '#lib/testing/page.svelte.js';
import Page from './+page.svelte';

const now = new Date('2026-10-18T12:00:00Z');
const ALL: NSSKeyPermission[] = ['view', 'rotate', 'revoke', 'edit', 'assign_group'];
const SECRET = 'bmV3LXNlY3JldA';

beforeEach(() => {
	vi.useFakeTimers({ now, toFake: ['Date'] });
	session.user = userFixture();
	session.resolved = true;
	fakePage.params = { id: 'nss_k7m4qp2x' };
	guards.length = 0;
});

afterEach(() => {
	vi.useRealTimers();
	vi.unstubAllGlobals();
	session.clear();
});

const emptyUsage = envelope({ rows: [], total: 0 });

/** serve routes the key, its usage and any extra routes a test needs. */
function serve(key: NSSKey, extra: Record<string, Route | Response> = {}) {
	return routeFetch({
		'GET /api/nss/keys/nss_k7m4qp2x': envelope(key),
		'GET /api/nss/keys/nss_k7m4qp2x/usage': emptyUsage,
		...extra
	});
}

/** loaded renders the page and waits for the key. */
async function loaded() {
	render(Page);
	await screen.findByTestId('nss-key-identity');
}

describe('NSS key detail', () => {
	it('should say the key is not shared when the server answers 404', async () => {
		routeFetch({
			'GET /api/nss/keys/nss_k7m4qp2x': envelope(null, 404, 'not found'),
			'GET /api/nss/keys/nss_k7m4qp2x/usage': envelope(null, 404, 'not found')
		});
		render(Page);

		expect(await screen.findByTestId('nss-key-missing')).toBeInTheDocument();
	});

	it('should offer Try again when the key fails to load', async () => {
		routeFetch({
			'GET /api/nss/keys/nss_k7m4qp2x': envelope(null, 500, 'database unavailable'),
			'GET /api/nss/keys/nss_k7m4qp2x/usage': emptyUsage
		});
		render(Page);

		expect(await screen.findByTestId('nss-key-error-retry')).toBeInTheDocument();
	});

	describe('actions follow permissions', () => {
		const controls = [
			'nss-key-revoke',
			'nss-key-rotate',
			'nss-key-clear-previous',
			'nss-key-edit-description',
			'nss-key-edit-expires',
			'nss-key-edit-email',
			'nss-key-edit-group'
		];
		const rotating = { previous_expires_at: '2026-11-01T12:00:00Z' };
		const cases: { name: string; key: NSSKey; shown: string[] }[] = [
			{ name: 'view alone (an auditor)', key: keyFixture(rotating), shown: [] },
			{
				name: 'a key group member',
				key: keyFixture({ ...rotating, permissions: ['view', 'rotate', 'revoke', 'edit'] }),
				shown: [
					'nss-key-revoke',
					'nss-key-rotate',
					'nss-key-clear-previous',
					'nss-key-edit-description',
					'nss-key-edit-expires',
					'nss-key-edit-email'
				]
			},
			{
				name: 'SOC, who may revoke only',
				key: keyFixture({ ...rotating, permissions: ['view', 'revoke'] }),
				shown: ['nss-key-revoke']
			},
			{
				name: 'every permission',
				key: keyFixture({ ...rotating, permissions: ALL }),
				shown: controls
			},
			{
				name: 'every permission on a key never rotated',
				key: keyFixture({ permissions: ALL }),
				shown: controls.filter((control) => control !== 'nss-key-clear-previous')
			},
			{
				name: 'a revoked key',
				key: keyFixture({ ...rotating, status: 'revoked', permissions: ALL }),
				shown: []
			}
		];

		for (const { name, key, shown } of cases) {
			it(`should offer exactly what the server allows when the caller has ${name}`, async () => {
				serve(key);
				await loaded();

				expect(controls.filter((control) => screen.queryByTestId(control))).toEqual(shown);
			});
		}

		it('should say the key is read-only when the caller may only view it', async () => {
			serve(keyFixture());
			await loaded();

			expect(screen.getByTestId('nss-key-readonly')).toBeInTheDocument();
		});
	});

	describe('revoke', () => {
		/** openRevoke loads a revocable key and opens the dialog. */
		async function openRevoke() {
			const calls = serve(keyFixture({ permissions: ALL }), {
				'POST /api/nss/keys/nss_k7m4qp2x/revoke': envelope(
					keyFixture({ status: 'revoked', permissions: ['view'] })
				)
			});
			await loaded();
			await userEvent.click(screen.getByTestId('nss-key-revoke'));
			return calls;
		}

		it('should hold Revoke key until the key id is typed', async () => {
			await openRevoke();

			expect(screen.getByTestId('nss-key-revoke-confirm')).toBeDisabled();
		});

		it('should hold Revoke key when the key name is typed instead of the id', async () => {
			await openRevoke();
			await userEvent.type(screen.getByTestId('nss-key-revoke-typed'), 'web fleet');

			expect(screen.getByTestId('nss-key-revoke-confirm')).toBeDisabled();
		});

		it('should revoke when the exact key id is typed and confirmed', async () => {
			const calls = await openRevoke();
			await userEvent.type(screen.getByTestId('nss-key-revoke-typed'), 'nss_k7m4qp2x');
			await userEvent.click(screen.getByTestId('nss-key-revoke-confirm'));

			expect(calls.some((call) => call.url.endsWith('/revoke'))).toBe(true);
		});

		it('should show the key as revoked once the revoke lands', async () => {
			await openRevoke();
			await userEvent.type(screen.getByTestId('nss-key-revoke-typed'), 'nss_k7m4qp2x');
			await userEvent.click(screen.getByTestId('nss-key-revoke-confirm'));

			expect(await screen.findByTestId('nss-key-revoked')).toBeInTheDocument();
		});
	});

	describe('rotate', () => {
		const rotated = keyFixture({
			permissions: ALL,
			current_generation: 2,
			rotated_at: '2026-10-18T12:00:00Z',
			previous_expires_at: '2026-11-17T12:00:00Z'
		});

		/** rotateOnce loads a rotatable key, rotates it and waits for the secret. */
		async function rotateOnce() {
			serve(keyFixture({ permissions: ALL }), {
				'POST /api/nss/keys/nss_k7m4qp2x/rotate': envelope({ key: rotated, secret: SECRET })
			});
			const view = render(Page);
			await screen.findByTestId('nss-key-identity');
			await userEvent.click(screen.getByTestId('nss-key-rotate'));
			await userEvent.click(screen.getByTestId('nss-key-rotate-confirm'));
			await screen.findByTestId('secret-panel');
			return view;
		}

		it('should show the new secret once the rotation lands', async () => {
			await rotateOnce();

			expect(screen.getByTestId('secret-panel-secret')).toHaveTextContent(SECRET);
		});

		it('should say when the previous secret stops when the rotation lands', async () => {
			await rotateOnce();

			expect(screen.getByTestId('secret-panel-grace')).toBeInTheDocument();
		});

		it('should hold the secret nowhere on the page once it is dismissed', async () => {
			const { container } = await rotateOnce();
			await userEvent.click(screen.getByLabelText('I have stored this secret'));
			await userEvent.click(screen.getByTestId('secret-panel-done'));

			expect(container.innerHTML).not.toContain(SECRET);
		});

		it('should ask before leaving while the new secret is on screen', async () => {
			vi.stubGlobal('confirm', () => false);
			await rotateOnce();
			const cancel = vi.fn();
			guards.forEach((guard) => guard({ cancel, type: 'link' }));

			expect(cancel).toHaveBeenCalled();
		});

		it('should warn that a live previous secret stops at once when rotating again', async () => {
			serve(keyFixture({ permissions: ALL, previous_expires_at: '2026-11-01T12:00:00Z' }));
			await loaded();
			await userEvent.click(screen.getByTestId('nss-key-rotate'));

			expect(screen.getByTestId('nss-key-rotate-replaces')).toBeInTheDocument();
		});

		it('should show the server reason in the dialog when the rotation is refused', async () => {
			serve(keyFixture({ permissions: ALL }), {
				'POST /api/nss/keys/nss_k7m4qp2x/rotate': envelope(
					null,
					409,
					'the key was rotated meanwhile'
				)
			});
			await loaded();
			await userEvent.click(screen.getByTestId('nss-key-rotate'));
			await userEvent.click(screen.getByTestId('nss-key-rotate-confirm'));

			expect(await screen.findByText('the key was rotated meanwhile')).toBeInTheDocument();
		});
	});

	describe('clear the previous secret', () => {
		const key = keyFixture({ permissions: ALL, previous_expires_at: '2026-11-01T12:00:00Z' });

		it('should say how many hosts would start failing when some have not switched', async () => {
			const rows = [
				...Array.from({ length: 12 }, (_, i) => reportRow(`10.0.0.${i + 1}`, 'not_switched')),
				...Array.from({ length: 128 }, (_, i) => reportRow(`10.0.1.${i + 1}`, 'switched'))
			];
			serve(key, {
				'GET /api/nss/keys/nss_k7m4qp2x/rotation-report': envelope(reportFixture(rows))
			});
			await loaded();
			await userEvent.click(screen.getByTestId('nss-key-clear-previous'));
			await settle();

			expect(screen.getByTestId('nss-key-clear-count')).toHaveTextContent(
				'12 hosts still on the old secret will start failing. That is 12 of 140 hosts seen.'
			);
		});

		it('should say every host switched when none is left on the old secret', async () => {
			serve(key, {
				'GET /api/nss/keys/nss_k7m4qp2x/rotation-report': envelope(
					reportFixture([reportRow('10.0.0.1', 'switched')])
				)
			});
			await loaded();
			await userEvent.click(screen.getByTestId('nss-key-clear-previous'));
			await settle();

			expect(screen.getByTestId('nss-key-clear-count')).toHaveTextContent('Every host seen');
		});

		it('should say the count is unknown when the report fails', async () => {
			serve(key, {
				'GET /api/nss/keys/nss_k7m4qp2x/rotation-report': envelope(null, 500, 'boom')
			});
			await loaded();
			await userEvent.click(screen.getByTestId('nss-key-clear-previous'));
			await settle();

			expect(screen.getByTestId('nss-key-clear-count')).toHaveTextContent('Could not check');
		});

		it('should call DELETE on the previous secret when confirmed', async () => {
			const calls = serve(key, {
				'GET /api/nss/keys/nss_k7m4qp2x/rotation-report': envelope(reportFixture([])),
				'DELETE /api/nss/keys/nss_k7m4qp2x/previous': envelope(keyFixture({ permissions: ALL }))
			});
			await loaded();
			await userEvent.click(screen.getByTestId('nss-key-clear-previous'));
			await userEvent.click(screen.getByTestId('nss-key-clear-confirm'));

			expect(calls.some((call) => call.method === 'DELETE')).toBe(true);
		});
	});

	describe('inline edits', () => {
		it('should send only the edited field when a description is saved', async () => {
			const calls = serve(keyFixture({ permissions: ALL }), {
				'PATCH /api/nss/keys/nss_k7m4qp2x': envelope(
					keyFixture({ permissions: ALL, description: 'web and api tiers' })
				)
			});
			await loaded();
			await userEvent.click(screen.getByTestId('nss-key-edit-description'));
			const input = screen.getByTestId('nss-key-input-description');
			await userEvent.clear(input);
			await userEvent.type(input, 'web and api tiers');
			await userEvent.click(screen.getByTestId('nss-key-save-description'));

			expect(calls.find((call) => call.method === 'PATCH')?.body).toEqual({
				description: 'web and api tiers'
			});
		});

		it('should show the server reason beside the field when a save is refused', async () => {
			serve(keyFixture({ permissions: ALL }), {
				'PATCH /api/nss/keys/nss_k7m4qp2x': envelope(
					null,
					400,
					'notification_email is not an address'
				)
			});
			await loaded();
			await userEvent.click(screen.getByTestId('nss-key-edit-email'));
			await userEvent.click(screen.getByTestId('nss-key-save-email'));

			expect(await screen.findByTestId('nss-key-edit-error')).toHaveTextContent(
				'notification_email is not an address'
			);
		});
	});

	describe('usage', () => {
		const row = {
			source_ip: '10.0.0.7',
			generation: 1,
			day: '2026-10-17T00:00:00Z',
			lookups: 200,
			misses: 50,
			first_seen: '2026-10-17T01:00:00Z',
			last_seen: '2026-10-17T23:00:00Z'
		};

		it('should list one row per address, generation and day', async () => {
			serve(keyFixture(), {
				'GET /api/nss/keys/nss_k7m4qp2x/usage': envelope({ rows: [row], total: 1 })
			});
			await loaded();

			const usage = await screen.findByTestId('nss-key-usage-row');
			expect(within(usage).getByText('10.0.0.7')).toBeInTheDocument();
		});

		it('should show the miss rate beside the misses', async () => {
			serve(keyFixture(), {
				'GET /api/nss/keys/nss_k7m4qp2x/usage': envelope({ rows: [row], total: 1 })
			});
			await loaded();

			expect(await screen.findByTestId('nss-key-usage-misses')).toHaveTextContent('50 (25%)');
		});

		it('should offer Load more while the server holds more rows', async () => {
			serve(keyFixture(), {
				'GET /api/nss/keys/nss_k7m4qp2x/usage': envelope({
					rows: [row],
					total: 30,
					next_cursor: 'c2'
				})
			});
			await loaded();

			expect(await screen.findByTestId('nss-key-usage-load-more')).toBeInTheDocument();
		});

		it('should say there are no lookups yet when the key has none', async () => {
			serve(keyFixture());
			await loaded();

			expect(await screen.findByTestId('nss-key-usage-empty')).toBeInTheDocument();
		});

		it('should keep the key on screen when only the usage fails', async () => {
			serve(keyFixture(), {
				'GET /api/nss/keys/nss_k7m4qp2x/usage': envelope(null, 500, 'boom')
			});
			await loaded();
			await screen.findByTestId('nss-key-usage-error');

			expect(screen.getByTestId('nss-key-name')).toHaveTextContent('web fleet');
		});
	});
});
