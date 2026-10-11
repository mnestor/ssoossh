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

	it('should prefer the error message the router carries', () => {
		pageState.status = 500;
		pageState.error = { message: 'Internal Error' };
		render(ErrorPage);
		expect(screen.getByText(/Internal Error/)).toBeInTheDocument();
	});

	it('should always lead back to the dashboard', () => {
		pageState.status = 500;
		render(ErrorPage);
		expect(screen.getByTestId('error-dashboard')).toBeInTheDocument();
	});
});
