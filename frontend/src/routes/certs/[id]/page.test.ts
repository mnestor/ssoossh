import { render, screen, within } from '@testing-library/svelte';
import { describe, expect, it, vi, afterEach, beforeEach } from 'vitest';

import type { CertificateResponse } from '$lib/api/types';
import Page from './+page.svelte';

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

function mockFetchError(message = 'network error') {
	vi.stubGlobal(
		'fetch',
		vi.fn(() => Promise.reject(new Error(message)))
	);
}

beforeEach(() => {
	vi.clearAllMocks();
});

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('Certificate detail page', () => {
	describe('when certificate loads successfully', () => {
		beforeEach(() => {
			const cert: CertificateResponse = {
				id: 'cert-123',
				type: 'user',
				serial_number: 42,
				key_id: 'my-key',
				principals: 'alice,alice@example.com',
				public_key_fingerprint: 'SHA256:abcd1234',
				issued_at: new Date('2024-08-24T10:00:00Z').toISOString(),
				expires_at: new Date('2024-08-24T18:00:00Z').toISOString()
			};
			mockFetch(cert);
		});

		it('should render the certificate details', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByText(/cert-123/)).toBeInTheDocument();
		});

		it('should display the certificate type', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			const certDetails = screen.getByTestId('cert-details');
			// Find the Type definition term (dt) which contains "Type" label
			const typeTerms = within(certDetails).getAllByText('Type');
			expect(typeTerms.length).toBeGreaterThan(0);
			// The type term should be a dt element
			const typeTerm = typeTerms[0] as HTMLElement;
			expect(typeTerm.tagName.toLowerCase()).toBe('dt');
		});

		it('should display the key ID', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByText(/my-key/)).toBeInTheDocument();
		});

		it('should display the serial number', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByText(/42/)).toBeInTheDocument();
		});
	});

	describe('when certificate fetch returns 404', () => {
		beforeEach(() => {
			mockFetch({ error: 'not found' }, 404);
		});

		it('should render access denied message', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByText(/not found|not authorized|access denied/i)).toBeInTheDocument();
		});
	});

	describe('when certificate fetch fails with network error', () => {
		beforeEach(() => {
			mockFetchError('network timeout');
		});

		it('should render error message', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByText(/could not load|error/i)).toBeInTheDocument();
		});
	});

	describe('loading state', () => {
		beforeEach(() => {
			mockFetch({ id: 'cert-123', type: 'user' }, 200);
		});

		it('should show loading state initially', () => {
			render(Page);
			expect(screen.getByText(/loading|loading\.\.\./i)).toBeInTheDocument();
		});
	});
});
