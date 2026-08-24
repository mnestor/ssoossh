import { render, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import type { NotificationPreferences } from '$lib/api/types';
import Page from './+page.svelte';

/** alicePreferences is a fully populated payload, overridable per test. */
function alicePreferences(
	overrides: Partial<NotificationPreferences> = {}
): NotificationPreferences {
	return {
		mail_enabled: true,
		address: 'alice@example.com',
		kinds: [
			{
				kind: 'service_enrollment_created',
				title: 'Service enrollment created',
				description: 'Sent when you approve a service certificate request.',
				enabled: true
			},
			{
				kind: 'service_enrollment_redeemed',
				title: 'Service enrollment redeemed',
				description: 'Sent every time one of your enrollment codes is redeemed.',
				enabled: false
			}
		],
		...overrides
	};
}

/** mockFetch stubs the global fetch with one response body per call. */
function mockFetch(...responses: object[]) {
	const fetchMock = vi.fn();
	for (const response of responses) {
		fetchMock.mockResolvedValueOnce(
			new Response(JSON.stringify({ data: response, error: null }), {
				status: 200,
				headers: { 'Content-Type': 'application/json' }
			})
		);
	}
	vi.stubGlobal('fetch', fetchMock);
	return fetchMock;
}

/** mockFetchFailure stubs fetch to answer with an error envelope. */
function mockFetchFailure(status: number, message: string) {
	vi.stubGlobal(
		'fetch',
		vi.fn(() =>
			Promise.resolve(
				new Response(JSON.stringify({ data: null, error: message }), {
					status,
					headers: { 'Content-Type': 'application/json' }
				})
			)
		)
	);
}

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('Preferences page', () => {
	describe('when the preferences load', () => {
		it('should list every notification the server offers', async () => {
			mockFetch(alicePreferences());
			render(Page);

			expect(await screen.findByText('Service enrollment created')).toBeInTheDocument();
			expect(screen.getByText('Service enrollment redeemed')).toBeInTheDocument();
		});

		it("should show each kind's description", async () => {
			mockFetch(alicePreferences());
			render(Page);

			expect(
				await screen.findByText('Sent when you approve a service certificate request.')
			).toBeInTheDocument();
		});

		it('should reflect the stored answer in each toggle', async () => {
			mockFetch(alicePreferences());
			render(Page);

			const created = await screen.findByRole('checkbox', { name: /Service enrollment created/ });
			const redeemed = screen.getByRole('checkbox', { name: /Service enrollment redeemed/ });

			expect(created).toBeChecked();
			expect(redeemed).not.toBeChecked();
		});

		it('should show the address notifications go to', async () => {
			mockFetch(alicePreferences());
			render(Page);

			expect(await screen.findByText('alice@example.com')).toBeInTheDocument();
		});
	});

	// Storing a preference the server cannot act on is the confusing case:
	// the toggle works, nothing arrives, and nothing says why.
	describe('when the server cannot send mail', () => {
		it('should say so', async () => {
			mockFetch(alicePreferences({ mail_enabled: false }));
			render(Page);

			expect(await screen.findByText(/not configured to send email/i)).toBeInTheDocument();
		});

		it('should still render the toggles', async () => {
			mockFetch(alicePreferences({ mail_enabled: false }));
			render(Page);

			expect(
				await screen.findByRole('checkbox', { name: /Service enrollment created/ })
			).toBeInTheDocument();
		});
	});

	describe('when the identity has no email address', () => {
		it('should say nothing can be delivered', async () => {
			mockFetch(alicePreferences({ address: '' }));
			render(Page);

			expect(await screen.findByText(/no email address/i)).toBeInTheDocument();
		});
	});

	describe('when saving', () => {
		it('should send only the kind that changed', async () => {
			const fetchMock = mockFetch(alicePreferences(), alicePreferences());
			render(Page);

			const created = await screen.findByRole('checkbox', { name: /Service enrollment created/ });
			await userEvent.click(created);
			await userEvent.click(screen.getByRole('button', { name: /save/i }));

			await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));

			const [, init] = fetchMock.mock.calls[1];
			expect(JSON.parse(init.body)).toEqual({ kinds: { service_enrollment_created: false } });
			expect(init.method).toBe('PUT');
		});

		it('should confirm the save', async () => {
			mockFetch(alicePreferences(), alicePreferences());
			render(Page);

			await userEvent.click(
				await screen.findByRole('checkbox', { name: /Service enrollment created/ })
			);
			await userEvent.click(screen.getByRole('button', { name: /save/i }));

			expect(await screen.findByText(/preferences saved/i)).toBeInTheDocument();
		});

		it('should not offer to save when nothing has changed', async () => {
			mockFetch(alicePreferences());
			render(Page);

			await screen.findByRole('checkbox', { name: /Service enrollment created/ });
			expect(screen.getByRole('button', { name: /save/i })).toBeDisabled();
		});

		it('should report a failure without discarding the edit', async () => {
			const fetchMock = vi.fn();
			fetchMock.mockResolvedValueOnce(
				new Response(JSON.stringify({ data: alicePreferences(), error: null }), {
					status: 200,
					headers: { 'Content-Type': 'application/json' }
				})
			);
			fetchMock.mockResolvedValueOnce(
				new Response(JSON.stringify({ data: null, error: 'the relay is on fire' }), {
					status: 500,
					headers: { 'Content-Type': 'application/json' }
				})
			);
			vi.stubGlobal('fetch', fetchMock);

			render(Page);
			const created = await screen.findByRole('checkbox', { name: /Service enrollment created/ });
			await userEvent.click(created);
			await userEvent.click(screen.getByRole('button', { name: /save/i }));

			expect(await screen.findByText(/the relay is on fire/)).toBeInTheDocument();
			expect(created).not.toBeChecked();
		});
	});

	describe('when the preferences cannot be loaded', () => {
		it('should show the error', async () => {
			mockFetchFailure(403, 'no user record for the authenticated identity');
			render(Page);

			expect(await screen.findByText(/no user record/)).toBeInTheDocument();
		});
	});
});
