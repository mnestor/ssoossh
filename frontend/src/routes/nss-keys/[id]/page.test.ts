import { render, screen, waitFor, within } from '@testing-library/svelte';
import { tick } from 'svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { NSSKeyPermission } from '#lib/api/generated/enums.js';
import type { NSSKey } from '#lib/api/types.js';
import { formatDateTime } from '#lib/format.js';
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

import { fakePage, resetFakePage } from '#lib/testing/page.svelte.js';
import Page from './+page.svelte';

const now = new Date('2026-10-18T12:00:00Z');
const ALL: NSSKeyPermission[] = ['view', 'rotate', 'revoke', 'edit', 'assign_group'];
const SECRET = 'bmV3LXNlY3JldA';

beforeEach(() => {
	vi.useFakeTimers({ now, toFake: ['Date'] });
	session.user = userFixture();
	session.resolved = true;
	fakePage.params = { id: 'nss_k7m4qp2x' };
	resetFakePage('http://localhost/nss-keys/nss_k7m4qp2x');
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

	const backCases: { name: string; search: string; want: string }[] = [
		{ name: 'the list carried no filters', search: '', want: '/nss-keys' },
		{
			name: 'the list carried its filters',
			search: `?back=${encodeURIComponent('?status=revoked&sort=expiry')}`,
			want: '/nss-keys?status=revoked&sort=expiry'
		},
		{
			name: 'the carried value is not a query string',
			search: `?back=${encodeURIComponent('https://elsewhere.example/')}`,
			want: '/nss-keys'
		},
		{
			name: 'the carried value has a fragment',
			search: `?back=${encodeURIComponent('?status=revoked#x')}`,
			want: '/nss-keys'
		},
		{
			name: 'the key was opened from the admin list with its filters',
			search: `?from=admin&back=${encodeURIComponent('?q=10.0.4.0%2F24&unused=1')}`,
			want: '/admin/nss/keys?q=10.0.4.0%2F24&unused=1'
		},
		{
			name: 'the from value is not one the page knows',
			search: '?from=https://elsewhere.example/',
			want: '/nss-keys'
		}
	];
	for (const { name, search, want } of backCases) {
		it(`should point the back chip at ${want} when ${name}`, async () => {
			resetFakePage(`http://localhost/nss-keys/nss_k7m4qp2x${search}`);
			serve(keyFixture());
			await loaded();

			expect(screen.getByTestId('nss-key-back')).toHaveAttribute('href', want);
		});
	}

	// The rotation report hands the way back on to its own back chip, so it
	// ends on the list the key was opened from.
	const rotationCases = [
		{ name: 'it was opened from the keys a reader manages', search: '', want: '' },
		{
			name: 'it was opened from the admin list with its filters',
			search: `?from=admin&back=${encodeURIComponent('?unused=1')}`,
			want: `?from=admin&back=${encodeURIComponent('?unused=1')}`
		}
	];
	for (const { name, search, want } of rotationCases) {
		it(`should carry the way back to the rotation report when ${name}`, async () => {
			resetFakePage(`http://localhost/nss-keys/nss_k7m4qp2x${search}`);
			serve(keyFixture({ current_generation: 2, rotated_at: '2026-10-18T12:00:00Z' }));
			await loaded();

			expect(screen.getByTestId('nss-key-rotation-link')).toHaveAttribute(
				'href',
				`/nss-keys/nss_k7m4qp2x/rotation${want}`
			);
		});
	}

	const backLabels = [
		{ name: 'it was opened from the keys a reader manages', search: '', want: 'NSS keys' },
		{ name: 'it was opened from the admin list', search: '?from=admin', want: 'All NSS keys' }
	];
	for (const { name, search, want } of backLabels) {
		it(`should name the back chip ${want} when ${name}`, async () => {
			resetFakePage(`http://localhost/nss-keys/nss_k7m4qp2x${search}`);
			serve(keyFixture());
			await loaded();

			expect(screen.getByTestId('nss-key-back')).toHaveTextContent(want);
		});
	}

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

		it('should offer no expiry edit when the caller has every permission', async () => {
			serve(keyFixture({ permissions: ALL }));
			await loaded();

			expect(screen.queryByTestId('nss-key-edit-expires')).toBeNull();
		});

		it('should say never when the key never expires', async () => {
			serve(keyFixture({ expires_at: undefined }));
			await loaded();

			expect(screen.getByTestId('nss-key-expires')).toHaveTextContent('Never');
		});

		const renewLineCases: { name: string; key: NSSKey; shown: boolean }[] = [
			{
				name: 'the key expires within 30 days',
				key: keyFixture({ expires_at: '2026-11-01T12:00:00Z' }),
				shown: true
			},
			{
				name: 'the key has expired',
				key: keyFixture({ status: 'expired', expires_at: '2026-10-01T12:00:00Z' }),
				shown: true
			},
			{ name: 'the key expires in months', key: keyFixture(), shown: false },
			{
				name: 'the key is revoked',
				key: keyFixture({ status: 'revoked', expires_at: '2026-11-01T12:00:00Z' }),
				shown: false
			}
		];
		for (const { name, key, shown } of renewLineCases) {
			it(`should ${shown ? 'say' : 'not say'} that rotating renews the expiry when ${name}`, async () => {
				serve(key);
				await loaded();

				expect(screen.queryByTestId('nss-key-expires-renew') !== null).toBe(shown);
			});
		}

		it('should draw the warning glyph beside the expiry when it is within 30 days', async () => {
			serve(keyFixture({ expires_at: '2026-11-01T12:00:00Z' }));
			await loaded();

			expect(screen.getByTestId('nss-key-expires').querySelector('svg')).not.toBeNull();
		});

		const expiredCases: { name: string; key: NSSKey; shown: boolean }[] = [
			{
				name: 'the key has expired',
				key: keyFixture({ status: 'expired', expires_at: '2026-10-01T12:00:00Z' }),
				shown: true
			},
			{ name: 'the key is active', key: keyFixture(), shown: false },
			{
				name: 'the key is revoked after its expiry',
				key: keyFixture({ status: 'revoked', expires_at: '2026-10-01T12:00:00Z' }),
				shown: false
			}
		];
		for (const { name, key, shown } of expiredCases) {
			it(`should ${shown ? 'show' : 'not show'} the expired notice when ${name}`, async () => {
				serve(key);
				await loaded();

				expect(screen.queryByTestId('nss-key-expired') !== null).toBe(shown);
			});
		}

		it('should say rotating revives an expired key when it has expired', async () => {
			serve(keyFixture({ status: 'expired', expires_at: '2026-10-01T12:00:00Z' }));
			await loaded();

			expect(screen.getByTestId('nss-key-expired')).toHaveTextContent(
				'The server refuses it, so hosts using it serve only the accounts they already cached, until their cache_expire_offline runs out, and resolve no new ones. Rotating issues a new secret and renews the expiry; the old secret is not accepted again.'
			);
		});

		it('should say the key is read-only when the caller may only view it', async () => {
			serve(keyFixture());
			await loaded();

			expect(screen.getByTestId('nss-key-readonly')).toBeInTheDocument();
		});

		const revokeOnlyCases: { name: string; permissions: NSSKeyPermission[]; shown: boolean }[] = [
			{ name: 'the caller may only revoke (SOC)', permissions: ['view', 'revoke'], shown: true },
			{
				name: 'the caller may also rotate and edit',
				permissions: ['view', 'rotate', 'revoke', 'edit'],
				shown: false
			},
			{ name: 'the caller may only view', permissions: ['view'], shown: false }
		];
		for (const { name, permissions, shown } of revokeOnlyCases) {
			it(`should ${shown ? 'say' : 'not say'} who may rotate and edit when ${name}`, async () => {
				serve(keyFixture({ permissions }));
				await loaded();

				expect(screen.queryByTestId('nss-key-revoke-only') !== null).toBe(shown);
			});
		}

		it('should name the roles that may rotate and edit when the caller may only revoke', async () => {
			serve(keyFixture({ permissions: ['view', 'revoke'] }));
			await loaded();

			expect(screen.getByTestId('nss-key-revoke-only')).toHaveTextContent(
				'Rotating and editing it need the fleet key admin role or membership of its group.'
			);
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

		it('should put the caret on the revoked notice when the revoke lands', async () => {
			await openRevoke();
			await userEvent.type(screen.getByTestId('nss-key-revoke-typed'), 'nss_k7m4qp2x');
			await userEvent.click(screen.getByTestId('nss-key-revoke-confirm'));
			await waitFor(() =>
				expect(screen.queryByTestId('nss-key-revoke-confirm')).not.toBeInTheDocument()
			);
			await tick();

			expect(document.activeElement).toBe(screen.getByTestId('nss-key-revoked').parentElement);
		});

		it('should put the caret back on Revoke key when the dialog is cancelled', async () => {
			await openRevoke();
			await userEvent.click(screen.getByRole('button', { name: 'Cancel' }));
			await waitFor(() =>
				expect(screen.queryByTestId('nss-key-revoke-confirm')).not.toBeInTheDocument()
			);
			await tick();

			expect(document.activeElement).toBe(screen.getByTestId('nss-key-revoke'));
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

		it('should leave the caret on the new secret once the dialog has gone', async () => {
			await rotateOnce();
			await waitFor(() =>
				expect(screen.queryByTestId('nss-key-rotate-confirm')).not.toBeInTheDocument()
			);

			expect(document.activeElement).toBe(screen.getByTestId('secret-panel-focus'));
		});

		it('should scroll the new secret into view when the rotation lands', async () => {
			const scroll = vi.fn();
			Element.prototype.scrollIntoView = scroll;
			await rotateOnce();
			delete (Element.prototype as Partial<Element>).scrollIntoView;

			expect(scroll).toHaveBeenCalledWith({ block: 'start' });
		});

		it('should say the expiry the server set when the rotation lands', async () => {
			await rotateOnce();

			expect(screen.getByTestId('secret-panel-expiry')).toHaveTextContent(
				`The key now expires ${formatDateTime('2027-01-10T12:00:00Z')}.`
			);
		});

		it('should announce the new expiry when the rotation lands', async () => {
			await rotateOnce();

			expect(screen.getByTestId('nss-key-live')).toHaveTextContent(
				`Key rotated. Store the new secret. The key now expires ${formatDateTime('2027-01-10T12:00:00Z')}.`
			);
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

		it('should hide Rotate secret when a new secret is on screen', async () => {
			await rotateOnce();

			expect(screen.queryByTestId('nss-key-rotate')).not.toBeInTheDocument();
		});

		it('should say why Rotate secret is gone when a new secret is on screen', async () => {
			await rotateOnce();

			expect(screen.getByTestId('nss-key-rotate-held')).toHaveTextContent(
				'Rotate again once the new secret above is stored and you press Done.'
			);
		});

		it('should put the caret on Rotate secret when Done is pressed', async () => {
			await rotateOnce();
			await userEvent.click(screen.getByLabelText('I have stored this secret'));
			await userEvent.click(screen.getByTestId('secret-panel-done'));
			await tick();

			expect(document.activeElement).toBe(screen.getByTestId('nss-key-rotate'));
		});

		it('should ask before leaving while the new secret is on screen', async () => {
			vi.stubGlobal('confirm', () => false);
			await rotateOnce();
			const cancel = vi.fn();
			guards.forEach((guard) => guard({ cancel, type: 'link' }));

			expect(cancel).toHaveBeenCalled();
		});

		it('should let the reader leave without asking when the secret is ticked as stored', async () => {
			const ask = vi.fn(() => false);
			vi.stubGlobal('confirm', ask);
			await rotateOnce();
			await userEvent.click(screen.getByLabelText('I have stored this secret'));
			guards.forEach((guard) => guard({ cancel: vi.fn(), type: 'link' }));

			expect(ask).not.toHaveBeenCalled();
		});

		it('should warn that a live previous secret stops at once when rotating again', async () => {
			serve(keyFixture({ permissions: ALL, previous_expires_at: '2026-11-01T12:00:00Z' }));
			await loaded();
			await userEvent.click(screen.getByTestId('nss-key-rotate'));

			expect(screen.getByTestId('nss-key-rotate-replaces')).toBeInTheDocument();
		});

		const graceCases: { name: string; settings: Route | Response; want: string }[] = [
			{
				name: 'the server keeps a 30 day grace',
				settings: envelope({ max_lifetime_seconds: 31536000, rotation_grace_seconds: 2592000 }),
				want: `still accepted until ${formatDateTime('2026-11-17T12:00:00.000Z')} (1mo from now)`
			},
			{
				name: 'the server keeps no grace',
				settings: envelope({ max_lifetime_seconds: 31536000, rotation_grace_seconds: 0 }),
				want: 'The current secret stops working at once'
			},
			{
				name: 'the settings fail to load',
				settings: envelope(null, 500, 'database unavailable'),
				want: 'still accepted for the rotation grace period'
			}
		];
		for (const { name, settings, want } of graceCases) {
			it(`should say when the current secret stops before confirming when ${name}`, async () => {
				serve(keyFixture({ permissions: ALL }), { 'GET /api/nss/key-settings': settings });
				await loaded();
				await settle();
				await userEvent.click(screen.getByTestId('nss-key-rotate'));

				expect(screen.getByTestId('nss-key-rotate-grace')).toHaveTextContent(want);
			});
		}

		// The fixture key lived a year from 2026-01-10; the clock is 2026-10-18 12:00 UTC.
		const renewCases: { name: string; key: NSSKey; settings: Route | Response; want: string }[] = [
			{
				name: 'the server limits keys to a year',
				key: keyFixture({ permissions: ALL }),
				settings: envelope({ max_lifetime_seconds: 31536000, rotation_grace_seconds: 0 }),
				want: `renewed to ${formatDateTime('2027-10-18T12:00:00.000Z')}`
			},
			{
				name: 'the server limits keys to 30 days, earlier than the current expiry',
				key: keyFixture({ permissions: ALL }),
				settings: envelope({ max_lifetime_seconds: 2592000, rotation_grace_seconds: 0 }),
				want: `The key's expiry moves earlier, to ${formatDateTime('2026-11-17T12:00:00.000Z')}, because the server's limit is now 1mo.`
			},
			{
				name: 'the key never expired and the server now limits keys to a year',
				key: keyFixture({ permissions: ALL, expires_at: undefined }),
				settings: envelope({ max_lifetime_seconds: 31536000, rotation_grace_seconds: 0 }),
				want: `The key now gets an expiry: ${formatDateTime('2027-10-18T12:00:00.000Z')}. Until now it never expired.`
			},
			{
				name: 'the key never expires and the server sets no limit',
				key: keyFixture({ permissions: ALL, expires_at: undefined }),
				settings: envelope({ max_lifetime_seconds: 0, rotation_grace_seconds: 0 }),
				want: 'The key still never expires.'
			},
			{
				name: 'the settings fail to load',
				key: keyFixture({ permissions: ALL }),
				settings: envelope(null, 500, 'database unavailable'),
				want: "renewed by its lifetime, up to the server's limit"
			}
		];
		for (const { name, key, settings, want } of renewCases) {
			it(`should say what the rotation does to the expiry when ${name}`, async () => {
				serve(key, { 'GET /api/nss/key-settings': settings });
				await loaded();
				await settle();
				await userEvent.click(screen.getByTestId('nss-key-rotate'));

				expect(screen.getByTestId('nss-key-rotate-expiry')).toHaveTextContent(want);
			});
		}

		it('should say an expired key gets no grace before confirming', async () => {
			serve(
				keyFixture({ permissions: ALL, status: 'expired', expires_at: '2026-10-01T12:00:00Z' })
			);
			await loaded();
			await settle();
			await userEvent.click(screen.getByTestId('nss-key-rotate'));

			expect(screen.getByTestId('nss-key-rotate-grace')).toHaveTextContent(
				'The current secret is not accepted again'
			);
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

	describe('usage alerts', () => {
		const cases: { name: string; key: NSSKey; shown: string[] }[] = [
			{
				name: 'the miss rate alert fired this week',
				key: keyFixture({ miss_rate_alert_at: '2026-10-17T09:30:00Z' }),
				shown: ['nss-key-alert-misses']
			},
			{
				name: 'the key source alert fired this week',
				key: keyFixture({ key_source_alert_at: '2026-10-16T00:05:00Z' }),
				shown: ['nss-key-alert-sources']
			},
			{
				name: 'both fired this week',
				key: keyFixture({
					miss_rate_alert_at: '2026-10-17T09:30:00Z',
					key_source_alert_at: '2026-10-16T00:05:00Z'
				}),
				shown: ['nss-key-alert-misses', 'nss-key-alert-sources']
			}
		];
		for (const { name, key, shown } of cases) {
			it(`should name each alert when ${name}`, async () => {
				serve(key);
				await loaded();

				const notice = screen.getByTestId('nss-key-alerts');
				expect(
					['nss-key-alert-misses', 'nss-key-alert-sources'].filter(
						(id) => within(notice).queryByTestId(id) !== null
					)
				).toEqual(shown);
			});
		}

		it('should say when the miss rate alert fired', async () => {
			serve(keyFixture({ miss_rate_alert_at: '2026-10-17T09:30:00Z' }));
			await loaded();

			expect(screen.getByTestId('nss-key-alert-misses')).toHaveTextContent(
				`Many unknown names were looked up with this key at ${formatDateTime('2026-10-17T09:30:00Z')}.`
			);
		});

		it('should show no notice when neither alert fired in the last 7 days', async () => {
			serve(keyFixture());
			await loaded();

			expect(screen.queryByTestId('nss-key-alerts')).not.toBeInTheDocument();
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
			await settle();
			await userEvent.click(screen.getByTestId('nss-key-clear-confirm'));

			expect(calls.some((call) => call.method === 'DELETE')).toBe(true);
		});

		const holdCases: {
			name: string;
			report: Route | Response;
			disabled: boolean;
		}[] = [
			{
				name: 'the count is still loading',
				report: () => new Promise<Response>(() => {}),
				disabled: true
			},
			{
				name: 'the count has loaded',
				report: envelope(reportFixture([reportRow('10.0.0.1', 'switched')])),
				disabled: false
			},
			{
				name: 'the count failed to load',
				report: envelope(null, 500, 'boom'),
				disabled: false
			}
		];
		for (const { name, report, disabled } of holdCases) {
			it(`should ${disabled ? 'hold' : 'offer'} End previous secret when ${name}`, async () => {
				serve(key, { 'GET /api/nss/keys/nss_k7m4qp2x/rotation-report': report });
				await loaded();
				await userEvent.click(screen.getByTestId('nss-key-clear-previous'));
				await settle();

				expect(screen.getByTestId('nss-key-clear-confirm').hasAttribute('disabled')).toBe(disabled);
			});
		}

		it('should link to the rotation report from the dialog when the count fails', async () => {
			serve(key, {
				'GET /api/nss/keys/nss_k7m4qp2x/rotation-report': envelope(null, 500, 'boom')
			});
			await loaded();
			await userEvent.click(screen.getByTestId('nss-key-clear-previous'));
			await settle();

			expect(screen.getByTestId('nss-key-clear-report-link')).toHaveAttribute(
				'href',
				'/nss-keys/nss_k7m4qp2x/rotation'
			);
		});

		it('should show the count of the latest opening when an earlier answer lands last', async () => {
			const answers: Array<(response: Response) => void> = [];
			serve(key, {
				'GET /api/nss/keys/nss_k7m4qp2x/rotation-report': () =>
					new Promise<Response>((resolve) => answers.push(resolve))
			});
			await loaded();
			await userEvent.click(screen.getByTestId('nss-key-clear-previous'));
			await userEvent.click(screen.getByRole('button', { name: 'Cancel' }));
			await waitFor(() =>
				expect(screen.queryByTestId('nss-key-clear-confirm')).not.toBeInTheDocument()
			);
			await userEvent.click(screen.getByTestId('nss-key-clear-previous'));
			answers[1](envelope(reportFixture([reportRow('10.0.0.1', 'not_switched')])));
			await settle();
			answers[0](envelope(reportFixture([reportRow('10.0.0.2', 'switched')])));
			await settle();

			expect(screen.getByTestId('nss-key-clear-count')).toHaveTextContent(
				'1 host still on the old secret will start failing.'
			);
		});

		it('should put the caret on the previous secret row when it has been ended', async () => {
			serve(key, {
				'GET /api/nss/keys/nss_k7m4qp2x/rotation-report': envelope(reportFixture([])),
				'DELETE /api/nss/keys/nss_k7m4qp2x/previous': envelope(keyFixture({ permissions: ALL }))
			});
			await loaded();
			await userEvent.click(screen.getByTestId('nss-key-clear-previous'));
			await settle();
			await userEvent.click(screen.getByTestId('nss-key-clear-confirm'));
			await waitFor(() =>
				expect(screen.queryByTestId('nss-key-clear-confirm')).not.toBeInTheDocument()
			);
			await tick();

			expect(document.activeElement).toBe(screen.getByTestId('nss-key-previous'));
		});
	});

	describe('inline edits', () => {
		const fields = ['description', 'group', 'email'];

		/** openEdit loads an editable key and opens one field's form. */
		async function openEdit(field: string) {
			serve(keyFixture({ permissions: ALL }), {
				'PATCH /api/nss/keys/nss_k7m4qp2x': envelope(keyFixture({ permissions: ALL }))
			});
			await loaded();
			await userEvent.click(screen.getByTestId(`nss-key-edit-${field}`));
		}

		for (const field of fields) {
			it(`should put the caret in the field when Edit ${field} is pressed`, async () => {
				await openEdit(field);

				expect(document.activeElement).toBe(screen.getByTestId(`nss-key-input-${field}`));
			});

			it(`should put the caret back on Edit ${field} when the edit is cancelled`, async () => {
				await openEdit(field);
				await userEvent.click(screen.getByRole('button', { name: 'Cancel' }));
				await tick();

				expect(document.activeElement).toBe(screen.getByTestId(`nss-key-edit-${field}`));
			});

			it(`should put the caret back on Edit ${field} when Escape is pressed`, async () => {
				await openEdit(field);
				await userEvent.keyboard('{Escape}');
				await tick();

				expect(document.activeElement).toBe(screen.getByTestId(`nss-key-edit-${field}`));
			});

			it(`should put the caret back on Edit ${field} when the edit is saved`, async () => {
				await openEdit(field);
				await userEvent.click(screen.getByTestId(`nss-key-save-${field}`));
				await waitFor(() =>
					expect(screen.queryByTestId(`nss-key-form-${field}`)).not.toBeInTheDocument()
				);
				await tick();

				expect(document.activeElement).toBe(screen.getByTestId(`nss-key-edit-${field}`));
			});
		}

		it('should keep the form open when Escape is pressed while the save is in flight', async () => {
			serve(keyFixture({ permissions: ALL }), {
				'PATCH /api/nss/keys/nss_k7m4qp2x': () => new Promise<Response>(() => {})
			});
			await loaded();
			await userEvent.click(screen.getByTestId('nss-key-edit-description'));
			await userEvent.click(screen.getByTestId('nss-key-save-description'));
			await userEvent.keyboard('{Escape}');

			expect(screen.getByTestId('nss-key-form-description')).toBeInTheDocument();
		});

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

		it('should list both rows when the server repeats a day, address and generation', async () => {
			serve(keyFixture(), {
				'GET /api/nss/keys/nss_k7m4qp2x/usage': envelope({ rows: [row, row], total: 2 })
			});
			await loaded();
			await screen.findAllByTestId('nss-key-usage-row');

			expect(screen.getAllByTestId('nss-key-usage-row')).toHaveLength(2);
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
