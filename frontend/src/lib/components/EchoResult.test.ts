import { render, screen, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import type { IdentityEchoPayload } from '#lib/api/types.js';
import EchoResult from './EchoResult.svelte';

/** payload is one echo: a username that does not resolve, a groups list
 * UserInfo filled, a nested claim and an access token claim. */
function payload(overrides: Partial<IdentityEchoPayload> = {}): IdentityEchoPayload {
	return {
		claims: {
			sub: 'sub-alice',
			upn: 'alice@example.com',
			groups: ['ssh-admins', 'platform'],
			iss: 'https://idp.example.com',
			realm_access: { roles: ['admin'] }
		},
		claim_sources: {
			sub: 'id_token',
			upn: 'id_token',
			groups: 'userinfo',
			iss: 'id_token',
			realm_access: 'id_token'
		},
		userinfo: 'used',
		mapping: { subject: 'sub', username: 'preferred_username', groups: 'groups' },
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
				status: 'missing',
				list: false
			},
			{
				field: 'groups',
				claim: 'groups',
				required: false,
				status: 'resolved',
				list: true,
				values: ['ssh-admins', 'platform']
			}
		],
		sign_in: {
			allowed: false,
			reason: 'OIDC ID token is missing the configured username claim "preferred_username"'
		},
		suggestions: [
			{
				field: 'username',
				claim: 'upn',
				reason: 'a string',
				yaml: 'authentication:\n  fields:\n    username: upn'
			},
			{
				field: 'extra',
				claim: 'upn',
				reason: 'unmapped',
				yaml: 'authentication:\n  fields:\n    extra:\n      upn: upn'
			}
		],
		admin_group_candidates: ['ssh-admins', 'platform'],
		not_mappable: [
			{
				claim: 'realm_access',
				source: 'id_token',
				value: { roles: ['admin'] },
				reason: 'an object'
			},
			{ claim: 'roles', source: 'access_token', value: ['ops'], reason: 'in the access token' }
		],
		protocol_claims: ['iss'],
		issued_at: '2026-10-21T09:00:00Z',
		...overrides
	};
}

afterEach(() => {
	vi.unstubAllGlobals();
	vi.restoreAllMocks();
});

describe('EchoResult', () => {
	it('should say when a login with the token would fail, and why', () => {
		render(EchoResult, { payload: payload() });
		expect(screen.getByTestId('echo-sign-in-verdict')).toHaveTextContent('preferred_username');
	});

	it('should say when a login with the token would succeed', () => {
		render(EchoResult, { payload: payload({ sign_in: { allowed: true } }) });
		expect(screen.getByTestId('echo-sign-in-verdict')).toHaveTextContent('Yes.');
	});

	it('should say what UserInfo added', () => {
		render(EchoResult, { payload: payload() });
		expect(screen.getByTestId('echo-userinfo')).toHaveTextContent('filled 1 claim');
	});

	it('should give each mapped field its status in words', () => {
		render(EchoResult, { payload: payload() });
		const rows = within(screen.getByTestId('echo-mapping-rows')).getAllByRole('row');
		expect(rows.map((r) => r.textContent)).toEqual([
			expect.stringContaining('Resolved'),
			expect.stringContaining('Missing'),
			expect.stringContaining('Resolved')
		]);
	});

	it('should name the source of each claim', () => {
		render(EchoResult, { payload: payload() });
		const groups = within(screen.getByTestId('echo-claims'))
			.getByText('groups')
			.closest('tr') as HTMLElement;
		expect(groups).toHaveTextContent('UserInfo');
	});

	it('should group the protocol claims apart', () => {
		render(EchoResult, { payload: payload() });
		expect(within(screen.getByTestId('echo-protocol-claims')).getByText('iss')).toBeInTheDocument();
	});

	it('should keep the protocol claims out of the identity table', () => {
		render(EchoResult, { payload: payload() });
		expect(within(screen.getByTestId('echo-claims')).queryByText('iss')).not.toBeInTheDocument();
	});

	it('should put the username candidate into the YAML', () => {
		render(EchoResult, { payload: payload() });
		expect(screen.getByTestId('echo-yaml-text')).toHaveTextContent('username: upn');
	});

	it('should add the admin group chosen to the YAML', async () => {
		render(EchoResult, { payload: payload() });
		await userEvent.selectOptions(screen.getByTestId('echo-admin-group'), 'platform');
		expect(screen.getByTestId('echo-yaml-text')).toHaveTextContent('require_group: platform');
	});

	it('should say there is no group to pick when groups does not resolve', () => {
		render(EchoResult, { payload: payload({ admin_group_candidates: [] }) });
		expect(screen.getByTestId('echo-no-admin-groups')).toBeInTheDocument();
	});

	it('should copy the YAML to the clipboard', async () => {
		const writeText = vi.fn(() => Promise.resolve());
		vi.stubGlobal('navigator', { clipboard: { writeText } });
		render(EchoResult, { payload: payload() });

		await userEvent.click(screen.getByTestId('echo-copy-yaml'));
		expect(writeText).toHaveBeenCalledWith(expect.stringContaining('username: upn'));
	});

	it('should say so when the clipboard refuses', async () => {
		vi.stubGlobal('navigator', {
			clipboard: { writeText: () => Promise.reject(new Error('denied')) }
		});
		render(EchoResult, { payload: payload() });

		await userEvent.click(screen.getByTestId('echo-copy-yaml'));
		expect(screen.getByTestId('echo-copy-status')).toHaveTextContent('Copy failed');
	});

	it('should build the downloaded file in the browser without a server request', async () => {
		const fetchSpy = vi.fn();
		vi.stubGlobal('fetch', fetchSpy);
		const createObjectURL = vi.fn(() => 'blob:echo');
		vi.stubGlobal('URL', Object.assign(URL, { createObjectURL, revokeObjectURL: vi.fn() }));
		vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});
		render(EchoResult, { payload: payload() });

		await userEvent.click(screen.getByTestId('echo-download'));
		expect({
			objectURLs: createObjectURL.mock.calls.length,
			requests: fetchSpy.mock.calls.length
		}).toEqual({
			objectURLs: 1,
			requests: 0
		});
	});

	it('should label an access token claim as unverified and unread', () => {
		render(EchoResult, { payload: payload() });
		expect(screen.getByTestId('echo-not-mappable')).toHaveTextContent(
			'Access token (unverified, not read by ssoossh)'
		);
	});

	it('should leave out the not mappable section when there is nothing in it', () => {
		render(EchoResult, { payload: payload({ not_mappable: [] }) });
		expect(screen.queryByTestId('echo-not-mappable')).not.toBeInTheDocument();
	});
});
