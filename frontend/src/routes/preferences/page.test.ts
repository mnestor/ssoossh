import { render, screen, waitFor, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import type { NotificationPreferences } from '#lib/api/types.js';
import { detail } from '#lib/detail.svelte.js';
import Page from './+page.svelte';

// Test methodology: render the page against a stubbed fetch answering one
// call at a time. What matters: every setting is live (no Save button), a
// switch saves only its own kind, a failed save puts that switch back and
// says so beside it, a second press waits for the first, the kinds are
// shown under the server's groups, and the pages' level survives a failed
// load of the notification settings.

/** alicePreferences is a fully populated payload, overridable per test. */
function alicePreferences(
	overrides: Partial<NotificationPreferences> = {}
): NotificationPreferences {
	return {
		mail_enabled: true,
		address: 'alice@example.com',
		groups: [
			{
				group: 'service_codes',
				title: 'Service codes',
				description: 'The service codes on accounts you hold.'
			},
			{
				group: 'certificates',
				title: 'Certificates issued to you',
				description: 'One email for every login.'
			}
		],
		kinds: [
			{
				kind: 'service_enrollment_created',
				group: 'service_codes',
				title: 'Service code created',
				description: 'Sent when you approve a service certificate request.',
				enabled: true
			},
			{
				kind: 'service_enrollment_redeemed',
				group: 'service_codes',
				title: 'Service code redeemed',
				description: 'Sent every time one of your service codes is redeemed.',
				enabled: false
			},
			{
				kind: 'user_certificate_issued',
				group: 'certificates',
				title: 'User certificate issued',
				description: 'Sent every time an interactive SSH certificate is signed for you.',
				enabled: false
			}
		],
		detail_level: 'simplified',
		...overrides
	};
}

/** ok wraps a body in a successful envelope response. */
function ok(body: object): Response {
	return new Response(JSON.stringify({ data: body, error: null }), {
		status: 200,
		headers: { 'Content-Type': 'application/json' }
	});
}

/** failure is an error envelope response. */
function failure(status: number, message: string): Response {
	return new Response(JSON.stringify({ data: null, error: message }), {
		status,
		headers: { 'Content-Type': 'application/json' }
	});
}

/** mockFetch stubs the global fetch with one response per call, in order. */
function mockFetch(...responses: Response[]) {
	const fetchMock = vi.fn();
	for (const response of responses) {
		fetchMock.mockResolvedValueOnce(response);
	}
	vi.stubGlobal('fetch', fetchMock);
	return fetchMock;
}

/** createdSwitch finds the "Service code created" switch once the page loads. */
function createdSwitch() {
	return screen.findByRole('switch', { name: 'Service code created' });
}

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('Preferences page', () => {
	describe('when the preferences load', () => {
		it('should list every notification the server offers', async () => {
			mockFetch(ok(alicePreferences()));
			render(Page);

			expect(await createdSwitch()).toBeInTheDocument();
		});

		it('should name each switch by its title alone', async () => {
			mockFetch(ok(alicePreferences()));
			render(Page);

			expect(await createdSwitch()).toHaveAccessibleName('Service code created');
		});

		it("should describe each switch with its kind's sentence", async () => {
			mockFetch(ok(alicePreferences()));
			render(Page);

			expect(await createdSwitch()).toHaveAccessibleDescription(
				'Sent when you approve a service certificate request.'
			);
		});

		it('should reflect the stored answer in each switch', async () => {
			mockFetch(ok(alicePreferences()));
			render(Page);

			expect(await createdSwitch()).toHaveAttribute('aria-checked', 'true');
		});

		it('should show each kind under its group', async () => {
			mockFetch(ok(alicePreferences()));
			render(Page);

			const group = await screen.findByRole('group', { name: 'Certificates issued to you' });

			expect(within(group).getByRole('switch', { name: 'User certificate issued' })).toBeVisible();
		});

		it('should show the address notifications go to', async () => {
			mockFetch(ok(alicePreferences()));
			render(Page);

			expect(await screen.findByText('alice@example.com')).toBeInTheDocument();
		});

		it('should offer no Save button', async () => {
			mockFetch(ok(alicePreferences()));
			render(Page);
			await createdSwitch();

			expect(screen.queryByRole('button', { name: /save/i })).toBeNull();
		});
	});

	// Storing a preference the server cannot act on is the confusing case:
	// the switch works, nothing arrives, and nothing says why.
	describe('when the server cannot send mail', () => {
		it('should say so', async () => {
			mockFetch(ok(alicePreferences({ mail_enabled: false })));
			render(Page);

			expect(await screen.findByText(/not configured to send email/i)).toBeInTheDocument();
		});

		it('should still render the switches', async () => {
			mockFetch(ok(alicePreferences({ mail_enabled: false })));
			render(Page);

			expect(await createdSwitch()).toBeInTheDocument();
		});
	});

	describe('when the identity has no email address', () => {
		it('should say nothing can be delivered', async () => {
			mockFetch(ok(alicePreferences({ address: '' })));
			render(Page);

			expect(await screen.findByText(/no email address/i)).toBeInTheDocument();
		});
	});

	describe('when a switch is pressed', () => {
		it('should save only that kind, at once', async () => {
			const fetchMock = mockFetch(ok(alicePreferences()), ok(alicePreferences()));
			render(Page);

			await userEvent.click(await createdSwitch());

			await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
			const [, init] = fetchMock.mock.calls[1];
			expect(JSON.parse(init.body)).toEqual({ kinds: { service_enrollment_created: false } });
		});

		it('should show the new state before the save answers', async () => {
			const fetchMock = mockFetch(ok(alicePreferences()));
			// The save never answers, so what shows is the press alone.
			fetchMock.mockReturnValueOnce(new Promise(() => undefined));
			render(Page);
			const created = await createdSwitch();

			await userEvent.click(created);

			expect(created).toHaveAttribute('aria-checked', 'false');
		});

		it('should announce the save for a screen reader', async () => {
			const stored = alicePreferences();
			stored.kinds[0].enabled = false;
			mockFetch(ok(alicePreferences()), ok(stored));
			render(Page);

			await userEvent.click(await createdSwitch());

			await waitFor(() =>
				expect(screen.getByTestId('preferences-status')).toHaveTextContent(
					'Service code created off. Saved.'
				)
			);
		});

		it('should put the switch back when the save fails', async () => {
			mockFetch(ok(alicePreferences()), failure(500, 'the relay is on fire'));
			render(Page);
			const created = await createdSwitch();

			await userEvent.click(created);

			await waitFor(() => expect(created).toHaveAttribute('aria-checked', 'true'));
		});

		it('should say beside the switch that the save failed', async () => {
			mockFetch(ok(alicePreferences()), failure(500, 'the relay is on fire'));
			render(Page);

			await userEvent.click(await createdSwitch());

			expect(
				await screen.findByTestId('notification-service_enrollment_created-error')
			).toHaveTextContent('Not saved');
		});

		it('should ignore a second press while the first is saving', async () => {
			const fetchMock = vi.fn();
			fetchMock.mockResolvedValueOnce(ok(alicePreferences()));
			// The save never answers, so the switch stays in flight.
			fetchMock.mockReturnValueOnce(new Promise(() => undefined));
			vi.stubGlobal('fetch', fetchMock);
			render(Page);
			const created = await createdSwitch();

			await userEvent.click(created);
			await userEvent.click(created);

			expect(fetchMock).toHaveBeenCalledTimes(2);
		});
	});

	describe('when the preferences cannot be loaded', () => {
		it('should show the error', async () => {
			mockFetch(failure(403, 'no user record for the authenticated identity'));
			render(Page);

			expect(await screen.findByText(/no user record/)).toBeInTheDocument();
		});
	});
});

describe('the Display section', () => {
	afterEach(() => {
		detail.level = 'simplified';
		detail.saveError = null;
	});

	// The level arrives with the session, not with the notification
	// settings, so a failed read of those must not take it with it.
	it('should stay usable when the notification settings fail to load', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(() => Promise.reject(new Error('network down')))
		);
		render(Page);

		await screen.findByTestId('preferences-error');

		expect(screen.getByTestId('display-preferences')).toBeInTheDocument();
	});

	it('should change the detail level the covered pages read', async () => {
		const fetchMock = vi.fn();
		fetchMock.mockRejectedValueOnce(new Error('network down'));
		fetchMock.mockResolvedValueOnce(ok({ detail_level: 'advanced' }));
		vi.stubGlobal('fetch', fetchMock);
		vi.stubGlobal('localStorage', { getItem: () => null, setItem: () => undefined });
		render(Page);
		await screen.findByTestId('preferences-error');

		await userEvent.click(screen.getByTestId('display-detail-advanced'));

		await waitFor(() => expect(detail.level).toBe('advanced'));
	});

	it('should say so when the level could not be saved', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(() => Promise.reject(new Error('network down')))
		);
		vi.stubGlobal('localStorage', { getItem: () => null, setItem: () => undefined });
		render(Page);
		await screen.findByTestId('preferences-error');

		await userEvent.click(screen.getByTestId('display-detail-advanced'));

		expect(await screen.findByTestId('display-detail-error')).toBeInTheDocument();
	});
});

