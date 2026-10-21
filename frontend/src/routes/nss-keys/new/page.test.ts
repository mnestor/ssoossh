import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { formatDateTime } from '#lib/format.js';
import { session } from '#lib/session.svelte.js';
import { endOfDay, localDay } from '#lib/nss.js';
import { envelope, keyFixture, routeFetch, settle, userFixture } from '#lib/testing/nss.js';

// Test methodology: fill the form and route the create call to a fixture
// answer, then drive the one-time secret panel. What is pinned: who may
// reach the form, what the body carries, that the secret cannot be waved
// away unacknowledged, and that nothing on the page still holds it once it
// is dismissed. Navigation is stubbed; beforeNavigate's callback is kept so
// the leave guard can be exercised directly.

const { goto, guards } = vi.hoisted(() => ({
	goto: vi.fn(() => Promise.resolve()),
	guards: [] as Array<(navigation: { cancel: () => void; type: string }) => void>
}));

vi.mock('$app/navigation', () => ({
	goto,
	beforeNavigate: (callback: (navigation: { cancel: () => void; type: string }) => void) => {
		guards.push(callback);
	}
}));
vi.mock('$app/state', async () => {
	const { fakePage } = await import('#lib/testing/page.svelte.js');
	return { page: fakePage };
});

import Page from './+page.svelte';

import { resetFakePage } from '#lib/testing/page.svelte.js';

const SECRET = 'c2VjcmV0LW9uY2U';

beforeEach(() => {
	resetFakePage('http://localhost/nss-keys/new');
	session.user = userFixture({ is_nss_key_admin: true });
	session.resolved = true;
	goto.mockClear();
	guards.length = 0;
});

afterEach(() => {
	vi.unstubAllGlobals();
	session.clear();
});

/** answers routes the suggestions read and the create call. */
function answers(create: Response = envelope({ key: keyFixture(), secret: SECRET })) {
	return routeFetch({
		'GET /api/nss/keys': envelope({ keys: [], can_create: true }),
		'GET /api/admin/nss/keys': envelope({ keys: [], total: 0, usage_retention_seconds: 0 }),
		'POST /api/nss/keys': create
	});
}

/** createNamed types a name and presses Create key. */
async function createNamed(name = 'web fleet') {
	await userEvent.type(screen.getByTestId('nss-key-name'), name);
	await userEvent.click(screen.getByTestId('nss-key-create'));
}

