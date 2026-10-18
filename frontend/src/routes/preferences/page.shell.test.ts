import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { session } from '#lib/session.svelte.js';
import { envelope, routeFetch, settle, userFixture, type Route } from '#lib/testing/nss.js';
import Page from './+page.svelte';

// Test methodology: the Login shell section alone, with the notification
// settings answered so the rest of the page renders. The section is live
// like the rest of the page: choosing saves at once, "Directory default"
// sends null, and a refused save puts the select back with the server's
// reason beside it.

const notifications = envelope({
	mail_enabled: true,
	address: 'alice@corp.example',
	groups: [],
	kinds: [],
	detail_level: 'simplified'
});

/** serve answers the page's reads, plus the shell save. */
function serve(save?: Route | Response, shells: string[] = ['bash', 'zsh', 'fish']) {
	return routeFetch({
		'GET /api/users/me/notifications': notifications,
		'GET /api/nss/shells': envelope({ allowed_shells: shells }),
		...(save ? { 'PUT /api/users/me/shell': save } : {})
	});
}

/** select is the shell select once the allowed list has loaded. */
function select(): Promise<HTMLSelectElement> {
	return screen.findByTestId('login-shell-select') as Promise<HTMLSelectElement>;
}

beforeEach(() => {
	session.user = userFixture({ posix: { uid: 10001, name: 'alice', shell_pref: null } });
	session.resolved = true;
});

afterEach(() => {
	vi.unstubAllGlobals();
	session.clear();
});

describe('Login shell preference', () => {
	it('should hide the section when the caller has no directory account', async () => {
		session.user = userFixture({ posix: null });
		serve();
		render(Page);
		await settle();

		expect(screen.queryByTestId('login-shell')).not.toBeInTheDocument();
	});

	it('should offer the directory default first and then the allowed shells', async () => {
		serve();
		render(Page);

		const options = [...(await select()).options].map((option) => option.textContent?.trim());
		expect(options).toEqual(['Directory default', 'bash', 'zsh', 'fish']);
	});

	it('should show the stored shell as chosen', async () => {
		session.user = userFixture({ posix: { uid: 10001, name: 'alice', shell_pref: 'zsh' } });
		serve();
		render(Page);

		expect((await select()).value).toBe('zsh');
	});

	it('should keep a stored shell the server no longer offers visible as chosen', async () => {
		session.user = userFixture({ posix: { uid: 10001, name: 'alice', shell_pref: 'tcsh' } });
		serve();
		render(Page);

		expect((await select()).selectedOptions[0].textContent).toContain('tcsh (no longer offered)');
	});

	it('should save the chosen shell by name', async () => {
		const calls = serve(envelope({ uid: 10001, name: 'alice', shell_pref: 'zsh' }));
		render(Page);
		await userEvent.selectOptions(await select(), 'zsh');
		await settle();

		expect(calls.find((call) => call.method === 'PUT')?.body).toEqual({ shell_pref: 'zsh' });
	});

	it('should send null when Directory default is chosen', async () => {
		session.user = userFixture({ posix: { uid: 10001, name: 'alice', shell_pref: 'zsh' } });
		const calls = serve(envelope({ uid: 10001, name: 'alice', shell_pref: null }));
		render(Page);
		await userEvent.selectOptions(await select(), '');
		await settle();

		expect(calls.find((call) => call.method === 'PUT')?.body).toEqual({ shell_pref: null });
	});

	it('should announce the save when it lands', async () => {
		serve(envelope({ uid: 10001, name: 'alice', shell_pref: 'zsh' }));
		render(Page);
		await userEvent.selectOptions(await select(), 'zsh');
		await settle();

		expect(screen.getByTestId('preferences-status')).toHaveTextContent('Login shell zsh. Saved.');
	});

	it('should show the server reason when the shell is refused', async () => {
		serve(
			envelope(
				null,
				400,
				'shell_not_allowed: "zsh" is not one of the shells this server allows (bash, fish)'
			)
		);
		render(Page);
		await userEvent.selectOptions(await select(), 'zsh');

		expect(await screen.findByTestId('login-shell-save-error')).toHaveTextContent(
			'shell_not_allowed: "zsh" is not one of the shells this server allows'
		);
	});

	it('should put the select back when the save fails', async () => {
		serve(envelope(null, 500, 'database unavailable'));
		render(Page);
		await userEvent.selectOptions(await select(), 'zsh');
		await screen.findByTestId('login-shell-save-error');

		expect((await select()).value).toBe('');
	});

	it('should offer Try again when the allowed shells fail to load', async () => {
		routeFetch({
			'GET /api/users/me/notifications': notifications,
			'GET /api/nss/shells': envelope(null, 500, 'boom')
		});
		render(Page);

		expect(await screen.findByTestId('login-shell-error-retry')).toBeInTheDocument();
	});

	it('should sit between the email notifications and the display sections', async () => {
		serve();
		render(Page);
		await select();

		const headings = screen.getAllByRole('heading', { level: 2 }).map((h) => h.textContent?.trim());
		expect(headings).toEqual(['Email notifications', 'Login shell', 'Display']);
	});
});