describe('the email detail level', () => {
	it('should sit with the email notifications', async () => {
		mockFetch(ok(alicePreferences()));
		render(Page);

		expect(await screen.findByTestId('email-detail')).toBeInTheDocument();
	});

	it('should show the stored email level as selected', async () => {
		mockFetch(ok(alicePreferences({ detail_level: 'advanced' })));
		render(Page);

		const advanced = await screen.findByTestId('email-detail-toggle-advanced');

		expect(advanced).toHaveAttribute('aria-pressed', 'true');
	});

	it('should save the chosen level on the press', async () => {
		const fetchMock = mockFetch(
			ok(alicePreferences()),
			ok(alicePreferences({ detail_level: 'advanced' }))
		);
		render(Page);

		await userEvent.click(await screen.findByTestId('email-detail-toggle-advanced'));

		await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
		const [, init] = fetchMock.mock.calls[1];
		expect(JSON.parse(init.body)).toEqual({ kinds: {}, detail_level: 'advanced' });
	});

	it('should put the level back when the save fails', async () => {
		mockFetch(ok(alicePreferences()), failure(500, 'database is down'));
		render(Page);

		await userEvent.click(await screen.findByTestId('email-detail-toggle-advanced'));

		await waitFor(() =>
			expect(screen.getByTestId('email-detail-toggle-simplified')).toHaveAttribute(
				'aria-pressed',
				'true'
			)
		);
	});

	it('should say so when the save fails', async () => {
		mockFetch(ok(alicePreferences()), failure(500, 'database is down'));
		render(Page);

		await userEvent.click(await screen.findByTestId('email-detail-toggle-advanced'));

		expect(await screen.findByTestId('email-detail-error')).toHaveTextContent('Not saved');
	});

	it('should not touch the pages level', async () => {
		mockFetch(ok(alicePreferences()), ok(alicePreferences({ detail_level: 'advanced' })));
		detail.level = 'simplified';
		render(Page);

		await userEvent.click(await screen.findByTestId('email-detail-toggle-advanced'));

		expect(detail.level).toBe('simplified');
	});
});
