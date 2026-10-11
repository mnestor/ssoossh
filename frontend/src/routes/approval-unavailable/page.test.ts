import { render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi, beforeEach } from 'vitest';

// The page's only input is the ?reason= query parameter, so the router
// state is stubbed and the URL rewritten per case.
// vi.hoisted: vi.mock is lifted above the imports, so the state the factory
// closes over has to be created up there with it.
const { pageState, branding } = vi.hoisted(() => ({
	pageState: { url: new URL('http://localhost/approval-unavailable') },
	branding: {
		support_email: undefined as string | undefined,
		support_label: undefined as string | undefined
	}
}));

vi.mock('$app/state', () => ({ page: pageState }));
vi.mock('#lib/branding.svelte.js', () => ({ getBranding: () => branding }));

import Page from './+page.svelte';

beforeEach(() => {
	pageState.url = new URL('http://localhost/approval-unavailable');
	branding.support_email = undefined;
	branding.support_label = undefined;
});

describe('Approval unavailable page', () => {
	it('should explain the link was already opened by default', () => {
		render(Page);
		expect(screen.getByTestId('claim-already-opened')).toBeInTheDocument();
	});

	it('should say links work once rather than accuse anyone', () => {
		render(Page);
		expect(screen.getByText(/Approval links work once/)).toBeInTheDocument();
	});

	it('should say nothing was approved', () => {
		render(Page);
		expect(screen.getByText(/Nothing was approved/)).toBeInTheDocument();
	});

	it('should point at link-scanning software as the common cause', () => {
		render(Page);
		expect(screen.getByText(/security software scanning links/)).toBeInTheDocument();
	});

	it('should tell the requester to run the client again from their terminal', () => {
		render(Page);
		expect(screen.getByTestId('claim-next-steps')).toHaveTextContent(
			'Run the client again and open the new link straight from your terminal'
		);
	});

	// The case the server treats as most likely: someone sent this link to
	// the reader. Telling them to get a fresh link and open it directly would
	// coach them round the scanner that just stopped a phishing approval.
	it('should tell someone sent the link not to ask for another', () => {
		render(Page);
		expect(screen.getByTestId('claim-sent-by-someone')).toHaveTextContent(
			'do not ask them for another one'
		);
	});

	it('should no longer tell every reader to open a fresh link directly', () => {
		render(Page);
		expect(screen.queryByText(/open it directly in your browser/)).not.toBeInTheDocument();
	});

	it('should name the configured support contact', () => {
		branding.support_email = 'help@corp.example';
		branding.support_label = 'the IT service desk';
		render(Page);
		expect(screen.getByRole('link', { name: 'the IT service desk' })).toHaveAttribute(
			'href',
			'mailto:help@corp.example'
		);
	});

	it('should fall back to the administrator without a support contact', () => {
		render(Page);
		expect(screen.getByTestId('claim-sent-by-someone')).toHaveTextContent(
			'Tell your administrator.'
		);
	});

	it('should say the dashboard needs cookies too when they are blocked', () => {
		pageState.url = new URL('http://localhost/approval-unavailable?reason=cookies');
		render(Page);
		expect(screen.getByTestId('claim-dashboard')).toHaveTextContent('once cookies are allowed');
	});

	// Nothing on this page can be retried, so it leads back into the app
	// rather than leave the reader on a dead end.
	it('should lead back to the dashboard', () => {
		render(Page);
		expect(screen.getByTestId('claim-dashboard')).toBeInTheDocument();
	});

	it('should show the cookie explanation when reason=cookies', () => {
		pageState.url = new URL('http://localhost/approval-unavailable?reason=cookies');
		render(Page);
		expect(screen.getByTestId('claim-cookies-blocked')).toBeInTheDocument();
	});

	it('should tell a cookie-blocked browser to allow cookies', () => {
		pageState.url = new URL('http://localhost/approval-unavailable?reason=cookies');
		render(Page);
		expect(screen.getByText(/Allow cookies for this site/)).toBeInTheDocument();
	});

	it('should fall back to the already-opened copy for an unknown reason', () => {
		pageState.url = new URL('http://localhost/approval-unavailable?reason=mystery');
		render(Page);
		expect(screen.getByTestId('claim-already-opened')).toBeInTheDocument();
	});
});
