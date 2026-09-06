import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { IdentityEchoPayload } from '$lib/api/types';
import Page from './+page.svelte';

/** echoPayload is one decoded ID token with a claim nothing reads. */
function echoPayload(overrides: Partial<IdentityEchoPayload> = {}): IdentityEchoPayload {
	return {
		claims: {
			sub: 'sub-alice',
			preferred_username: 'alice',
			groups: ['ssh-admins', 'platform'],
			employee_type: 'staff'
		},
		mapping: {
			subject: 'sub',
			username: 'preferred_username',
			name: 'name',
			groups: 'groups',
			email: 'email'
		},
		suggestions: [
			{
				claim: 'employee_type',
				reason: 'unmapped: nothing in the configuration reads this claim',
				yaml: 'authentication:\n  fields:\n    extra:\n      employee_type: employee_type'
			}
		],
		issued_at: '2026-09-06T10:00:00Z',
		...overrides
	};
}

/** withFragment puts an encoded payload in the URL the way the OIDC
 * callback's redirect does. */
function withFragment(payload: IdentityEchoPayload) {
	const encoded = btoa(JSON.stringify(payload))
		.replace(/\+/g, '-')
		.replace(/\//g, '_')
		.replace(/=+$/, '');
	window.location.hash = '#' + encoded;
}

// Replacing window.location is the only way to observe the redirect, and it
// has to be undone or every later test sees a plain object where jsdom's
// Location was — which silently breaks history.replaceState.
const realLocation = Object.getOwnPropertyDescriptor(window, 'location');

beforeEach(() => {
	window.location.hash = '';
});

afterEach(() => {
	vi.unstubAllGlobals();
	if (realLocation) {
		Object.defineProperty(window, 'location', realLocation);
	}
	window.location.hash = '';
});

describe('Claims echo page', () => {
	describe('before an echo has run', () => {
		it('should explain that nothing is stored', () => {
			render(Page);
			expect(screen.getByTestId('echo-explainer')).toHaveTextContent('never leaves this page');
		});

		it('should not show a result', () => {
			render(Page);
			expect(screen.queryByTestId('echo-result')).not.toBeInTheDocument();
		});

		it('should send the browser to the authorization URL when started', async () => {
			vi.stubGlobal(
				'fetch',
				vi.fn(() =>
					Promise.resolve(
						new Response(
							JSON.stringify({
								data: { authorization_url: 'https://idp.example.com/authorize?prompt=login' },
								error: null
							}),
							{ status: 200, headers: { 'Content-Type': 'application/json' } }
						)
					)
				)
			);
			Object.defineProperty(window, 'location', {
				value: { hash: '', pathname: '/admin/identity/echo', href: '' },
				writable: true,
				configurable: true
			});
			render(Page);

			await userEvent.click(screen.getByTestId('echo-start'));
			expect(window.location.href).toBe('https://idp.example.com/authorize?prompt=login');
		});
	});

	describe('when the callback hands back a token', () => {
		it('should render every claim it carried', async () => {
			withFragment(echoPayload());
			render(Page);

			const claims = await screen.findByTestId('echo-claims');
			expect(claims).toHaveTextContent('preferred_username');
			expect(claims).toHaveTextContent('employee_type');
		});

		it('should name the configured field that consumes each claim', async () => {
			withFragment(echoPayload());
			render(Page);

			expect(await screen.findByText('fields.username')).toBeInTheDocument();
		});

		// The claim the whole account is keyed by. An operator checking a
		// token has to be able to see which claim that was read from, since
		// one that varies between logins creates a new account each time.
		it('should name the field that reads the subject claim', async () => {
			withFragment(echoPayload());
			render(Page);

			expect(await screen.findByText('fields.subject')).toBeInTheDocument();
		});

		it('should mark a claim nothing reads as unmapped', async () => {
			withFragment(echoPayload());
			render(Page);

			await screen.findByTestId('echo-claims');
			expect(screen.getAllByText('unmapped').length).toBeGreaterThan(0);
		});

		it('should offer the config that would capture an unmapped claim', async () => {
			withFragment(echoPayload());
			render(Page);

			expect(await screen.findByTestId('echo-suggestions')).toHaveTextContent(
				'employee_type: employee_type'
			);
		});

		it('should say so when every claim is already read', async () => {
			withFragment(echoPayload({ suggestions: [] }));
			render(Page);
			expect(await screen.findByTestId('echo-no-suggestions')).toBeInTheDocument();
		});

		it('should clear the fragment so the claims leave the address bar', async () => {
			withFragment(echoPayload());
			render(Page);

			await screen.findByTestId('echo-claims');
			expect(window.location.hash).toBe('');
		});

		it('should report a fragment it cannot decode', async () => {
			window.location.hash = '#not-a-payload';
			render(Page);
			expect(await screen.findByTestId('echo-read-error')).toBeInTheDocument();
		});
	});
});
