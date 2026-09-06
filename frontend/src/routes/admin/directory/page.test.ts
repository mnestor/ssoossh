import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import type { LDAPProbeResponse, LDAPStatusResponse, LDAPSyncRunResponse } from '$lib/api/types';
import { session } from '$lib/session.svelte';
import Page from './+page.svelte';

/** enabledStatus is a directory that is configured and syncing. */
function enabledStatus(overrides: Partial<LDAPStatusResponse> = {}): LDAPStatusResponse {
	return {
		enabled: true,
		url: 'ldaps://dir.corp.example',
		base_dn: 'dc=corp,dc=example',
		user_filter: '(&(objectClass=person)(uid={{.Username}}))',
		configured_attributes: ['memberOf'],
		sync_interval_seconds: 900,
		disable_after_seconds: 2700,
		reenable: true,
		tls_insecure_skip_verify: false,
		running: false,
		...overrides
	};
}

/** syncRun is a completed pass. */
function syncRun(overrides: Partial<LDAPSyncRunResponse> = {}): LDAPSyncRunResponse {
	return {
		id: 'run-1',
		started_at: '2026-09-06T10:00:00Z',
		finished_at: '2026-09-06T10:00:30Z',
		trigger: 'schedule',
		dry_run: false,
		users_seen: 12,
		found: 11,
		missing: 1,
		failed: 0,
		disabled: 0,
		reenabled: 0,
		...overrides
	};
}

/** probeResult is one matched entry with a dropped group. */
function probeResult(overrides: Partial<LDAPProbeResponse> = {}): LDAPProbeResponse {
	return {
		base_dn: 'dc=corp,dc=example',
		filter_sent: '(&(objectClass=person)(uid=alice))',
		mode: 'template',
		attributes: ['*'],
		matched: 1,
		entry: {
			dn: 'uid=alice,ou=People,dc=corp,dc=example',
			attributes: [
				{ name: 'memberOf', values: ['cn=platform,dc=corp,dc=example'], configured: true },
				{ name: 'departmentNumber', values: ['4120'], configured: false }
			]
		},
		fields: [
			{
				name: 'groups',
				attribute: 'memberOf',
				attribute_present: true,
				attribute_values: ['cn=platform,dc=corp,dc=example'],
				values: ['cn=platform,dc=corp,dc=example']
			}
		],
		merge: [
			{
				name: 'groups',
				action: 'persist-groups',
				kept: ['platform'],
				dropped: ['vpn-legacy'],
				note: 'stored as user_groups rows'
			}
		],
		suggestions: [
			{
				reason: 'departmentNumber is on the entry but no configured field reads it',
				yaml: 'ldap:\n  fields:\n    department_number: departmentNumber'
			}
		],
		elapsed_ms: 34,
		timeout_ms: 5000,
		tls_insecure_skip_verify: false,
		wrote: false,
		...overrides
	};
}

/** mockApi answers the status, sync and probe calls the page makes. */
function mockApi(options: {
	status?: LDAPStatusResponse;
	run?: LDAPSyncRunResponse;
	probe?: LDAPProbeResponse;
	syncStatus?: number;
	syncError?: string;
}) {
	const calls: Array<{ url: string; body: unknown }> = [];
	vi.stubGlobal(
		'fetch',
		vi.fn((url: string, init?: RequestInit) => {
			calls.push({ url, body: init?.body ? JSON.parse(String(init.body)) : undefined });

			if (url.includes('/admin/ldap/sync')) {
				const status = options.syncStatus ?? 200;
				const body =
					status === 200
						? { data: options.run ?? syncRun(), error: null }
						: { data: null, error: options.syncError ?? 'conflict' };
				return Promise.resolve(
					new Response(JSON.stringify(body), {
						status,
						headers: { 'Content-Type': 'application/json' }
					})
				);
			}
			if (url.includes('/admin/ldap/probe')) {
				return Promise.resolve(
					new Response(JSON.stringify({ data: options.probe ?? probeResult(), error: null }), {
						status: 200,
						headers: { 'Content-Type': 'application/json' }
					})
				);
			}
			return Promise.resolve(
				new Response(JSON.stringify({ data: options.status ?? enabledStatus(), error: null }), {
					status: 200,
					headers: { 'Content-Type': 'application/json' }
				})
			);
		})
	);
	return calls;
}

/** signedInAsAdmin puts an admin in the session store, which gates the
 * sync button and the probe console. */
