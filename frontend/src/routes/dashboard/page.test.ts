import { render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi, afterEach, beforeEach } from 'vitest';

import type { CertificateListResponse } from '$lib/api/types';
import Page from './+page.svelte';

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

	// A row is a link to the certificate's own page. It used to open a
	// dialog over the list, which could not be reloaded, linked to, or left
	// with the browser's own Back.
	describe('the rows', () => {
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

		it('should link a row to the certificate it is about', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByRole('link', { name: /key-1/ })).toHaveAttribute(
				'href',
				'/certs/cert-1?from=dashboard'
			);
		});
	});
});
