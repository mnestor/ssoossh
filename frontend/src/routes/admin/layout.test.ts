import { render, screen } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { session } from '$lib/session.svelte';
import { resetFakePage } from '$lib/testing/page.svelte';
import Layout from './+layout.svelte';

// Test methodology: render the admin gate around a marker child with the
// app-wide session singleton put into each state directly. The gate is
// display-only (the server re-checks every read), but it decides whether a
// signed-in non-auditor gets an explanation or a redirect loop, which is
// worth pinning.
//
// The section tabs this layout used to own moved to the rail, and are
// covered by AppRail.test.ts. What is pinned here instead is that they did
// not come back: a layout that navigates as well as gates would put the
// admin sections in two places that can disagree.

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

	// The rail names the sections now. A second copy here would be a second
	// list to keep in step.
	it('should not carry a navigation of its own', () => {
		session.user = signedInUser(true);
		session.resolved = true;

		render(Layout, { children });

		expect(screen.queryByRole('navigation')).not.toBeInTheDocument();
	});

	// Each page states its own width through PageShell. A width here would
	// be a second opinion wrapping the first.
	it('should not impose a width on the page inside it', () => {
		session.user = signedInUser(true);
		session.resolved = true;

		const { container } = render(Layout, { children });

		expect(container.querySelector('[class*="max-w-"]')).toBeNull();
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
