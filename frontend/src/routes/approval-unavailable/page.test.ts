import { render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi, beforeEach } from 'vitest';

// The page's only input is the ?reason= query parameter, so the router
// state is stubbed and the URL rewritten per case.
// vi.hoisted: vi.mock is lifted above the imports, so the state the factory
// closes over has to be created up there with it.
const { pageState } = vi.hoisted(() => ({
	pageState: { url: new URL('http://localhost/approval-unavailable') }
}));

vi.mock('$app/state', () => ({ page: pageState }));

import Page from './+page.svelte';

beforeEach(() => {
	pageState.url = new URL('http://localhost/approval-unavailable');
});

describe('Approval unavailable page', () => {
	it('should explain the link was already opened by default', () => {
		render(Page);
		expect(screen.getByTestId('claim-already-opened')).toBeInTheDocument();
	});

	it('should say links are single-use rather than accuse anyone', () => {
		render(Page);
		expect(screen.getByText(/single-use/)).toBeInTheDocument();
	});

	it('should point at link-scanning software as the common cause', () => {
		render(Page);
		expect(screen.getByText(/security software scanning links/)).toBeInTheDocument();
	});

	it('should say how to recover with a fresh link', () => {
		render(Page);
		expect(screen.getByText(/Run the client again/)).toBeInTheDocument();
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
