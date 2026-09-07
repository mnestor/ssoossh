import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { fakePage, resetFakePage } from '$lib/testing/page.svelte';
import Page from './+page.svelte';

vi.mock('$app/state', async () => {
	const { fakePage: p } = await import('$lib/testing/page.svelte');
	return { page: p };
});

// The disable confirmation is the reason this page has tests: an admin has
// to be told what disabling actually does BEFORE they confirm it, with the
// real enrollment count and not placeholder copy. Stubbing fetch rather than
// the endpoints module keeps that number flowing from a server response the
// way it does in production.
const ENROLLMENTS = 3;

/** mockDetail answers the detail and audit calls this page makes. */
function mockDetail(overrides: Record<string, unknown> = {}) {
	vi.stubGlobal(
		'fetch',
		vi.fn((input: RequestInfo | URL) => {
			const url = String(input);
			// The audit timeline is a separate auditor-scoped read the page
			// makes on mount; answering it here keeps the stub matching the
			// calls production makes.
			const body = url.includes('/audit')
				? { events: [], total: 0 }
				: url.includes('/admin/config')
					? {
							admin_contact_email: 'it-help@corp.example',
							admin_disabled_message: 'Open a ticket at go/access'
						}
					: {
							id: 'user-1',
							username: 'alice',
							email: 'alice@corp.example',
							subject: 'sub-alice',
							name: 'Alice Smith',
							other_accounts: ['a.smith'],
							service_accounts: ['svc-deploy'],
							extra_fields: { employee_id: 'E-40921' },
							directory_overrides: [],
							directory_enabled: true,
							groups: [],
							// Both account fields mapped, which is what makes
							// a directory value an override rather than the
							// only source. The unmapped case is its own test.
							oidc_fields: { other_accounts: true, service_accounts: true, name: true },
							notification_preferences: [],
							created_at: '2026-08-01T10:00:00Z',
							updated_at: '2026-08-01T10:00:00Z',
							service_enrollment_count: ENROLLMENTS,
							certificate_count: 7,
							disabled_at: undefined,
							disabled_by_username: undefined,
							...overrides
						};
			return Promise.resolve(
				new Response(JSON.stringify({ data: body, error: null }), {
					status: 200,
					headers: { 'Content-Type': 'application/json' }
				})
			);
		})
	);
}

