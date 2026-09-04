import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi, afterEach, beforeEach } from 'vitest';

import type { CertificateListResponse } from '$lib/api/types';
import { resetFakePage } from '$lib/testing/page.svelte';
import Page from './+page.svelte';

// $app/state and $app/navigation are replaced with a reactive fake so the
// shallow-routing flow (click a row, modal opens) is exercised for real.
// See src/lib/testing/page.svelte.ts for why the fake refuses to update
// page.url.
vi.mock('$app/state', async () => {
	const { fakePage } = await import('$lib/testing/page.svelte');
	return { page: fakePage };
});
vi.mock('$app/navigation', async () => {
	const { fakePushState } = await import('$lib/testing/page.svelte');
	return { pushState: fakePushState };
});

/** mockFetch stubs the global fetch with a response body and status. */
function mockFetch(response: object, status = 200) {
	vi.stubGlobal(
		'fetch',
		vi.fn(() =>
			Promise.resolve(
				new Response(JSON.stringify({ data: response, error: null }), {
					status,
					headers: { 'Content-Type': 'application/json' }
				})
			)
		)
	);
}

/** mockFetchError stubs the global fetch to reject with an error message. */
function mockFetchError(message = 'network error') {
	vi.stubGlobal(
		'fetch',
		vi.fn(() => Promise.reject(new Error(message)))
	);
}

beforeEach(() => {
	resetFakePage('http://localhost/dashboard');
});

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('Dashboard page', () => {
	describe('when certificates load successfully with an empty list', () => {
		beforeEach(() => {
			const emptyResponse: CertificateListResponse = {
				certificates: []
			};
			mockFetch(emptyResponse);
		});

		it('should not show Loading… after the fetch resolves', async () => {
			render(Page);
			// Give the effect a chance to run and complete
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.queryByText('Loading…')).not.toBeInTheDocument();
		});

		it('should show the empty-state message when no certificates are present', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByText(/ssoossh login/)).toBeInTheDocument();
		});

		it('should display the empty-state prompt specifically', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByText(/Nothing yet/)).toBeInTheDocument();
		});
	});

	describe('when the fetch fails', () => {
		beforeEach(() => {
			mockFetchError('server error');
		});

		it('should not show Loading… after the fetch rejects', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.queryByText('Loading…')).not.toBeInTheDocument();
		});

		it('should show the error message', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByText(/Could not load your certificates/)).toBeInTheDocument();
		});
	});

	describe('when certificates load successfully with data', () => {
		beforeEach(() => {
			const populatedResponse: CertificateListResponse = {
				certificates: [
					{
						id: 'cert-1',
						type: 'user',
						serial_number: '1',
						principals: 'alice',
						public_key_fingerprint: 'SHA256:abc123',
						issued_at: '2026-08-01T10:00:00Z',
						expires_at: new Date(Date.now() + 86400000).toISOString(),
						decided_at: '2026-08-01T10:00:00Z',
						decided_by_username: 'system',
						decided_by_subject: '',
						key_id: 'key-1'
					}
				]
			};
			mockFetch(populatedResponse);
		});

		it('should not show Loading… after the fetch resolves', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.queryByText('Loading…')).not.toBeInTheDocument();
		});

		it('should display the certificate principal', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByText('alice')).toBeInTheDocument();
		});
	});

	it('should display Loading… initially', () => {
		mockFetch({ certificates: [] });
		render(Page);
		// Before the effect runs, should show loading
		expect(screen.getByText('Loading…')).toBeInTheDocument();
	});

	describe('the certificate detail modal', () => {
		beforeEach(() => {
			const response: CertificateListResponse = {
				certificates: [
					{
						id: 'cert-1',
						type: 'user',
						serial_number: '1',
						principals: 'alice',
						public_key_fingerprint: 'SHA256:abc123',
						issued_at: '2026-08-01T10:00:00Z',
						expires_at: new Date(Date.now() + 86400000).toISOString(),
						key_id: 'key-1'
					}
				]
			};
			mockFetch(response);
		});

		// Regression: this flow was wired through the ?modal= search
		// parameter, but SvelteKit's pushState never reassigns page.url — so
		// the row updated the address bar and nothing else, and the modal
		// never opened.
		it('should open the modal when a row is activated', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			await userEvent.click(screen.getByRole('button', { name: /key-1/ }));
			expect(screen.getByRole('button', { name: 'Close' })).toBeInTheDocument();
		});

		it('should close the modal when Close is used', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			await userEvent.click(screen.getByRole('button', { name: /key-1/ }));
			await userEvent.click(screen.getByRole('button', { name: 'Close' }));
			expect(screen.queryByRole('button', { name: 'Close' })).not.toBeInTheDocument();
		});

		it('should open the modal named by a pasted ?modal= link', async () => {
			resetFakePage('http://localhost/dashboard?modal=cert-1');
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByRole('button', { name: 'Close' })).toBeInTheDocument();
		});

		// The search parameter stays in page.url after closing, so a close
		// that only cleared state would be undone on the next recompute.
		it('should stay closed after closing a modal that arrived by link', async () => {
			resetFakePage('http://localhost/dashboard?modal=cert-1');
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			await userEvent.click(screen.getByRole('button', { name: 'Close' }));
			expect(screen.queryByRole('button', { name: 'Close' })).not.toBeInTheDocument();
		});
	});
});
