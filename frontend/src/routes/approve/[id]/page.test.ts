import { render, screen, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// The page reads its id from the route params and, on a failed load, decides
// between redirecting to /login and rendering an explanation. Neither a
// router nor a real navigation belongs in a component test, so both are
// stubbed.
// vi.hoisted: vi.mock is lifted above the imports, so the state the factories
// close over has to be created up there with them.
const { pageState, redirectIfUnauthenticated } = vi.hoisted(() => ({
	pageState: { params: { id: 'req-1' } },
	redirectIfUnauthenticated: vi.fn()
}));

vi.mock('$app/state', () => ({ page: pageState }));
vi.mock('#lib/auth.js', async () => {
	const actual = await vi.importActual<typeof import('#lib/auth.js')>('#lib/auth.js');
	return { ...actual, redirectIfUnauthenticated };
});

import type { CurrentUser } from '#lib/api/types.js';
import { session } from '#lib/session.svelte.js';
import { detail } from '#lib/detail.svelte.js';

import Page from './+page.svelte';

/** mockFetch stubs the global fetch with one enveloped response. */
function mockFetch(status: number, body: object = {}) {
	vi.stubGlobal(
		'fetch',
		vi.fn(() =>
			Promise.resolve(
				new Response(JSON.stringify({ data: body, error: status === 200 ? null : 'denied' }), {
					status,
					headers: { 'Content-Type': 'application/json' }
				})
			)
		)
	);
}

/** settle lets the load effect's promise chain run to completion. */
function settle(): Promise<void> {
	return new Promise((resolve) => setTimeout(resolve, 0));
}

/** signedInAs builds a CurrentUser, defaulting everything the picker ignores. */
function signedInAs(overrides: Partial<CurrentUser> = {}): CurrentUser {
	return {
		subject: 'sub-alice',
		username: 'alice',
		name: 'Alice Ashworth',
		email: 'alice@example.com',
		groups: [],
		other_accounts: [],
		service_accounts: [],
		approvable_service_accounts: [],
		user_own_service_accounts: [],
		extra: {},
		is_admin: false,
		is_soc: false,
		is_auditor: false,
		detail_level: 'advanced',
		...overrides
	};
}

/**
 * approveBody returns the decoded JSON body of the approve POST. The client
 * serializes with JSON.stringify, so the recorded body is a string — matching
 * it as an object silently never matches.
 */
function approveBody(): unknown {
	const calls = vi.mocked(globalThis.fetch).mock.calls;
	const approve = calls.find(([url]) => String(url).includes('/approve'));
	if (!approve) throw new Error('no approve request was sent');
	return JSON.parse(String((approve[1] as RequestInit).body));
}

beforeEach(() => {
	redirectIfUnauthenticated.mockReset();
});

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('Approval page', () => {
	describe('when the visitor is not signed in', () => {
		beforeEach(() => {
			mockFetch(401);
			// The real helper navigates; here it only reports that it would,
			// which is what the page branches on.
			redirectIfUnauthenticated.mockReturnValue(true);
		});

		// The bug this guards: a certificate request URL is how most people
		// first reach this app, and sending them to the identity provider
		// from here skipped /login — and with it the consent notice /login
		// is what shows.
		it('should send an unauthenticated visitor to the login screen', async () => {
			render(Page);
			await settle();
			expect(redirectIfUnauthenticated).toHaveBeenCalled();
		});

		it('should not render its own sign-in prompt', async () => {
			render(Page);
			await settle();
			expect(screen.queryByTestId('sign-in-button')).not.toBeInTheDocument();
		});

		it('should not render a load failure it is redirecting away from', async () => {
			render(Page);
			await settle();
			expect(screen.queryByText('Could not load this request')).not.toBeInTheDocument();
		});
	});

	describe('when the request belongs to someone else', () => {
		beforeEach(() => {
			mockFetch(403);
			redirectIfUnauthenticated.mockReturnValue(false);
		});

		it('should explain that another account claimed it', async () => {
			render(Page);
			await settle();
			expect(await screen.findByTestId('load-failure-forbidden')).toBeInTheDocument();
		});

		it('should not offer to sign in as a way out', async () => {
			render(Page);
			await settle();
			expect(screen.queryByTestId('sign-in-button')).not.toBeInTheDocument();
		});

		// A claimed request cannot be fixed by asking again, so the way out
		// leads back into the app instead of offering a retry.
		it('should lead back to the dashboard', async () => {
			render(Page);
			await settle();
			expect(await screen.findByTestId('load-failure-dashboard')).toBeInTheDocument();
		});
	});

	describe('when the server fails to answer', () => {
		beforeEach(() => {
			mockFetch(500);
			redirectIfUnauthenticated.mockReturnValue(false);
		});

		it('should offer Try again', async () => {
			render(Page);
			await settle();
			expect(await screen.findByTestId('load-failure-retry')).toBeInTheDocument();
		});

		it('should fetch the request again when Try again is pressed', async () => {
			render(Page);
			await settle();
			const calls = vi.mocked(fetch).mock.calls.length;

			await userEvent.click(await screen.findByTestId('load-failure-retry'));
			await settle();

			expect(vi.mocked(fetch).mock.calls.length).toBeGreaterThan(calls);
		});
	});

	describe('when the request no longer exists', () => {
		beforeEach(() => {
			mockFetch(404);
			redirectIfUnauthenticated.mockReturnValue(false);
		});

		it('should report it as a missing request', async () => {
			render(Page);
			await settle();
			expect(await screen.findByTestId('load-failure-not-found')).toBeInTheDocument();
		});
	});

	// session is a plain exported singleton with $state fields, so these set
	// it directly rather than mocking the module. principals: [] on the
	// detail keeps the read-only Principals row from duplicating the names
	// the picker renders.
	describe('principal selection for user certificates', () => {
		beforeEach(() => {
			mockFetch(200, {
				id: 'req-1',
				type: 'user',
				status: 'pending',
				source_ip: '198.51.100.7',
				public_key: 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5',
				principals: [],
				valid_seconds: 28800,
				requested: { extensions: [], no_touch_required: false },
				granted: { extensions: [], no_touch_required: false },
				created_at: '2026-08-14T09:00:00Z',
				approval_url: '/approve/req-1',
				is_owned_by_you: true,
				already_closed: false
			});
			redirectIfUnauthenticated.mockReturnValue(false);
		});

		afterEach(() => {
			session.user = null;
		});

		/** principalBoxes returns the picker's chip names, in render order. */
		function principalBoxes(): string[] {
			const group = screen.getByRole('group', { name: 'Principals to include' });
			return within(group)
				.getAllByRole('button')
				.map((chip) => chip.textContent?.trim() ?? '');
		}

		/** chip returns the toggle chip for principal. */
		function chip(principal: string) {
			return screen.getByRole('button', { name: principal });
		}

		it('should list the username before the approver other accounts', async () => {
			session.user = signedInAs({ other_accounts: ['alice-alt', 'alice-service'] });

			render(Page);
			await settle();

			expect(principalBoxes()).toEqual(['alice', 'alice-alt', 'alice-service']);
		});

		it('should drop a duplicate when an other account repeats the username', async () => {
			session.user = signedInAs({ other_accounts: ['alice-alt', 'alice'] });

			render(Page);
			await settle();

			expect(principalBoxes()).toEqual(['alice', 'alice-alt']);
		});

		it('should pre-check the username so the default matches the old behaviour', async () => {
			session.user = signedInAs({ other_accounts: ['alice-alt'] });

			render(Page);
			await settle();

			expect(chip('alice')).toHaveAttribute('aria-pressed', 'true');
		});

		/** pamRequest mocks a pending PAM request acting as target. */
		function pamRequest(target: string) {
			mockFetch(200, {
				id: 'req-1',
				type: 'pam',
				status: 'pending',
				source_ip: '198.51.100.7',
				public_key: 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5',
				principals: [],
				target_account: target,
				valid_seconds: 30,
				requested: { extensions: [], no_touch_required: false },
				granted: { extensions: [], no_touch_required: false },
				created_at: '2026-08-14T09:00:00Z',
				approval_url: '/approve/req-1',
				is_owned_by_you: true,
				already_closed: false
			});
		}

		// The host matches the certificate's principals against the account
		// being acted as, so that account is the default, and the only one:
		// the username is not added alongside it.
		it('should default a PAM request to the account it acts as when the approver holds it', async () => {
			pamRequest('alice-alt');
			session.user = signedInAs({ other_accounts: ['alice-alt', 'alice-service'] });

			render(Page);
			await settle();

			expect(chip('alice')).toHaveAttribute('aria-pressed', 'false');
			expect(chip('alice-alt')).toHaveAttribute('aria-pressed', 'true');
			expect(chip('alice-service')).toHaveAttribute('aria-pressed', 'false');
		});

		it('should fall back to the username when the approver does not hold the account acted as', async () => {
			pamRequest('root');
			session.user = signedInAs({ other_accounts: ['alice-alt'] });

			render(Page);
			await settle();

			expect(chip('alice')).toHaveAttribute('aria-pressed', 'true');
			expect(chip('alice-alt')).toHaveAttribute('aria-pressed', 'false');
		});

		it('should send the selected principals under the snake_case wire name', async () => {
			session.user = signedInAs({ other_accounts: ['alice-alt'] });

			render(Page);
			await settle();

			await userEvent.click(chip('alice-alt'));
			await userEvent.click(screen.getByTestId('approve-button'));
			await settle();

			expect(approveBody()).toEqual({ principals: ['alice', 'alice-alt'] });
		});
	});
});

describe('at the simplified detail level', () => {
	/** pamRequest is a pending sudo request, overridable per case. */
	function pamRequest(overrides: Record<string, unknown> = {}) {
		return {
			id: 'req-pam',
			type: 'pam',
			status: 'pending',
			source_ip: '10.0.0.5',
			public_key: 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5',
			principals: [],
			valid_seconds: 30,
			requested: { extensions: [], no_touch_required: false },
			granted: { extensions: [], no_touch_required: false },
			created_at: '2026-08-14T09:00:00Z',
			expires_at: new Date(Date.now() + 10 * 60 * 1000).toISOString(),
			approval_url: '/approve/req-pam',
			is_owned_by_you: true,
			already_closed: false,
			target_account: 'root',
			hostname: 'web01',
			process: 'systemctl restart nginx',
			pam_service: 'sudo',
			tty: 'pts/3',
			client: 'pam_ssoossh-c/0.3.0',
			caller_pid: 4242,
			...overrides
		};
	}

	/** renderSimplified opens a request at the simplified level. */
	async function renderSimplified(overrides: Record<string, unknown> = {}) {
		vi.stubGlobal('localStorage', { getItem: () => null, setItem: () => undefined });
		detail.level = 'simplified';
		mockFetch(200, pamRequest(overrides));
		redirectIfUnauthenticated.mockReturnValue(false);
		render(Page);
		await screen.findByTestId('approval-view');
	}

	afterEach(() => {
		detail.level = 'advanced';
	});

	it('should open with who asked', async () => {
		await renderSimplified();
		expect(screen.getByTestId('request-line')).toHaveTextContent('root');
	});

	// The address the server saw is not a claim, so it is not filed under the
	// tiles' "never verified" caption; it sits with the check by the buttons.
	it('should keep the server-observed address out of the claimed tiles', async () => {
		await renderSimplified();
		expect(screen.getByTestId('request-line')).not.toHaveTextContent('10.0.0.5');
	});

	it('should show the server-observed address beside the buttons', async () => {
		await renderSimplified();
		expect(screen.getByTestId('approve-addresses')).toHaveTextContent('10.0.0.5');
	});

	it('should say what the request runs', async () => {
		await renderSimplified();
		expect(screen.getByTestId('request-line')).toHaveTextContent('systemctl restart nginx');
	});

	it('should say which machine it is on', async () => {
		await renderSimplified();
		expect(screen.getByTestId('request-line')).toHaveTextContent('web01');
	});

	it('should drop the request id', async () => {
		await renderSimplified();
		expect(screen.queryByTestId('request-id')).not.toBeInTheDocument();
	});

	// R1: the claimed service stays.
	it('should keep the claimed service', async () => {
		await renderSimplified();
		expect(screen.getByText('sudo')).toBeInTheDocument();
	});

	it('should drop the process ids', async () => {
		await renderSimplified();
		expect(screen.queryByText(/pid 4242/)).not.toBeInTheDocument();
	});

	it('should drop the reporting client', async () => {
		await renderSimplified();
		expect(screen.queryByText('pam_ssoossh-c/0.3.0')).not.toBeInTheDocument();
	});

	it('should keep the decision itself', async () => {
		await renderSimplified();
		expect(screen.getByTestId('approve-button')).toBeInTheDocument();
	});

	it('should not count down while more than a minute is left', async () => {
		await renderSimplified();
		expect(screen.queryByTestId('approval-countdown')).not.toBeInTheDocument();
	});

	// "Approvable until" is advanced-only except in its last minute.
	it('should count down beside the buttons in the last minute', async () => {
		await renderSimplified({ expires_at: new Date(Date.now() + 40 * 1000).toISOString() });
		expect(screen.getByTestId('approval-countdown')).toHaveTextContent(/Expires in 0:\d\d/);
	});

	it('should give an expired request its own notice', async () => {
		await renderSimplified({ status: 'expired', already_closed: true });
		expect(screen.getByTestId('expired-notice')).toHaveTextContent('Run the client again');
	});

	// R3: who decided is history; the certificate page keeps it.
	it('should drop the decision record', async () => {
		await renderSimplified({
			status: 'approved',
			already_closed: true,
			decided_at: '2026-08-14T09:01:00Z',
			decided_by_outcome: 'approved',
			decided_by_username: 'alice'
		});
		expect(screen.queryByText('Decision record')).not.toBeInTheDocument();
	});

	// R3: the pam-headless note is never hidden.
	it('should keep the note that a request looks unattended', async () => {
		await renderSimplified({ tty: undefined });
		expect(screen.getByTestId('pam-headless-note')).toBeInTheDocument();
	});

	it('should say what it left out', async () => {
		await renderSimplified();
		expect(screen.getByTestId('detail-hint')).toBeInTheDocument();
	});
});

// A trimmed critical option changes what the certificate does, so the
// simplified page shows the Critical options list whenever one is trimmed,
// struck through like a trimmed extension, and drops it otherwise.
describe('critical options at the simplified level', () => {
	/** renderCritical opens a user request at the simplified level with the
	 * given requested and granted critical options. */
	async function renderCritical(
		requested: Record<string, unknown>,
		granted: Record<string, unknown>
	) {
		vi.stubGlobal('localStorage', { getItem: () => null, setItem: () => undefined });
		detail.level = 'simplified';
		mockFetch(200, {
			id: 'req-1',
			type: 'user',
			status: 'pending',
			source_ip: '198.51.100.7',
			public_key: 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5',
			principals: [],
			valid_seconds: 28800,
			requested: { extensions: [], no_touch_required: false, ...requested },
			granted: { extensions: [], no_touch_required: false, ...granted },
			created_at: '2026-08-14T09:00:00Z',
			approval_url: '/approve/req-1',
			is_owned_by_you: true,
			already_closed: false
		});
		redirectIfUnauthenticated.mockReturnValue(false);
		render(Page);
		await screen.findByTestId('approval-view');
	}

	/** One requested force-command trimmed, one no-touch-required granted. */
	const narrowed = (): Promise<void> =>
		renderCritical(
			{ force_command: 'backup.sh', no_touch_required: true },
			{ no_touch_required: true }
		);

	afterEach(() => {
		detail.level = 'advanced';
	});

	it('should show the Critical options section when one is trimmed', async () => {
		await narrowed();
		expect(screen.getByText('Critical options')).toBeInTheDocument();
	});

	it('should list the trimmed critical option struck through', async () => {
		await narrowed();
		expect(screen.getByTestId('option-trimmed')).toHaveTextContent('force-command: backup.sh');
	});

	it('should list the granted critical option beside it', async () => {
		await narrowed();
		expect(screen.getByTestId('option-granted')).toHaveTextContent('no-touch-required');
	});

	it('should warn that less than was requested', async () => {
		await narrowed();
		expect(screen.getByTestId('narrowed-warning')).toBeInTheDocument();
	});

	it('should drop the Critical options section when nothing is trimmed', async () => {
		await renderCritical({ force_command: 'backup.sh' }, { force_command: 'backup.sh' });
		expect(screen.queryByText('Critical options')).not.toBeInTheDocument();
	});

	// The requested source_addresses is the client's own interfaces plus the
	// observed address, sent on every request; it is not a request for the
	// option and must not read as one the server removed.
	it('should not strike through a source-address nobody asked for', async () => {
		await renderCritical({ source_addresses: ['192.0.2.10', '198.51.100.7'] }, {});
		expect(screen.queryByTestId('option-trimmed')).not.toBeInTheDocument();
	});

	it('should not warn that less was requested over the reported addresses', async () => {
		await renderCritical({ source_addresses: ['192.0.2.10', '198.51.100.7'] }, {});
		expect(screen.queryByTestId('narrowed-warning')).not.toBeInTheDocument();
	});
});

// Options the config does not allow are shown struck through before
// approval at both levels (SSOOSSH.md), so the simplified page lists a
// trimmed extension and carries the narrowed warning too.
describe('a trimmed extension at the simplified level', () => {
	/** renderNarrowed opens a user request whose permit-port-forwarding
	 * the server trimmed, at the simplified level. */
	async function renderNarrowed() {
		vi.stubGlobal('localStorage', { getItem: () => null, setItem: () => undefined });
		detail.level = 'simplified';
		mockFetch(200, {
			id: 'req-1',
			type: 'user',
			status: 'pending',
			source_ip: '198.51.100.7',
			public_key: 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5',
			principals: [],
			valid_seconds: 28800,
			requested: {
				extensions: ['permit-pty', 'permit-port-forwarding'],
				no_touch_required: false
			},
			granted: { extensions: ['permit-pty'], no_touch_required: false },
			created_at: '2026-08-14T09:00:00Z',
			approval_url: '/approve/req-1',
			is_owned_by_you: true,
			already_closed: false
		});
		redirectIfUnauthenticated.mockReturnValue(false);
		render(Page);
		await screen.findByTestId('approval-view');
	}

	afterEach(() => {
		detail.level = 'advanced';
	});

	it('should list the trimmed extension struck through', async () => {
		await renderNarrowed();
		expect(screen.getByTestId('option-trimmed')).toHaveTextContent('permit-port-forwarding');
	});

	it('should still list the granted extension', async () => {
		await renderNarrowed();
		expect(screen.getByTestId('option-granted')).toHaveTextContent('permit-pty');
	});

	it('should label the trimmed extension as not permitted', async () => {
		await renderNarrowed();
		expect(screen.getByText('not permitted by this server')).toBeInTheDocument();
	});

	it('should warn that less than was requested', async () => {
		await renderNarrowed();
		expect(screen.getByTestId('narrowed-warning')).toBeInTheDocument();
	});
});

describe('remembered principals', () => {
	/** userRequest is a pending user-certificate request. */
	function userRequest() {
		return {
			id: 'req-1',
			type: 'user',
			status: 'pending',
			source_ip: '198.51.100.7',
			public_key: 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5',
			principals: [],
			valid_seconds: 28800,
			requested: { extensions: [], no_touch_required: false },
			granted: { extensions: [], no_touch_required: false },
			created_at: '2026-08-14T09:00:00Z',
			approval_url: '/approve/req-1',
			is_owned_by_you: true,
			already_closed: false
		};
	}

	/** stubStorage installs a localStorage holding `seed`, and returns it. */
	function stubStorage(seed: Record<string, string> = {}) {
		const store = new Map(Object.entries(seed));
		vi.stubGlobal('localStorage', {
			getItem: (key: string) => store.get(key) ?? null,
			setItem: (key: string, value: string) => void store.set(key, value)
		});
		return store;
	}

	/** pressed lists the picker's ticked chips, in render order. */
	function pressed(): string[] {
		const group = screen.getByRole('group', { name: 'Principals to include' });
		return within(group)
			.getAllByRole('button')
			.filter((chip) => chip.getAttribute('aria-pressed') === 'true')
			.map((chip) => chip.textContent?.trim() ?? '');
	}

	beforeEach(() => {
		redirectIfUnauthenticated.mockReturnValue(false);
		session.user = signedInAs({ other_accounts: ['alice-admin', 'alice-ops'] });
	});

	afterEach(() => {
		session.user = null;
	});

	it('should pre-tick what the approver last approved with', async () => {
		stubStorage({
			'ssoossh:principals:alice': JSON.stringify({ user: ['alice-admin', 'alice-ops'] })
		});
		mockFetch(200, userRequest());
		render(Page);
		await settle();

		expect(pressed()).toEqual(['alice-admin', 'alice-ops']);
	});

	it('should fall back to the username when nothing is remembered', async () => {
		stubStorage();
		mockFetch(200, userRequest());
		render(Page);
		await settle();

		expect(pressed()).toEqual(['alice']);
	});

	it('should remember the selection when the request is approved', async () => {
		const store = stubStorage();
		mockFetch(200, userRequest());
		render(Page);
		await settle();

		await userEvent.click(screen.getByRole('button', { name: 'alice-ops' }));
		await userEvent.click(screen.getByTestId('approve-button'));
		await settle();

		expect(JSON.parse(store.get('ssoossh:principals:alice') ?? '{}')).toEqual({
			user: ['alice', 'alice-ops']
		});
	});

	// A denial says nothing about which accounts the approver wants next.
	it('should remember nothing when the request is denied', async () => {
		const store = stubStorage();
		mockFetch(200, userRequest());
		render(Page);
		await settle();

		await userEvent.click(screen.getByTestId('deny-button'));
		await settle();

		expect(store.has('ssoossh:principals:alice')).toBe(false);
	});

	// Seeded once: unticking every chip must not re-tick the remembered set.
	it('should leave the picker empty once the approver unticks everything', async () => {
		stubStorage();
		mockFetch(200, userRequest());
		render(Page);
		await settle();

		await userEvent.click(screen.getByRole('button', { name: 'alice' }));

		expect(pressed()).toEqual([]);
	});
});

describe('when a decision does not go through', () => {
	function pending(overrides: Record<string, unknown> = {}) {
		return {
			id: 'req-1',
			type: 'user',
			status: 'pending',
			source_ip: '198.51.100.7',
			public_key: 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5',
			principals: [],
			valid_seconds: 28800,
			requested: { extensions: [], no_touch_required: false },
			granted: { extensions: [], no_touch_required: false },
			created_at: '2026-08-14T09:00:00Z',
			expires_at: new Date(Date.now() + 10 * 60 * 1000).toISOString(),
			approval_url: '/approve/req-1',
			is_owned_by_you: true,
			already_closed: false,
			...overrides
		};
	}

	/**
	 * stubDecision answers the first detail read with a pending request, the
	 * approve call with `status` and `message` (by default the 409 the
	 * server gives a request decided in the meantime), and every later
	 * detail read with `after`, so the page's re-check sees what changed.
	 */
	function stubDecision(
		after: object,
		status = 409,
		message = 'this certificate request was already approved'
	) {
		let reads = 0;
		vi.stubGlobal(
			'fetch',
			vi.fn((input: RequestInfo | URL) => {
				const url = String(input);
				const reply = (status: number, data: unknown) =>
					Promise.resolve(
						new Response(JSON.stringify({ data, error: status === 200 ? null : message }), {
							status,
							headers: { 'Content-Type': 'application/json' }
						})
					);
				if (url.includes('/approve')) return reply(status, null);
				reads += 1;
				return reply(200, reads === 1 ? pending() : after);
			})
		);
	}

	beforeEach(() => {
		redirectIfUnauthenticated.mockReturnValue(false);
		session.user = signedInAs();
	});

	afterEach(() => {
		session.user = null;
	});

	// The server answers approving a closed request with 409 or 410, so
	// the page reads the request back and says what really happened.
	it('should show the request closed in the meantime instead of the error', async () => {
		stubDecision(pending({ status: 'approved', already_closed: true }));
		render(Page);
		await settle();

		await userEvent.click(screen.getByTestId('approve-button'));
		await settle();
		await settle();

		expect(screen.getByTestId('blocked-already-resolved')).toHaveTextContent('nothing left to do');
	});

	it('should show the denial when the request was denied in the meantime', async () => {
		stubDecision(
			pending({ status: 'denied', already_closed: true }),
			409,
			'this certificate request was already denied'
		);
		render(Page);
		await settle();

		await userEvent.click(screen.getByTestId('approve-button'));
		await settle();
		await settle();

		expect(screen.getByTestId('blocked-already-resolved')).toHaveTextContent('denied');
	});

	it('should show the expiry when the request expired in the meantime', async () => {
		stubDecision(
			pending({ status: 'expired', already_closed: true }),
			410,
			'this certificate request has expired'
		);
		render(Page);
		await settle();

		await userEvent.click(screen.getByTestId('approve-button'));
		await settle();
		await settle();

		expect(screen.getByTestId('expired-notice')).toBeInTheDocument();
	});

	// A request past its deadline that nothing has marked expired yet still
	// reads back as pending; the server's own reason is then the answer.
	it('should show the server reason when the request reads back unchanged', async () => {
		stubDecision(pending(), 410, 'this certificate request has expired');
		render(Page);
		await settle();

		await userEvent.click(screen.getByTestId('approve-button'));
		await settle();
		await settle();

		expect(screen.getByTestId('action-error')).toHaveTextContent(
			'this certificate request has expired'
		);
	});

	it('should keep the error when the request has not changed', async () => {
		stubDecision(pending(), 500, 'internal server error');
		render(Page);
		await settle();

		await userEvent.click(screen.getByTestId('approve-button'));
		await settle();
		await settle();

		expect(screen.getByTestId('action-error')).toHaveTextContent('Could not record your decision');
	});
});

describe('the browser tab', () => {
	it('should name a console login for what it is', async () => {
		redirectIfUnauthenticated.mockReturnValue(false);
		mockFetch(200, {
			id: 'req-1',
			type: 'console',
			status: 'pending',
			source_ip: '198.51.100.7',
			public_key: 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5',
			principals: [],
			valid_seconds: 600,
			requested: { extensions: [], no_touch_required: false },
			granted: { extensions: [], no_touch_required: false },
			created_at: '2026-08-14T09:00:00Z',
			approval_url: '/approve/req-1',
			is_owned_by_you: true,
			already_closed: false
		});
		render(Page);
		await settle();
		expect(document.title).toBe('Approve a console login · ssoossh');
	});
});

// #191: the page reads the request once and never again after a decision
// goes through, so the status badge must follow the decision itself. Run
// through the page rather than the view, because the bug was the page
// leaving `detail.status` at pending under an Approved or Denied alert.
describe('after a decision goes through', () => {
	/** pendingOf is a pending request of `type` the visitor owns. */
	function pendingOf(type: string) {
		return {
			id: 'req-1',
			type,
			status: 'pending',
			source_ip: '198.51.100.7',
			public_key: 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5',
			principals: ['alice'],
			valid_seconds: 28800,
			requested: { extensions: [], no_touch_required: false },
			granted: { extensions: [], no_touch_required: false },
			created_at: '2026-08-14T09:00:00Z',
			expires_at: new Date(Date.now() + 10 * 60 * 1000).toISOString(),
			approval_url: '/approve/req-1',
			is_owned_by_you: true,
			already_closed: false
		};
	}

	/** badgeText is the request status badge's text. */
	function badgeText(): string {
		return screen.getByTestId('request-status').textContent!.trim();
	}

	beforeEach(() => {
		redirectIfUnauthenticated.mockReturnValue(false);
		vi.stubGlobal('localStorage', { getItem: () => null, setItem: () => undefined });
		session.user = signedInAs({ approvable_service_accounts: ['svc-deploy'] });
	});

	afterEach(() => {
		session.user = null;
	});

	it.each([
		{ type: 'user', button: 'approve-button', want: 'approved' },
		{ type: 'pam', button: 'approve-button', want: 'approved' },
		{ type: 'console', button: 'approve-button', want: 'approved' },
		{ type: 'service', button: 'approve-button', want: 'enrolled' },
		{ type: 'user', button: 'deny-button', want: 'denied' },
		{ type: 'service', button: 'deny-button', want: 'denied' }
	])(
		'should show $want on the badge after $button on a $type request',
		async ({ type, button, want }) => {
			mockFetch(200, pendingOf(type));
			render(Page);
			await settle();
			if (type === 'service' && button === 'approve-button') {
				await userEvent.selectOptions(screen.getByTestId('service-account-select'), 'svc-deploy');
			}

			await userEvent.click(screen.getByTestId(button));
			await settle();

			expect(badgeText()).toBe(want);
		}
	);
});

// #192: on a service request the Principals row showed the requester's own
// name, while the certificate goes to the service account picked on the
// page. Run through the page so the binding between the select and the row
// stays covered, before and after Approve.
describe('the Principals row on a service request', () => {
	/** serviceRequest is a pending service request the requester alice made. */
	function serviceRequest() {
		return {
			id: 'req-1',
			type: 'service',
			status: 'pending',
			source_ip: '198.51.100.7',
			public_key: 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5',
			principals: ['alice'],
			valid_seconds: 28800,
			requested: { extensions: [], no_touch_required: false },
			granted: { extensions: [], no_touch_required: false },
			created_at: '2026-08-14T09:00:00Z',
			expires_at: new Date(Date.now() + 10 * 60 * 1000).toISOString(),
			approval_url: '/approve/req-1',
			is_owned_by_you: true,
			already_closed: false
		};
	}

	/** principalsRow is the value cell beside the Principals label. */
	function principalsRow(): HTMLElement {
		return screen.getByText('Principals', { selector: 'dt' }).nextElementSibling as HTMLElement;
	}

	beforeEach(() => {
		redirectIfUnauthenticated.mockReturnValue(false);
		session.user = signedInAs({ approvable_service_accounts: ['sv-deploy'] });
		mockFetch(200, serviceRequest());
	});

	afterEach(() => {
		session.user = null;
	});

	it('should not name the requester before an account is chosen', async () => {
		render(Page);
		await settle();
		expect(principalsRow()).toHaveTextContent('the account you select below');
	});

	it('should name the chosen service account', async () => {
		render(Page);
		await settle();
		await userEvent.selectOptions(screen.getByTestId('service-account-select'), 'sv-deploy');
		expect(principalsRow()).toHaveTextContent(/^sv-deploy$/);
	});

	it('should keep naming the chosen account after approval', async () => {
		render(Page);
		await settle();
		await userEvent.selectOptions(screen.getByTestId('service-account-select'), 'sv-deploy');
		await userEvent.click(screen.getByTestId('approve-button'));
		await settle();
		expect(principalsRow()).toHaveTextContent(/^sv-deploy$/);
	});
});