describe('New NSS key', () => {
	it('should turn away a caller without the fleet key admin role', () => {
		session.user = userFixture();
		answers();
		render(Page);

		expect(screen.getByTestId('nss-key-new-denied')).toBeInTheDocument();
	});

	it('should ask for a name when Create key is pressed without one', async () => {
		const calls = answers();
		render(Page);
		await userEvent.click(screen.getByTestId('nss-key-create'));

		expect(screen.getByTestId('nss-key-name-error')).toBeInTheDocument();
		expect(calls.filter((call) => call.method === 'POST')).toHaveLength(0);
	});

	it('should name the name field by its label alone when its error shows', async () => {
		answers();
		render(Page);
		await userEvent.click(screen.getByTestId('nss-key-create'));

		expect(screen.getByRole('textbox', { name: 'Name' })).toBe(screen.getByTestId('nss-key-name'));
	});

	it('should describe the name field with its error when the name is missing', async () => {
		answers();
		render(Page);
		await userEvent.click(screen.getByTestId('nss-key-create'));

		expect(screen.getByTestId('nss-key-name')).toHaveAccessibleDescription(
			'Give the key a name, such as the fleet it serves.'
		);
	});

	it('should mark the notification email optional when the form is shown', () => {
		answers();
		render(Page);

		expect(screen.getByLabelText(/^\s*Notification email \(optional\)/)).toBe(
			screen.getByTestId('nss-key-email')
		);
	});

	it('should say the suggestions include other keys groups when the reader holds the role', () => {
		answers();
		render(Page);

		expect(screen.getByText(/Any group name is accepted/)).toHaveTextContent(
			'the suggestions are the groups other keys use and the groups you are in.'
		);
	});

	// The fleet key admin role reads every key without an admin area role,
	// so its suggestions come from that list too.
	it('should suggest the groups every key uses when the reader holds no admin area role', async () => {
		session.user = userFixture({ is_nss_key_admin: true, groups: [] });
		routeFetch({
			'GET /api/admin/nss/keys': envelope({
				keys: [{ group_name: 'db-admins' }, { group_name: '' }],
				total: 2,
				usage_retention_seconds: 0
			})
		});
		const { container } = render(Page);
		await settle();

		const options = [...container.querySelectorAll('#nss-key-groups option')].map((o) =>
			o.getAttribute('value')
		);
		expect(options).toEqual(['db-admins']);
	});

	const backCases = [
		{
			name: 'it was opened from the keys a reader manages',
			url: '/nss-keys/new',
			href: '/nss-keys',
			label: 'NSS keys'
		},
		{
			name: 'it was opened from the admin list',
			url: '/nss-keys/new?from=admin',
			href: '/admin/nss/keys',
			label: 'All NSS keys'
		}
	];
	for (const { name, url, href, label } of backCases) {
		it(`should point the back chip at ${href} when ${name}`, () => {
			resetFakePage(`http://localhost${url}`);
			answers();
			render(Page);

			expect(screen.getByTestId('nss-key-new-back')).toHaveAttribute('href', href);
			expect(screen.getByTestId('nss-key-new-back')).toHaveTextContent(label);
		});
	}

	it('should default the notification email to the caller', () => {
		answers();
		render(Page);

		expect(screen.getByTestId('nss-key-email')).toHaveValue('alice@corp.example');
	});

	it('should send the name, the group and the address when the form is submitted', async () => {
		const calls = answers();
		render(Page);
		await userEvent.type(screen.getByTestId('nss-key-group'), 'web-admins');
		await createNamed();

		expect(calls.find((call) => call.method === 'POST')?.body).toEqual({
			name: 'web fleet',
			group_name: 'web-admins',
			notification_email: 'alice@corp.example'
		});
	});

	it('should send the end of the chosen day when an expiry is picked', async () => {
		const calls = answers();
		render(Page);
		await userEvent.type(screen.getByTestId('nss-key-expires'), '2027-03-01');
		await createNamed();

		const body = calls.find((call) => call.method === 'POST')?.body as { expires_at: string };
		expect(new Date(body.expires_at).getTime()).toBe(new Date('2027-03-01T23:59:59').getTime());
	});

	describe('proposed expiry', () => {
		const now = new Date('2026-10-18T12:00:00Z');

		beforeEach(() => {
			vi.useFakeTimers({ now, toFake: ['Date'] });
		});

		afterEach(() => {
			vi.useRealTimers();
		});

		/** withSettings routes the form's reads, the key settings among them. */
		function withSettings(settings: Response) {
			return routeFetch({
				'GET /api/nss/keys': envelope({ keys: [], can_create: true }),
				'GET /api/nss/key-settings': settings,
				'POST /api/nss/keys': envelope({ key: keyFixture(), secret: SECRET })
			});
		}

		const YEAR = envelope({ max_lifetime_seconds: 31536000, rotation_grace_seconds: 2592000 });
		// The last day whose end is inside a year from now, in the test's zone.
		const lastDay = localDay(new Date(now.getTime() + (31536000 - 86400) * 1000));

		const cases: { name: string; settings: Response; want: string }[] = [
			{ name: 'the server limits keys to a year', settings: YEAR, want: lastDay },
			{
				name: 'the server limits keys to less than a day',
				settings: envelope({ max_lifetime_seconds: 3600, rotation_grace_seconds: 0 }),
				want: ''
			},
			{
				name: 'the server sets no limit',
				settings: envelope({ max_lifetime_seconds: 0, rotation_grace_seconds: 0 }),
				want: ''
			},
			{
				name: 'the settings fail to load',
				settings: envelope(null, 500, 'database unavailable'),
				want: ''
			}
		];
		for (const { name, settings, want } of cases) {
			it(`should start the expiry at ${want || 'nothing'} when ${name}`, async () => {
				withSettings(settings);
				render(Page);
				await settle();

				expect(screen.getByTestId('nss-key-expires')).toHaveValue(want);
			});
		}

		it('should send the end of the proposed day when the expiry is left as proposed', async () => {
			const calls = withSettings(YEAR);
			render(Page);
			await settle();
			await createNamed();

			const body = calls.find((call) => call.method === 'POST')?.body as { expires_at: string };
			expect(new Date(body.expires_at).getTime()).toBe(endOfDay(lastDay).getTime());
		});

		it('should bound the field at the last day the server allows', async () => {
			withSettings(YEAR);
			render(Page);
			await settle();

			expect(screen.getByTestId('nss-key-expires')).toHaveAttribute('max', lastDay);
		});

		it('should refuse a typed day past the limit without asking the server', async () => {
			const calls = withSettings(YEAR);
			render(Page);
			await settle();
			await userEvent.clear(screen.getByTestId('nss-key-expires'));
			await userEvent.type(screen.getByTestId('nss-key-expires'), '2030-01-01');
			await createNamed();

			expect(screen.getByTestId('nss-key-expires-error')).toBeInTheDocument();
			expect(calls.some((call) => call.method === 'POST')).toBe(false);
		});

		const earlyCases: { name: string; typed: string }[] = [
			{ name: 'a past day is typed', typed: '2026-10-01' },
			{ name: 'today is typed', typed: '2026-10-18' }
		];
		for (const { name, typed } of earlyCases) {
			it(`should refuse the day without asking the server when ${name}`, async () => {
				const calls = withSettings(YEAR);
				render(Page);
				await settle();
				await userEvent.clear(screen.getByTestId('nss-key-expires'));
				await userEvent.type(screen.getByTestId('nss-key-expires'), typed);
				await createNamed();

				expect(calls.some((call) => call.method === 'POST')).toBe(false);
			});
		}

		it('should say to choose a later day when a past day is typed', async () => {
			withSettings(envelope(null, 500, 'database unavailable'));
			render(Page);
			await settle();
			await userEvent.type(screen.getByTestId('nss-key-expires'), '2026-10-01');
			await createNamed();

			expect(screen.getByTestId('nss-key-expires-error')).toHaveTextContent(
				'Choose a day after today.'
			);
		});

		it('should send no expiry when the limit is under a day', async () => {
			const calls = withSettings(
				envelope({ max_lifetime_seconds: 3600, rotation_grace_seconds: 0 })
			);
			render(Page);
			await settle();
			await createNamed();

			const body = calls.find((call) => call.method === 'POST')?.body as object;
			expect(body).not.toHaveProperty('expires_at');
		});

		it('should say an empty expiry never expires when the server sets no limit', async () => {
			withSettings(envelope({ max_lifetime_seconds: 0, rotation_grace_seconds: 0 }));
			render(Page);
			await settle();

			expect(screen.getByTestId('nss-key-expires-hint')).toHaveTextContent(
				'Leave it empty and the key never expires.'
			);
		});

		it('should say each rotation renews the expiry when the form is shown', async () => {
			withSettings(YEAR);
			render(Page);
			await settle();

			expect(screen.getByTestId('nss-key-expires-hint')).toHaveTextContent(
				"The expiry is set now. Each rotation renews it by the same length, up to the server's limit."
			);
		});

		const createdCases: {
			name: string;
			typed: string;
			expiresAt: string | undefined;
			want: string;
		}[] = [
			{
				name: 'the server keeps the day asked for',
				typed: '2027-01-10',
				expiresAt: endOfDay('2027-01-10').toISOString(),
				want: `It expires ${formatDateTime(endOfDay('2027-01-10').toISOString())}.`
			},
			{
				name: "the server's limit trims the day asked for",
				typed: '2030-01-01',
				expiresAt: '2027-10-18T12:00:00.000Z',
				want: `It expires ${formatDateTime('2027-10-18T12:00:00.000Z')}, the latest the server's limit allows, not the day you chose.`
			},
			{
				name: 'the key never expires',
				typed: '',
				expiresAt: undefined,
				want: 'It never expires.'
			}
		];
		for (const { name, typed, expiresAt, want } of createdCases) {
			it(`should say the expiry the key got when ${name}`, async () => {
				routeFetch({
					'GET /api/nss/keys': envelope({ keys: [], can_create: true }),
					'GET /api/nss/key-settings': envelope(null, 500, 'database unavailable'),
					'POST /api/nss/keys': envelope({
						key: keyFixture({ expires_at: expiresAt }),
						secret: SECRET
					})
				});
				render(Page);
				await settle();
				if (typed) await userEvent.type(screen.getByTestId('nss-key-expires'), typed);
				await createNamed();

				expect(await screen.findByTestId('nss-key-created-expiry')).toHaveTextContent(want);
			});
		}
	});

	it('should show the server reason when the create is refused', async () => {
		answers(envelope(null, 400, 'expires_at is in the past'));
		render(Page);
		await createNamed();

		expect(await screen.findByTestId('nss-key-create-error')).toHaveTextContent(
			'expires_at is in the past'
		);
	});

	describe('one-time secret', () => {
		it('should show the secret once the key is created', async () => {
			answers();
			render(Page);
			await createNamed();

			expect(await screen.findByTestId('secret-panel-secret')).toHaveTextContent(SECRET);
		});

		it('should take the caret onto the secret once the key is created', async () => {
			answers();
			render(Page);
			await createNamed();

			expect(document.activeElement).toBe(await screen.findByTestId('secret-panel-focus'));
		});

		it('should hold Done until the secret is acknowledged', async () => {
			answers();
			render(Page);
			await createNamed();

			expect(await screen.findByTestId('secret-panel-done')).toBeDisabled();
		});

		it('should open the new key when Done is pressed after acknowledging', async () => {
			answers();
			render(Page);
			await createNamed();
			await userEvent.click(await screen.findByLabelText('I have stored this secret'));
			await userEvent.click(screen.getByTestId('secret-panel-done'));

			expect(goto).toHaveBeenCalledWith('/nss-keys/nss_k7m4qp2x');
		});

		it('should keep the way back to the admin list when Done is pressed after opening from it', async () => {
			resetFakePage('http://localhost/nss-keys/new?from=admin');
			answers();
			render(Page);
			await createNamed();
			await userEvent.click(await screen.findByLabelText('I have stored this secret'));
			await userEvent.click(screen.getByTestId('secret-panel-done'));

			expect(goto).toHaveBeenCalledWith('/nss-keys/nss_k7m4qp2x?from=admin');
		});

		it('should not read the key list again once one is created when the role already shows the entry', async () => {
			const calls = answers();
			render(Page);
			await createNamed();
			await screen.findByTestId('secret-panel-done');
			await settle();

			expect(calls.some((call) => call.method === 'GET' && call.url === '/api/nss/keys')).toBe(
				false
			);
		});

		it('should hold the secret nowhere on the page once Done is pressed', async () => {
			answers();
			const { container } = render(Page);
			await createNamed();
			await userEvent.click(await screen.findByLabelText('I have stored this secret'));
			await userEvent.click(screen.getByTestId('secret-panel-done'));

			expect(container.innerHTML).not.toContain(SECRET);
		});

		it('should ask before a navigation away while the secret is on screen', async () => {
			answers();
			const confirm = vi.fn(() => false);
			vi.stubGlobal('confirm', confirm);
			render(Page);
			await createNamed();
			await screen.findByTestId('secret-panel');

			const cancel = vi.fn();
			guards.forEach((guard) => guard({ cancel, type: 'link' }));

			expect(cancel).toHaveBeenCalled();
		});

		it('should let the reader leave without asking when the secret is ticked as stored', async () => {
			answers();
			const confirm = vi.fn(() => false);
			vi.stubGlobal('confirm', confirm);
			render(Page);
			await createNamed();
			await userEvent.click(await screen.findByLabelText('I have stored this secret'));
			guards.forEach((guard) => guard({ cancel: vi.fn(), type: 'link' }));

			expect(confirm).not.toHaveBeenCalled();
		});

		it('should announce the new key when it is created', async () => {
			answers();
			render(Page);
			await createNamed();
			await screen.findByTestId('secret-panel');

			expect(screen.getByTestId('nss-key-new-live')).toHaveTextContent(
				`Key web fleet created. It expires ${formatDateTime('2027-01-10T12:00:00Z')}. Store its secret now.`
			);
		});

		it('should let a navigation through once the secret is dismissed', async () => {
			answers();
			render(Page);
			await createNamed();
			await userEvent.click(await screen.findByLabelText('I have stored this secret'));
			await userEvent.click(screen.getByTestId('secret-panel-done'));

			const cancel = vi.fn();
			guards.forEach((guard) => guard({ cancel, type: 'goto' }));

			expect(cancel).not.toHaveBeenCalled();
		});
	});
});