function signedInAsAdmin(isAdmin = true) {
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

describe('Directory admin page', () => {
	describe('when the directory is not configured', () => {
		it('should say so rather than looking broken', async () => {
			signedInAsAdmin();
			mockApi({ status: { ...enabledStatus(), enabled: false } });
			render(Page);
			expect(await screen.findByTestId('ldap-disabled')).toBeInTheDocument();
		});
	});

	describe('the sync panel', () => {
		it('should report the last pass', async () => {
			signedInAsAdmin();
			mockApi({ status: enabledStatus({ last_run: syncRun() }) });
			render(Page);

			const run = await screen.findByTestId('ldap-last-run');
			expect(run).toHaveTextContent('11 found');
		});

		it('should say when no pass has ever been recorded', async () => {
			signedInAsAdmin();
			mockApi({ status: enabledStatus() });
			render(Page);
			expect(await screen.findByTestId('ldap-no-runs')).toBeInTheDocument();
		});

		it('should warn when the scheduled sync is switched off', async () => {
			signedInAsAdmin();
			mockApi({ status: enabledStatus({ sync_interval_seconds: 0 }) });
			render(Page);
			expect(await screen.findByTestId('ldap-sync-off')).toBeInTheDocument();
		});

		it('should default to a dry run', async () => {
			signedInAsAdmin();
			mockApi({ status: enabledStatus() });
			render(Page);
			expect(await screen.findByTestId('ldap-dry-run')).toBeChecked();
		});

		it('should send the dry run flag the box is showing', async () => {
			signedInAsAdmin();
			const calls = mockApi({ status: enabledStatus() });
			render(Page);

			await userEvent.click(await screen.findByTestId('ldap-run-sync'));

			const sync = calls.find((call) => call.url.includes('/admin/ldap/sync'));
			expect(sync?.body).toEqual({ dry_run: true });
		});

		it('should send a live pass once the dry run box is cleared', async () => {
			signedInAsAdmin();
			const calls = mockApi({ status: enabledStatus() });
			render(Page);

			await userEvent.click(await screen.findByTestId('ldap-dry-run'));
			await userEvent.click(screen.getByTestId('ldap-run-sync'));

			const sync = calls.find((call) => call.url.includes('/admin/ldap/sync'));
			expect(sync?.body).toEqual({ dry_run: false });
		});

		it('should warn before a live pass that it can disable accounts', async () => {
			signedInAsAdmin();
			mockApi({ status: enabledStatus() });
			render(Page);

			await userEvent.click(await screen.findByTestId('ldap-dry-run'));
			expect(screen.getByTestId('ldap-live-warning')).toHaveTextContent('disable an account');
		});

		it('should report a pass that could not reach the directory', async () => {
			signedInAsAdmin();
			mockApi({
				status: enabledStatus(),
				run: syncRun({ error: 'directory sync could not connect: connection refused' })
			});
			render(Page);

			await userEvent.click(await screen.findByTestId('ldap-run-sync'));
			expect(await screen.findByTestId('ldap-run-error')).toHaveTextContent('connection refused');
		});

		it('should surface a refused second pass', async () => {
			signedInAsAdmin();
			mockApi({
				status: enabledStatus(),
				syncStatus: 409,
				syncError: 'a directory sync is already running on this instance'
			});
			render(Page);

			await userEvent.click(await screen.findByTestId('ldap-run-sync'));
			expect(await screen.findByTestId('ldap-sync-error')).toHaveTextContent('already running');
		});

		it('should not offer the sync to a non-admin', async () => {
			signedInAsAdmin(false);
			mockApi({ status: enabledStatus() });
			render(Page);

			await screen.findByTestId('ldap-sync-card');
			expect(screen.queryByTestId('ldap-run-sync')).not.toBeInTheDocument();
		});
	});

	describe('the probe console', () => {
		it('should not be offered to a non-admin', async () => {
			signedInAsAdmin(false);
			mockApi({ status: enabledStatus() });
			render(Page);

			await screen.findByTestId('ldap-sync-card');
			expect(screen.queryByTestId('ldap-probe-card')).not.toBeInTheDocument();
		});

		it('should warn when certificate verification is off', async () => {
			signedInAsAdmin();
			mockApi({ status: enabledStatus({ tls_insecure_skip_verify: true }) });
			render(Page);
			expect(await screen.findByTestId('ldap-insecure-tls')).toBeInTheDocument();
		});

		it('should send the typed filter and mode', async () => {
			signedInAsAdmin();
			const calls = mockApi({ status: enabledStatus() });
			render(Page);

			await userEvent.click(await screen.findByTestId('ldap-mode-literal'));
			await userEvent.type(screen.getByTestId('ldap-filter'), '(uid=alice)');
			await userEvent.click(screen.getByTestId('ldap-run-probe'));

			const probe = calls.find((call) => call.url.includes('/admin/ldap/probe'));
			expect(probe?.body).toMatchObject({ mode: 'literal', filter: '(uid=alice)' });
		});

		it('should send typed bindings when they are chosen', async () => {
			signedInAsAdmin();
			const calls = mockApi({ status: enabledStatus() });
			render(Page);

			await userEvent.click(await screen.findByTestId('ldap-binding-custom'));
			await userEvent.type(screen.getByTestId('ldap-bind-username'), 'never-logged-in');
			await userEvent.click(screen.getByTestId('ldap-run-probe'));

			const probe = calls.find((call) => call.url.includes('/admin/ldap/probe'));
			expect(probe?.body).toMatchObject({
				binding_source: 'custom',
				bindings: { username: 'never-logged-in' }
			});
		});

		it('should show the filter that was actually sent', async () => {
			signedInAsAdmin();
			mockApi({ status: enabledStatus() });
			render(Page);

			await userEvent.click(await screen.findByTestId('ldap-run-probe'));
			expect(await screen.findByTestId('ldap-probe-result')).toHaveTextContent(
				'(&(objectClass=person)(uid=alice))'
			);
		});

		it('should name the attribute the entry is anchored on', async () => {
			signedInAsAdmin();
			mockApi({
				status: enabledStatus(),
				probe: probeResult({
					id_attribute: 'entryUUID',
					directory_id: '8f14e45f-ea8f-4f2d-9c1b-3a7b5d2e6c40'
				})
			});
			render(Page);

			await userEvent.click(await screen.findByTestId('ldap-run-probe'));
			expect(await screen.findByTestId('ldap-probe-identifier')).toHaveTextContent('entryUUID');
		});

		// Unset is the state worth naming, not a blank field: without an
		// identifier a renamed entry cannot be re-anchored and reads as a
		// deleted one.
		it('should say what is lost when no identifier is configured', async () => {
			signedInAsAdmin();
			mockApi({ status: enabledStatus(), probe: probeResult({ id_attribute: '' }) });
			render(Page);

			await userEvent.click(await screen.findByTestId('ldap-run-probe'));
			expect(await screen.findByTestId('ldap-probe-identifier')).toHaveTextContent(
				'ldap.id_attribute'
			);
		});

		it('should show every attribute the directory returned', async () => {
			signedInAsAdmin();
			mockApi({ status: enabledStatus() });
			render(Page);

			await userEvent.click(await screen.findByTestId('ldap-run-probe'));
			const entry = await screen.findByTestId('ldap-panel-entry');
			expect(entry).toHaveTextContent('departmentNumber');
		});

		it('should name the group values the allowlist would drop', async () => {
			signedInAsAdmin();
			mockApi({ status: enabledStatus() });
			render(Page);

			await userEvent.click(await screen.findByTestId('ldap-run-probe'));
			await userEvent.click(await screen.findByTestId('ldap-tab-merge'));

			expect(screen.getByTestId('ldap-panel-merge')).toHaveTextContent('vpn-legacy');
		});

		it('should offer the config that would keep what is ignored', async () => {
			signedInAsAdmin();
			mockApi({ status: enabledStatus() });
			render(Page);

			await userEvent.click(await screen.findByTestId('ldap-run-probe'));
			expect(await screen.findByTestId('ldap-suggestions')).toHaveTextContent(
				'department_number: departmentNumber'
			);
		});

		it('should treat no match as an answer rather than a failure', async () => {
			signedInAsAdmin();
			mockApi({
				status: enabledStatus(),
				probe: probeResult({ matched: 0, entry: undefined, fields: [], merge: [], suggestions: [] })
			});
			render(Page);

			await userEvent.click(await screen.findByTestId('ldap-run-probe'));
			expect(await screen.findByTestId('ldap-probe-no-match')).toBeInTheDocument();
		});

		it('should say when a filter matched more than one entry', async () => {
			signedInAsAdmin();
			mockApi({ status: enabledStatus(), probe: probeResult({ matched: 3 }) });
			render(Page);

			await userEvent.click(await screen.findByTestId('ldap-run-probe'));
			expect(await screen.findByTestId('ldap-probe-result')).toHaveTextContent('too loose');
		});
	});
});
