import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import type { DiagnosticsResponse } from '$lib/api/types';
import { session } from '$lib/session.svelte';

import Page from './+page.svelte';

/** report is a run with one critical and one clean check. */
function report(overrides: Partial<DiagnosticsResponse> = {}): DiagnosticsResponse {
	return {
		public_origin: 'https://ssh.example',
		checks: [
			{
				id: 'cors',
				title: 'CORS / edge header attribution',
				status: 'critical',
				summary: 'The edge reflects an arbitrary Origin and allows credentials.',
				findings: ['A probe origin was echoed back.'],
				remediation: 'Stop reflecting arbitrary origins at the reverse proxy.'
			},
			{
				id: 'proxy_trust',
				title: 'Proxy trust and client IP',
				status: 'ok',
				summary: 'Client IP resolution is bounded to specific trusted proxies.',
				findings: []
			}
		],
		...overrides
	};
}

/** mockRun stubs fetch so the run endpoint returns the given report. */
function mockRun(resp: DiagnosticsResponse = report()) {
	vi.stubGlobal(
		'fetch',
		vi.fn().mockResolvedValue(
			new Response(JSON.stringify({ data: resp, error: null }), {
				status: 200,
				headers: { 'Content-Type': 'application/json' }
			})
		)
	);
}

/** signedIn puts a user in the session store, admin by default. */
function signedIn(isAdmin = true) {
	session.user = {
		subject: 'sub-alice',
		username: 'alice',
		name: 'Alice Ashworth',
		email: 'alice@corp.example',
		groups: [],
		other_accounts: [],
		service_accounts: [],
		extra: {},
		is_admin: isAdmin,
		is_soc: isAdmin,
		is_auditor: true
	};
	session.resolved = true;
}

afterEach(() => {
	vi.unstubAllGlobals();
	session.clear();
});

describe('Diagnostics admin page', () => {
	it('should tell a non-admin the section is admin-only', () => {
		signedIn(false);
		render(Page);
		expect(screen.getByText('Admin only')).toBeInTheDocument();
	});

	it('should render each check with its status once run', async () => {
		signedIn();
		mockRun();
		render(Page);

		await userEvent.click(screen.getByTestId('run-diagnostics'));

		expect(await screen.findByText('CORS / edge header attribution')).toBeInTheDocument();
		expect(screen.getByText('Critical')).toBeInTheDocument();
		expect(screen.getByText('Proxy trust and client IP')).toBeInTheDocument();
	});

	it('should show the fix only for a check that needs attention', async () => {
		signedIn();
		mockRun();
		render(Page);

		await userEvent.click(screen.getByTestId('run-diagnostics'));

		// The critical check carries its remediation; the OK one does not.
		expect(
			await screen.findByText('Stop reflecting arbitrary origins at the reverse proxy.')
		).toBeInTheDocument();
		expect(screen.getAllByText('How to fix')).toHaveLength(1);
	});
});