beforeEach(() => {
	resetFakePage('http://localhost/admin/users/user-1');
	fakePage.params = { id: 'user-1' };
});

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('Admin user detail', () => {
	// The heading is the person, the chip beside it is the account. Both,
	// because an admin arrives here from a username and reads the page as a
	// name.
	it('should head the page with the human-readable name', async () => {
		mockDetail();
		render(Page);
		expect(await screen.findByRole('heading', { name: 'Alice Smith' })).toBeInTheDocument();
	});

	it('should show the username beside the name', async () => {
		mockDetail();
		render(Page);
		expect(await screen.findByTestId('user-username')).toHaveTextContent('alice');
	});

	it('should fall back to the username when no name was captured', async () => {
		mockDetail({ name: '' });
		render(Page);
		expect(await screen.findByRole('heading', { name: 'alice' })).toBeInTheDocument();
	});

	it('should show an operator-configured extra field', async () => {
		mockDetail();
		render(Page);
		expect(await screen.findByText('E-40921')).toBeInTheDocument();
	});

	it('should not offer a confirmation before the admin asks for one', async () => {
		mockDetail();
		render(Page);
		await screen.findByTestId('user-username');
		expect(screen.queryByText(/Disable this account\?/)).not.toBeInTheDocument();
	});

	it('should count the enrollments the disable leaves alone', async () => {
		mockDetail();
		render(Page);
		await screen.findByTestId('user-username');

		await userEvent.click(screen.getByRole('button', { name: /^Disable$/ }));
		// The real count from the server, not fixed copy: an admin deciding
		// whether to disable someone needs to know this is three services,
		// not "some". Scoped to the consequences paragraph because the page
		// behind the modal also shows an enrollment count.
		const consequences = await screen.findByTestId('disable-consequences');
		expect(consequences).toHaveTextContent(`${ENROLLMENTS} live service enrollment`);
	});

	// The consequence that used to be here -- enrollments expiring after a
	// grace period -- is gone with group ownership. The dialog has to say the
	// opposite now, or an admin will hesitate to disable a leaver who
	// approved anything.
	it('should say the enrollments they approved keep working', async () => {
		mockDetail();
		render(Page);
		await screen.findByTestId('user-username');

		await userEvent.click(screen.getByRole('button', { name: /^Disable$/ }));
		const consequences = await screen.findByTestId('disable-consequences');
		expect(consequences).toHaveTextContent(/keep working/);
	});

	it('should say so when they approved no live enrollments', async () => {
		mockDetail({ service_enrollment_count: 0 });
		render(Page);
		await screen.findByTestId('user-username');

		await userEvent.click(screen.getByRole('button', { name: /^Disable$/ }));
		const consequences = await screen.findByTestId('disable-consequences');
		expect(consequences).toHaveTextContent(/no live service enrollments/);
	});

	it('should say the account is blocked immediately', async () => {
		mockDetail();
		render(Page);
		await screen.findByTestId('user-username');

		await userEvent.click(screen.getByRole('button', { name: /^Disable$/ }));
		expect(await screen.findByText(/immediately/)).toBeInTheDocument();
	});

	it('should close the confirmation without disabling when cancelled', async () => {
		mockDetail();
		render(Page);
		await screen.findByTestId('user-username');

		await userEvent.click(screen.getByRole('button', { name: /^Disable$/ }));
		await screen.findByText(/Disable this account\?/);
		await userEvent.click(screen.getByRole('button', { name: /Cancel/ }));

		expect(screen.queryByText(/Disable this account\?/)).not.toBeInTheDocument();
	});

	it('should report who disabled an already-disabled user', async () => {
		mockDetail({ disabled_at: '2026-08-20T09:00:00Z', disabled_by_username: 'root-admin' });
		render(Page);
		expect(await screen.findByText('root-admin')).toBeInTheDocument();
	});

	// The server requires a non-empty reason, so the button is gated rather
	// than letting the confirm fail with a 400 the admin has to interpret.
	it('should not allow confirming a disable until a reason is given', async () => {
		mockDetail();
		render(Page);
		await screen.findByTestId('user-username');

		await userEvent.click(screen.getByRole('button', { name: /^Disable$/ }));
		await screen.findByText(/Disable this account\?/);

		expect(screen.getByTestId('confirm-disable')).toBeDisabled();

		await userEvent.type(screen.getByTestId('disable-reason'), 'offboarded, SEC-1234');
		expect(screen.getByTestId('confirm-disable')).toBeEnabled();
	});

	it('should not treat a whitespace-only reason as a reason', async () => {
		mockDetail();
		render(Page);
		await screen.findByTestId('user-username');

		await userEvent.click(screen.getByRole('button', { name: /^Disable$/ }));
		await screen.findByText(/Disable this account\?/);
		await userEvent.type(screen.getByTestId('disable-reason'), '   ');

		expect(screen.getByTestId('confirm-disable')).toBeDisabled();
	});

	// The motivating case for the whole audit change: the person deciding
	// whether to re-enable has to be able to see why it was disabled.
	it('should show the recorded disable reason on a disabled account', async () => {
		mockDetail({
			disabled_at: '2026-08-20T09:00:00Z',
			disabled_by_username: 'root-admin',
			disabled_reason: 'offboarded, SEC-1234'
		});
		render(Page);
		expect(await screen.findByTestId('user-disabled-reason')).toHaveTextContent(
			'offboarded, SEC-1234'
		);
	});

	it('should require a reason before re-enabling', async () => {
		mockDetail({ disabled_at: '2026-08-20T09:00:00Z', disabled_by_username: 'root-admin' });
		render(Page);
		await screen.findByTestId('user-username');

		await userEvent.click(screen.getByTestId('enable-user'));
		await screen.findByText(/Re-enable this account\?/);

		expect(screen.getByTestId('confirm-enable')).toBeDisabled();

		await userEvent.type(screen.getByTestId('enable-reason'), 'cleared with security');
		expect(screen.getByTestId('confirm-enable')).toBeEnabled();
	});
	describe('the stored record', () => {
		it('should list every captured group membership with its source', async () => {
			mockDetail({
				groups: [
					{
						name: 'platform',
						source: 'ldap',
						first_seen_at: '2026-07-01T10:00:00Z',
						last_seen_at: '2026-09-01T10:00:00Z'
					},
					{
						name: 'ssh-users',
						source: 'oidc',
						first_seen_at: '2026-07-01T10:00:00Z',
						last_seen_at: '2026-09-01T10:00:00Z'
					}
				]
			});
			render(Page);

			const table = await screen.findByTestId('user-groups-table');
			expect(table).toHaveTextContent('platform');
			expect(table).toHaveTextContent('ssh-users');
		});

		it('should say so when no group membership has been captured', async () => {
			mockDetail({ groups: [] });
			render(Page);
			expect(await screen.findByTestId('user-groups-empty')).toBeInTheDocument();
		});

		it('should show the directory record when the user has one', async () => {
			mockDetail({
				directory: {
					dn: 'uid=alice,ou=People,dc=corp,dc=example',
					attributes: { groups: ['platform'] },
					last_seen_at: '2026-09-01T10:00:00Z',
					last_synced_at: '2026-09-01T10:15:00Z',
					consecutive_misses: 0
				}
			});
			render(Page);

			const directory = await screen.findByTestId('user-directory');
			expect(directory).toHaveTextContent('uid=alice,ou=People,dc=corp,dc=example');
		});

		it('should not show a directory record for a user who has never been enriched', async () => {
			mockDetail({ directory: undefined });
			render(Page);
			await screen.findByTestId('user-username');
			expect(screen.queryByTestId('user-directory')).not.toBeInTheDocument();
		});

		it('should name the re-anchoring identifier when one is stored', async () => {
			mockDetail({
				directory: {
					directory_id: '8f14e45f-ea8f-4f2d-9c1b-3a7b5d2e6c40',
					dn: 'uid=alice,ou=People,dc=corp,dc=example',
					attributes: {},
					consecutive_misses: 0
				}
			});
			render(Page);
			expect(await screen.findByTestId('user-directory-id')).toHaveTextContent(
				'8f14e45f-ea8f-4f2d-9c1b-3a7b5d2e6c40'
			);
		});

		// Not a cosmetic gap: with no identifier a rename cannot be
		// re-anchored and reads as a deletion, so the page says which
		// setting is missing rather than showing a blank field.
		it('should say the identifier is unconfigured when none is stored', async () => {
			mockDetail({
				directory: {
					directory_id: '',
					dn: 'uid=alice,ou=People,dc=corp,dc=example',
					attributes: {},
					consecutive_misses: 0
				}
			});
			render(Page);
			expect(await screen.findByTestId('user-directory-id')).toHaveTextContent('not configured');
		});

		it('should flag a directory entry that is currently missing', async () => {
			mockDetail({
				directory: {
					dn: 'uid=alice,ou=People,dc=corp,dc=example',
					attributes: {},
					last_seen_at: '2026-09-01T10:00:00Z',
					last_synced_at: '2026-09-01T10:15:00Z',
					first_missing_at: '2026-09-01T10:05:00Z',
					consecutive_misses: 3
				}
			});
			render(Page);
			expect(await screen.findByTestId('user-directory-missing')).toBeInTheDocument();
		});

		it('should name what disabled the account', async () => {
			mockDetail({
				disabled_at: '2026-08-20T09:00:00Z',
				disabled_reason: 'directory entry not found',
				disabled_source: 'ldap_sync'
			});
			render(Page);
			expect(await screen.findByTestId('user-disabled-source')).toHaveTextContent(
				'the directory sync'
			);
		});

		// The server sends the whole catalogue, not just the stored rows: a
		// list of raw kind strings left the reader to guess what each one
		// was, and an empty list read as "we send them nothing" when it
		// meant the opposite.
		it('should name each notification in the words the preferences page uses', async () => {
			mockDetail({
				notification_preferences: [
					{
						kind: 'service_enrollment_expiring',
						title: 'Service enrollment expiring',
						description: 'Sent while one of your enrollment codes is close to expiring.',
						enabled: true,
						default: true,
						explicit: false,
						registered: true
					}
				]
			});
			render(Page);

			expect(await screen.findByTestId('user-notification-preferences')).toHaveTextContent(
				'Service enrollment expiring'
			);
		});

		// The registry's sentence explaining when a kind fires belongs on
		// the preferences page, where someone is deciding. Here it was seven
		// paragraphs between an admin and the two facts they came for.
		it('should not repeat the registry description for every kind', async () => {
			mockDetail({
				notification_preferences: [
					{
						kind: 'service_enrollment_expiring',
						title: 'Service enrollment expiring',
						description: 'Sent while one of your enrollment codes is close to expiring.',
						enabled: true,
						default: true,
						explicit: false,
						registered: true
					}
				]
			});
			render(Page);

			await screen.findByTestId('user-notification-preferences');
			expect(screen.queryByText(/close to expiring/)).not.toBeInTheDocument();
		});

		it('should still show the raw kind alongside the readable name', async () => {
			mockDetail({
				notification_preferences: [
					{
						kind: 'service_enrollment_expiring',
						title: 'Service enrollment expiring',
						description: '',
						enabled: true,
						default: true,
						explicit: false,
						registered: true
					}
				]
			});
			render(Page);
			expect(
				await screen.findByTestId('user-notification-service_enrollment_expiring')
			).toHaveTextContent('service_enrollment_expiring');
		});

		it('should mark a kind the user has never touched as its default', async () => {
			mockDetail({
				notification_preferences: [
					{
						kind: 'user_certificate_issued',
						title: 'User certificate issued',
						description: '',
						enabled: false,
						default: false,
						explicit: false,
						registered: true
					}
				]
			});
			render(Page);
			expect(
				await screen.findByTestId('user-notification-user_certificate_issued')
			).toHaveTextContent('default');
		});

		// A date in the Changed column is the whole signal that this row is
		// the person's own decision rather than the registered default.
		it('should date a choice the user made themselves', async () => {
			mockDetail({
				notification_preferences: [
					{
						kind: 'service_enrollment_expiring',
						title: 'Service enrollment expiring',
						description: '',
						enabled: false,
						default: true,
						explicit: true,
						registered: true,
						updated_at: '2026-09-01T10:00:00Z'
					}
				]
			});
			render(Page);

			const row = await screen.findByTestId('user-notification-service_enrollment_expiring');
			expect(row).toHaveTextContent(new Date('2026-09-01T10:00:00Z').toLocaleDateString());
			expect(row).not.toHaveTextContent('default');
		});

		// A stored row for a kind this build no longer has. Shown rather
		// than dropped: the choice is real, still on disk, and comes back if
		// the kind does.
		it('should keep a stored choice for a kind the server no longer sends', async () => {
			mockDetail({
				notification_preferences: [
					{
						kind: 'enrollment_expiring',
						title: '',
						description: '',
						enabled: true,
						default: false,
						explicit: true,
						registered: false,
						updated_at: '2026-09-01T10:00:00Z'
					}
				]
			});
			render(Page);

			const row = await screen.findByTestId('user-notification-enrollment_expiring');
			expect(row).toHaveTextContent('enrollment_expiring');
			expect(screen.getByTestId('user-notification-retired')).toBeInTheDocument();
		});

		// Only reachable on a server that registers no notification kinds at
		// all; the section has nothing to say then.
		it('should hide the notification block when the server lists no kinds', async () => {
			mockDetail({ notification_preferences: [] });
			render(Page);
			await screen.findByTestId('user-username');
			expect(screen.queryByTestId('user-notification-preferences')).not.toBeInTheDocument();
		});
	});
});

