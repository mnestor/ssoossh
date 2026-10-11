import { render, screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

// The page reads only the router's status and error, so both are stubbed
// and set per case.
// vi.hoisted: vi.mock is lifted above the imports, so the state the factory
// closes over has to be created up there with it.
const { pageState } = vi.hoisted(() => ({
	pageState: { status: 404, error: null as { message: string } | null }
}));

vi.mock('$app/state', () => ({ page: pageState }));

import ErrorPage from './+error.svelte';

beforeEach(() => {
	pageState.status = 404;
	pageState.error = null;
});

describe('error page', () => {
	it('should call a 404 a missing page', () => {
		render(ErrorPage);
		expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent("That page isn't here");
	});

	// A server failure used to read "That page isn't here", telling the
	// reader their link was wrong.
	it('should not call a server failure a missing page', () => {
		pageState.status = 500;
		render(ErrorPage);
		expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('This page could not load');
	});

	it('should name a refusal as a matter of access', () => {
		pageState.status = 403;
		render(ErrorPage);
		expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(
			'You do not have access to this page'
		);
	});

	// SvelteKit's own defaults name the status, not the cause; the page's
	// explanation used to be hidden behind them.
	it('should explain a server failure instead of repeating the router default', () => {
		pageState.status = 500;
		pageState.error = { message: 'Internal Error' };
		render(ErrorPage);
		expect(screen.getByText(/The server did not answer as expected/)).toBeInTheDocument();
	});

	it('should explain a missing page instead of saying Not Found twice', () => {
		pageState.status = 404;
		pageState.error = { message: 'Not Found' };
		render(ErrorPage);
		expect(screen.getByText(/The link may be incomplete/)).toBeInTheDocument();
	});

	it('should keep a message the app wrote itself', () => {
		pageState.status = 500;
		pageState.error = { message: 'The signer is unreachable.' };
		render(ErrorPage);
		expect(screen.getByText(/The signer is unreachable\./)).toBeInTheDocument();
	});

	it('should offer Reload for a server failure', () => {
		pageState.status = 500;
		render(ErrorPage);
		expect(screen.getByTestId('error-reload')).toBeInTheDocument();
	});

	it('should not offer Reload for a missing page', () => {
		pageState.status = 404;
		render(ErrorPage);
		expect(screen.queryByTestId('error-reload')).not.toBeInTheDocument();
	});

	it('should always lead back to the dashboard', () => {
		pageState.status = 500;
		render(ErrorPage);
		expect(screen.getByTestId('error-dashboard')).toBeInTheDocument();
	});
});
