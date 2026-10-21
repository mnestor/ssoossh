import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { IdentityEchoPayload } from '#lib/api/types.js';
import Page from './+page.svelte';

/** encode writes a value the way the callback's fragment carries it. */
function encode(value: unknown): string {
	return btoa(JSON.stringify(value)).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

/** payload is one echo result. */
function payload(): IdentityEchoPayload {
	return {
		claims: { sub: 'sub-alice', preferred_username: 'alice' },
		mapping: { subject: 'sub', username: 'preferred_username' },
		fields: [
			{
				field: 'subject',
				claim: 'sub',
				required: true,
				status: 'resolved',
				list: false,
				value: 'sub-alice'
			},
			{
				field: 'username',
				claim: 'preferred_username',
				required: true,
				status: 'resolved',
				list: false,
				value: 'alice'
			}
		],
		sign_in: { allowed: true },
		issued_at: '2026-10-21T09:00:00Z'
	};
}

/** respond stubs fetch with one JSON envelope. */
function respond(status: number, body: unknown) {
	const fetchSpy = vi.fn(() =>
		Promise.resolve(
			new Response(JSON.stringify(body), {
				status,
				headers: { 'Content-Type': 'application/json' }
			})
		)
	);
	vi.stubGlobal('fetch', fetchSpy);
	return fetchSpy;
}

// Replacing window.location is the only way to observe the redirect, and it
// has to be undone or every later test sees a plain object where jsdom's
// Location was.
const realLocation = Object.getOwnPropertyDescriptor(window, 'location');

beforeEach(() => {
	window.history.replaceState(null, '', '/identity/echo');
});

afterEach(() => {
	vi.unstubAllGlobals();
	if (realLocation) {
		Object.defineProperty(window, 'location', realLocation);
	}
	window.history.replaceState(null, '', '/identity/echo');
});

describe('Bootstrap identity echo page', () => {
	it('should say where the link comes from when there is none', () => {
		render(Page);
		expect(screen.getByTestId('echo-empty')).toHaveTextContent('ssoosshd identity echo');
	});

	describe('when opened from a link', () => {
		it('should ask before it spends the code', async () => {
			window.history.replaceState(null, '', '/identity/echo#code=abc123');
			const fetchSpy = respond(200, { data: { authorization_url: 'x' }, error: null });
			render(Page);

			await screen.findByTestId('echo-redeem-start');
			expect(fetchSpy).not.toHaveBeenCalled();
		});

		it('should drop the code from the address bar', async () => {
			window.history.replaceState(null, '', '/identity/echo#code=abc123');
			render(Page);

			await screen.findByTestId('echo-redeem-start');
			expect(window.location.hash).toBe('');
		});

		it('should redeem the code from the link', async () => {
			window.history.replaceState(null, '', '/identity/echo#code=abc123');
			const fetchSpy = respond(200, {
				data: { authorization_url: 'https://idp.example.com/a' },
				error: null
			});
			render(Page);
			const start = await screen.findByTestId('echo-redeem-start');
			Object.defineProperty(window, 'location', {
				value: { hash: '', search: '', pathname: '/identity/echo', href: '' },
				writable: true,
				configurable: true
			});

			await userEvent.click(start);
			const [, init] = fetchSpy.mock.calls[0] as unknown as [string, RequestInit];
			expect(JSON.parse(String(init.body))).toEqual({ code: 'abc123' });
		});

		it('should send the browser to the provider', async () => {
			window.history.replaceState(null, '', '/identity/echo#code=abc123');
			respond(200, { data: { authorization_url: 'https://idp.example.com/a' }, error: null });
			render(Page);
			const start = await screen.findByTestId('echo-redeem-start');
			Object.defineProperty(window, 'location', {
				value: { hash: '', search: '', pathname: '/identity/echo', href: '' },
				writable: true,
				configurable: true
			});

			await userEvent.click(start);
			expect(window.location.href).toBe('https://idp.example.com/a');
		});

		it('should say a refused code is wrong, used or expired', async () => {
			window.history.replaceState(null, '', '/identity/echo#code=abc123');
			respond(404, { data: null, error: 'not found' });
			render(Page);

			await userEvent.click(await screen.findByTestId('echo-redeem-start'));
			expect(await screen.findByTestId('echo-redeem-error')).toHaveTextContent(
				'wrong, already used, or expired'
			);
		});
	});

	it('should show the result the callback hands back', async () => {
		window.history.replaceState(null, '', '/identity/echo#' + encode(payload()));
		render(Page);
		expect(await screen.findByTestId('echo-claims')).toHaveTextContent('preferred_username');
	});

	it('should show the error that came before any claims', async () => {
		window.history.replaceState(
			null,
			'',
			'/identity/echo?error=failed#' + encode({ message: 'oauth2: "invalid_client"' })
		);
		render(Page);
		expect(await screen.findByTestId('echo-failure-detail')).toHaveTextContent('invalid_client');
	});

	it('should clear the reason from the address bar', async () => {
		window.history.replaceState(null, '', '/identity/echo?error=refused');
		render(Page);
		await screen.findByTestId('echo-failure');
		expect(window.location.search).toBe('');
	});

	it('should report a link it cannot read', async () => {
		window.history.replaceState(null, '', '/identity/echo#nonsense');
		render(Page);
		expect(await screen.findByTestId('echo-read-error')).toBeInTheDocument();
	});
});