describe('the OIDC record and what the directory overrides', () => {
	it('should label the OIDC capture as its own record', async () => {
		mockDetail();
		render(Page);
		expect(await screen.findByTestId('user-oidc-record')).toHaveTextContent('OIDC record');
	});

	// The whole point of showing both sides. The OIDC value is what the
	// operator wrote a claim mapping for; the directory value is what the
	// server acts on. Showing only the winner makes a claim mapping that is
	// quietly wrong invisible.
	it('should show the OIDC value alongside the directory value that replaced it', async () => {
		mockDetail({
			other_accounts: ['a.smith'],
			directory_overrides: [
				{ field: 'other_accounts', oidc: ['a.smith'], effective: ['alice.adm', 'alice.root'] }
			]
		});
		render(Page);

		const block = await screen.findByTestId('user-oidc-other_accounts');
		expect(block).toHaveTextContent('a.smith');
		expect(block).toHaveTextContent('alice.adm');
	});

	// A source badge rather than a sentence: the group table on the same
	// page already answers "where did this come from" with the same chip,
	// and the field's own values are what the reader came for.
	it('should badge a directory-supplied field as coming from LDAP', async () => {
		mockDetail({
			directory_overrides: [
				{ field: 'service_accounts', oidc: ['svc-deploy'], effective: ['svc-prod'] }
			]
		});
		render(Page);
		expect(await screen.findByTestId('user-account-source-service_accounts')).toHaveTextContent(
			'ldap'
		);
	});

	it('should badge an unoverridden field as coming from OIDC', async () => {
		mockDetail({ service_accounts: ['svc-deploy'], directory_overrides: [] });
		render(Page);
		expect(await screen.findByTestId('user-account-source-service_accounts')).toHaveTextContent(
			'oidc'
		);
	});

	// The effective list leads. Leading with the OIDC capture struck
	// through and burying what the server acts on in a callout underneath
	// is backwards -- the effective list is the answer.
	it('should lead with the values the server acts on', async () => {
		mockDetail({
			directory_overrides: [
				{ field: 'service_accounts', oidc: ['svc-deploy'], effective: ['svc-prod'] }
			]
		});
		render(Page);
		expect(await screen.findByTestId('user-oidc-service_accounts')).toHaveTextContent('svc-prod');
	});

	// A configured claim that arrived empty while the directory supplied
	// values is the exact state someone is diagnosing when they ask why a
	// principal is missing, so it is said rather than left to be inferred
	// from an absence.
	it('should say when a configured claim supplied nothing and the directory won', async () => {
		mockDetail({
			other_accounts: [],
			directory_overrides: [{ field: 'other_accounts', oidc: [], effective: ['alice.adm'] }]
		});
		render(Page);
		expect(await screen.findByTestId('user-override-other_accounts')).toHaveTextContent(
			'The OIDC claim supplied nothing'
		);
	});

	it('should mark an overridden extra field', async () => {
		mockDetail({
			extra_fields: { employee_id: 'E-40921' },
			directory_overrides: [{ field: 'employee_id', oidc: ['E-40921'], effective: ['E-99999'] }]
		});
		render(Page);
		expect(await screen.findByTestId('user-override-employee_id')).toHaveTextContent('E-99999');
	});

	// A field the directory supplies that the ID token never carried is
	// still an override in the sense that matters: the server acts on a
	// value with no OIDC side at all.
	it('should show a field the directory supplies and OIDC never did', async () => {
		mockDetail({
			extra_fields: {},
			directory_overrides: [{ field: 'cost_center', oidc: [], effective: ['CC-7781'] }]
		});
		render(Page);
		expect(await screen.findByTestId('user-override-cost_center')).toHaveTextContent('CC-7781');
	});

	// Both account fields default to empty in authentication.fields, so the
	// common deployment populates neither from OIDC. Calling the directory
	// value an override there names a conflict that does not exist, and
	// "None in the ID token" sends someone to check a claim mapping that was
	// never configured.
	describe('a field with no configured OIDC claim', () => {
		it('should say the field is not read from OIDC rather than that the claim was empty', async () => {
			mockDetail({
				other_accounts: [],
				oidc_fields: { other_accounts: false, service_accounts: false, name: true }
			});
			render(Page);

			const block = await screen.findByTestId('user-oidc-unmapped-other_accounts');
			expect(block).toHaveTextContent('authentication.fields.other_accounts');
			expect(screen.queryByText('None.')).not.toBeInTheDocument();
		});

		// With no claim configured there is no losing side to report. The
		// badge has already said the directory is where this came from, so
		// a note would be saying it twice.
		it('should report no losing side when no claim was ever configured', async () => {
			mockDetail({
				other_accounts: [],
				oidc_fields: { other_accounts: false, service_accounts: false, name: true },
				directory_overrides: [{ field: 'other_accounts', oidc: [], effective: ['alice.adm'] }]
			});
			render(Page);

			await screen.findByTestId('user-oidc-other_accounts');
			expect(screen.getByTestId('user-account-source-other_accounts')).toHaveTextContent('ldap');
			expect(screen.queryByTestId('user-override-other_accounts')).not.toBeInTheDocument();
		});

		it('should name what the directory replaced when the claim carried a value', async () => {
			mockDetail({
				other_accounts: ['a.smith'],
				oidc_fields: { other_accounts: true, service_accounts: true, name: true },
				directory_overrides: [
					{ field: 'other_accounts', oidc: ['a.smith'], effective: ['alice.adm'] }
				]
			});
			render(Page);

			expect(await screen.findByTestId('user-override-other_accounts')).toHaveTextContent(
				'LDAP replaced a.smith from OIDC'
			);
		});

		it('should describe an unmapped name as supplied rather than overridden', async () => {
			mockDetail({
				name: '',
				oidc_fields: { other_accounts: true, service_accounts: true, name: false },
				directory_overrides: [{ field: 'name', oidc: [], effective: ['Alice R. Smith'] }]
			});
			render(Page);

			expect(await screen.findByTestId('user-name-overridden')).toHaveTextContent(
				'Supplied by LDAP'
			);
		});

		// The conservative reading: an unknown field keeps the fuller
		// explanation rather than silently losing one.
		it('should treat a field the server said nothing about as configured', async () => {
			mockDetail({
				extra_fields: { employee_id: 'E-40921' },
				oidc_fields: { other_accounts: true, service_accounts: true, name: true },
				directory_overrides: [{ field: 'employee_id', oidc: ['E-40921'], effective: ['E-99999'] }]
			});
			render(Page);

			expect(await screen.findByTestId('user-override-employee_id')).toHaveTextContent(
				'Overridden by LDAP'
			);
		});
	});

	it('should mark the name as overridden by the directory', async () => {
		mockDetail({
			name: 'Alice Smith',
			directory_overrides: [{ field: 'name', oidc: ['Alice Smith'], effective: ['Alice R. Smith'] }]
		});
		render(Page);
		expect(await screen.findByTestId('user-name-overridden')).toHaveTextContent('Alice R. Smith');
	});

	// The server withholds the directory row while ldap.enabled is false,
	// so the page has to explain the absence rather than let an operator
	// conclude the memberships were deleted.
	it('should explain that directory rows are withheld when the directory is off', async () => {
		mockDetail({ directory_enabled: false, directory: undefined });
		render(Page);
		expect(await screen.findByTestId('user-groups-oidc-only')).toBeInTheDocument();
	});

	// The other absence: the directory is on, this person has simply never
	// resolved. Nothing is being withheld, so saying so would be wrong.
	it('should not claim rows are withheld when the directory is on', async () => {
		mockDetail({ directory_enabled: true, directory: undefined });
		render(Page);
		await screen.findByTestId('user-username');
		expect(screen.queryByTestId('user-groups-oidc-only')).not.toBeInTheDocument();
	});
});
