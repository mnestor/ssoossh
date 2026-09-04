import { render, screen, within } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { session } from '$lib/session.svelte';
import { resetFakePage } from '$lib/testing/page.svelte';
import Layout from './+layout.svelte';

// Test methodology: render the admin gate around a marker child with the
// app-wide session singleton put into each state directly. The gate is
// display-only (the server re-checks every read), but it decides whether a
// signed-in non-auditor gets an explanation or a redirect loop, which is
// worth pinning. The section tabs are pinned alongside it: which one reads
// as current is the only thing about them a page can get wrong, and a
// detail page is the case that used to leave the row with nothing marked.

vi.mock('$app/state', async () => {
	const { fakePage } = await import('$lib/testing/page.svelte');
	return { page: fakePage };
});

const children = createRawSnippet(() => ({
	render: () => '<p data-testid="admin-child">admin content</p>'
}));

/** signedInUser is a minimal session user with the given auditor flag. */
function signedInUser(isAuditor: boolean) {
	return {
		subject: 'sub-1',
		username: 'alice',
		email: 'alice@example.com',
		groups: [],
		other_accounts: [],
		service_accounts: [],
		extra: {},
		is_auditor: isAuditor
	};
}

beforeEach(() => {
	resetFakePage('http://localhost/admin/users');
	session.clear();
});

describe('admin layout', () => {
	it('should render the admin area for an auditor', () => {
		session.user = signedInUser(true);
		session.resolved = true;

		render(Layout, { children });

		expect(screen.getByTestId('admin-child')).toBeInTheDocument();
		expect(screen.getByText('Audit log')).toBeInTheDocument();
		expect(screen.queryByTestId('admin-access-denied')).not.toBeInTheDocument();
	});

	// Logging in again cannot grant admin, so a signed-in non-auditor gets
	// an explanation rather than a redirect loop.
	it('should explain access denial to a signed-in non-auditor', () => {
		session.user = signedInUser(false);
		session.resolved = true;

		render(Layout, { children });

		expect(screen.getByTestId('admin-access-denied')).toBeInTheDocument();
		expect(screen.queryByTestId('admin-child')).not.toBeInTheDocument();
	});

	it('should name every admin section in the tab row', () => {
		session.user = signedInUser(true);
		session.resolved = true;

		render(Layout, { children });

		const tabs = screen.getByRole('navigation', { name: 'Admin sections' });
		const labels = within(tabs)
			.getAllByRole('link')
			.map((link) => link.textContent?.trim());

		expect(labels).toEqual(['Users', 'Certificates', 'Service codes', 'Config', 'Audit log']);
	});

	it('should mark the section being viewed as the current page', () => {
		session.user = signedInUser(true);
		session.resolved = true;

		render(Layout, { children });

		expect(screen.getByRole('link', { name: 'Users' })).toHaveAttribute('aria-current', 'page');
		expect(screen.getByRole('link', { name: 'Config' })).not.toHaveAttribute('aria-current');
	});

	// A detail page sits under its section rather than at it, and used to
	// leave the whole row unmarked.
	it('should keep a section current on a detail page beneath it', () => {
		resetFakePage('http://localhost/admin/users/user-123');
		session.user = signedInUser(true);
		session.resolved = true;

		render(Layout, { children });

		expect(screen.getByRole('link', { name: 'Users' })).toHaveAttribute('aria-current', 'page');
	});

	// /admin/service-codes must not light up for /admin/service-codes-other,
	// which a bare prefix match would.
	it('should not mark a section whose route is only a prefix of the path', () => {
		resetFakePage('http://localhost/admin/certificates-archive');
		session.user = signedInUser(true);
		session.resolved = true;

		render(Layout, { children });

		expect(screen.getByRole('link', { name: 'Certificates' })).not.toHaveAttribute('aria-current');
	});

	it('should hold on a loading state while the session resolves', () => {
		session.user = null;
		session.resolved = false;

		render(Layout, { children });

		expect(screen.getByText('Loading...')).toBeInTheDocument();
		expect(screen.queryByTestId('admin-child')).not.toBeInTheDocument();
		expect(screen.queryByTestId('admin-access-denied')).not.toBeInTheDocument();
	});
});
