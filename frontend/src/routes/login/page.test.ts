import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi, beforeEach } from 'vitest';

// The page reads the current URL for ?return_to= and hands it to startLogin.
// Neither a router nor a real IdP redirect belongs in a component test, so
// both are stubbed and the URL is rewritten per case.
// vi.hoisted: vi.mock is lifted above the imports, so the state the factories
// close over has to be created up there with them.
const { pageState, startLogin, branding } = vi.hoisted(() => ({
	pageState: { url: new URL('http://localhost/login') },
	startLogin: vi.fn(),
	branding: {
		login_notice: undefined as string | undefined,
		support_email: undefined as string | undefined,
		support_label: undefined as string | undefined
	}
}));

vi.mock('$app/state', () => ({ page: pageState }));
vi.mock('$lib/auth', () => ({ startLogin }));
vi.mock('$lib/branding.svelte', () => ({ getBranding: () => branding }));

import Page from './+page.svelte';

beforeEach(() => {
	pageState.url = new URL('http://localhost/login');
	branding.login_notice = undefined;
	branding.support_email = undefined;
	branding.support_label = undefined;
	startLogin.mockClear();
});

describe('Login page', () => {
	it('should show the sign-in heading when signed out', () => {
		render(Page);
		expect(screen.getByRole('heading', { name: 'Sign in to ssoossh' })).toBeInTheDocument();
	});

	it('should label the primary action Continue with SSO', () => {
		render(Page);
		expect(screen.getByRole('button', { name: /Continue with SSO/ })).toBeInTheDocument();
	});

	it('should show where to turn when sign-in fails', () => {
		render(Page);
		expect(screen.getByText(/Contact your administrator/)).toBeInTheDocument();
	});

	it('should send the caller to the dashboard when no return_to is given', async () => {
		render(Page);
		await userEvent.click(screen.getByRole('button', { name: /Continue with SSO/ }));
		expect(startLogin).toHaveBeenCalledWith('/dashboard');
	});

	it('should carry an internal return_to through to startLogin', async () => {
		pageState.url = new URL('http://localhost/login?return_to=/approve/abc');
		render(Page);
		await userEvent.click(screen.getByRole('button', { name: /Continue with SSO/ }));
		expect(startLogin).toHaveBeenCalledWith('/approve/abc');
	});

	it('should refuse an off-site return_to and fall back to the dashboard', async () => {
		pageState.url = new URL('http://localhost/login?return_to=https://evil.example/steal');
		render(Page);
		await userEvent.click(screen.getByRole('button', { name: /Continue with SSO/ }));
		expect(startLogin).toHaveBeenCalledWith('/dashboard');
	});

	describe('when the deployment names a support contact', () => {
		beforeEach(() => {
			branding.support_email = 'support@example.com';
		});

		it('should link the contact by mail', () => {
			branding.support_label = 'the IT service desk';
			render(Page);
			expect(screen.getByRole('link', { name: 'the IT service desk' })).toHaveAttribute(
				'href',
				'mailto:support@example.com'
			);
		});

		it('should fall back to the address itself when no label is configured', () => {
			render(Page);
			expect(screen.getByRole('link', { name: 'support@example.com' })).toHaveAttribute(
				'href',
				'mailto:support@example.com'
			);
		});

		it('should drop the generic administrator wording', () => {
			render(Page);
			expect(screen.queryByText(/Contact your administrator/)).toBeNull();
		});
	});

	it('should keep the generic wording when only a label is configured', () => {
		branding.support_label = 'the IT service desk';
		render(Page);
		expect(screen.getByText(/Contact your administrator/)).toBeInTheDocument();
	});

	describe('when the deployment sets a consent notice', () => {
		beforeEach(() => {
			branding.login_notice = 'Authorized use only.';
		});

		it('should show the notice before sign-in is possible', () => {
			render(Page);
			expect(screen.getByText('Authorized use only.')).toBeInTheDocument();
		});

		it('should disable the sign-in button until the notice is accepted', () => {
			render(Page);
			expect(screen.getByRole('button', { name: /Continue with SSO/ })).toBeDisabled();
		});

		it('should enable the sign-in button once the notice is accepted', async () => {
			render(Page);
			await userEvent.click(screen.getByRole('button', { name: 'I Accept' }));
			expect(screen.getByRole('button', { name: /Continue with SSO/ })).toBeEnabled();
		});
	});
});
