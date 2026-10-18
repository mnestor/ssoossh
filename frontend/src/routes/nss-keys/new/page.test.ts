import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { session } from '#lib/session.svelte.js';
import { envelope, keyFixture, routeFetch, userFixture } from '#lib/testing/nss.js';

// Test methodology: fill the form and route the create call to a fixture
// answer, then drive the one-time secret panel. What is pinned: who may
// reach the form, what the body carries, that the secret cannot be waved
// away unacknowledged, and that nothing on the page still holds it once it
// is dismissed. Navigation is stubbed; beforeNavigate's callback is kept so
// the leave guard can be exercised directly.

const { goto, guards } = vi.hoisted(() => ({
	goto: vi.fn(() => Promise.resolve()),
	guards: [] as Array<(navigation: { cancel: () => void; type: string }) => void>
}));

vi.mock('$app/navigation', () => ({
	goto,
	beforeNavigate: (callback: (navigation: { cancel: () => void; type: string }) => void) => {
		guards.push(callback);
	}
}));
vi.mock('$app/state', async () => {
	const { fakePage } = await import('#lib/testing/page.svelte.js');
	return { page: fakePage };
});

import Page from './+page.svelte';

const SECRET = 'c2VjcmV0LW9uY2U';

beforeEach(() => {
	session.user = userFixture({ is_nss_key_admin: true });
	session.resolved = true;
	goto.mockClear();
	guards.length = 0;
});

afterEach(() => {
	vi.unstubAllGlobals();
	session.clear();
});

/** answers routes the suggestions read and the create call. */
function answers(create: Response = envelope({ key: keyFixture(), secret: SECRET })) {
	return routeFetch({
		'GET /api/nss/keys': envelope({ keys: [], can_create: true }),
		'POST /api/nss/keys': create
	});
}

/** createNamed types a name and presses Create key. */
async function createNamed(name = 'web fleet') {
	await userEvent.type(screen.getByTestId('nss-key-name'), name);
	await userEvent.click(screen.getByTestId('nss-key-create'));
}

describe('New NSS key', () => {
	it('should turn away a caller without the fleet key admin role', () => {
		session.user = userFixture();
		answers();
		render(Page);

		expect(screen.getByTestId('nss-key-new-denied')).toBeInTheDocument();
	});

	it('should ask for a name when Create key is pressed without one', async () => {
		const calls = answers();
		render(Page);
		await userEvent.click(screen.getByTestId('nss-key-create'));

		expect(screen.getByTestId('nss-key-name-error')).toBeInTheDocument();
		expect(calls.filter((call) => call.method === 'POST')).toHaveLength(0);
	});

	it('should default the notification email to the caller', () => {
		answers();
		render(Page);

		expect(screen.getByTestId('nss-key-email')).toHaveValue('alice@corp.example');
	});

	it('should send the name, the group and the address when the form is submitted', async () => {
		const calls = answers();
		render(Page);
		await userEvent.type(screen.getByTestId('nss-key-group'), 'web-admins');
		await createNamed();

		expect(calls.find((call) => call.method === 'POST')?.body).toEqual({
			name: 'web fleet',
			group_name: 'web-admins',
			notification_email: 'alice@corp.example'
		});
	});

	it('should send the end of the chosen day when an expiry is picked', async () => {
		const calls = answers();
		render(Page);
		await userEvent.type(screen.getByTestId('nss-key-expires'), '2027-03-01');
		await createNamed();

		const body = calls.find((call) => call.method === 'POST')?.body as { expires_at: string };
		expect(new Date(body.expires_at).getTime()).toBe(new Date('2027-03-01T23:59:59').getTime());
	});

	it('should show the server reason when the create is refused', async () => {
		answers(envelope(null, 400, 'expires_at is in the past'));
		render(Page);
		await createNamed();

		expect(await screen.findByTestId('nss-key-create-error')).toHaveTextContent(
			'expires_at is in the past'
		);
	});

	describe('one-time secret', () => {
		it('should show the secret once the key is created', async () => {
			answers();
			render(Page);
			await createNamed();

			expect(await screen.findByTestId('secret-panel-secret')).toHaveTextContent(SECRET);
		});

		it('should hold Done until the secret is acknowledged', async () => {
			answers();
			render(Page);
			await createNamed();

			expect(await screen.findByTestId('secret-panel-done')).toBeDisabled();
		});

		it('should open the new key when Done is pressed after acknowledging', async () => {
			answers();
			render(Page);
			await createNamed();
			await userEvent.click(await screen.findByLabelText('I have stored this secret'));
			await userEvent.click(screen.getByTestId('secret-panel-done'));

			expect(goto).toHaveBeenCalledWith('/nss-keys/nss_k7m4qp2x');
		});

		it('should hold the secret nowhere on the page once Done is pressed', async () => {
			answers();
			const { container } = render(Page);
			await createNamed();
			await userEvent.click(await screen.findByLabelText('I have stored this secret'));
			await userEvent.click(screen.getByTestId('secret-panel-done'));

			expect(container.innerHTML).not.toContain(SECRET);
		});

		it('should ask before a navigation away while the secret is on screen', async () => {
			answers();
			const confirm = vi.fn(() => false);
			vi.stubGlobal('confirm', confirm);
			render(Page);
			await createNamed();
			await screen.findByTestId('secret-panel');

			const cancel = vi.fn();
			guards.forEach((guard) => guard({ cancel, type: 'link' }));

			expect(cancel).toHaveBeenCalled();
		});

		it('should let a navigation through once the secret is dismissed', async () => {
			answers();
			render(Page);
			await createNamed();
			await userEvent.click(await screen.findByLabelText('I have stored this secret'));
			await userEvent.click(screen.getByTestId('secret-panel-done'));

			const cancel = vi.fn();
			guards.forEach((guard) => guard({ cancel, type: 'goto' }));

			expect(cancel).not.toHaveBeenCalled();
		});
	});
});
