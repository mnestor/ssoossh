import { render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

import type { EffectiveConfigResponse } from '$lib/api/types';
import Page from './+page.svelte';

// Test methodology: render the effective-config view against a stubbed
// fetch. The page is a read-only mirror of the server's answer, so one
// full render pins the mapping from response fields to labeled values, and
// the failure path keeps a load error from reading as an empty config.

/** effectiveConfig builds a complete auditor config answer. */
function effectiveConfig(): EffectiveConfigResponse {
	return {
		server_name: 'ssh.example.com',
		port: 8443,
		is_https: true,
		db_provider: 'postgres',
		provider_url: 'https://idp.example.com',
		admin_require_group: 'ssh-admins',
		admin_soc_group: 'ssh-soc',
		admin_auditor_group: 'ssh-auditors',
		admin_contact_email: 'admin@example.com',
		admin_disabled_message: 'Contact support',
		logging_level: 'info',
		cert_user_valid_duration: '8h0m0s',
		cert_user_extensions: ['permit-pty'],
		cert_user_require: 'group "SSH Users"',
		cert_service_valid_duration: '1h0m0s',
		cert_service_extensions: [],
		cert_service_require: '',
		cert_pam_valid_duration: '30s',
		cert_pam_require: 'claim loc >= 40',
		cert_client_timeout: '5m0s',
		cert_approval_ttl: '4m0s',
		cert_signing_grace: '30s'
	} as EffectiveConfigResponse;
}

/** stubConfig answers the admin config fetch. */
function stubConfig(config: EffectiveConfigResponse) {
	vi.stubGlobal(
		'fetch',
		vi.fn(() =>
			Promise.resolve(
				new Response(JSON.stringify({ data: config, error: null }), {
					status: 200,
					headers: { 'Content-Type': 'application/json' }
				})
			)
		)
	);
}

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('admin config page', () => {
	it('should render the effective configuration by section', async () => {
		stubConfig(effectiveConfig());

		render(Page);

		expect(await screen.findByText('ssh.example.com')).toBeInTheDocument();
		expect(screen.getByText('8443')).toBeInTheDocument();
		expect(screen.getByText('postgres')).toBeInTheDocument();
		expect(screen.getByText('https://idp.example.com')).toBeInTheDocument();
		expect(screen.getByText('ssh-auditors')).toBeInTheDocument();
	});

	it('should render the certificate policy fields', async () => {
		stubConfig(effectiveConfig());

		render(Page);

		expect(await screen.findByText('8h0m0s')).toBeInTheDocument();
		// The PAM duration and the signing grace legitimately share a value.
		expect(screen.getAllByText('30s').length).toBeGreaterThan(0);
		expect(screen.getByText('claim loc >= 40')).toBeInTheDocument();
	});

	it('should surface a load failure instead of an empty view', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(() =>
				Promise.resolve(
					new Response(JSON.stringify({ data: null, error: 'not authorized as auditor' }), {
						status: 403,
						headers: { 'Content-Type': 'application/json' }
					})
				)
			)
		);

		render(Page);

		expect(await screen.findByText('not authorized as auditor')).toBeInTheDocument();
	});
});
