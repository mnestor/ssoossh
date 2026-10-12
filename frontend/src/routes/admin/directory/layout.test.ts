import { render, screen } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { session } from '#lib/session.svelte.js';
import { resetFakePage } from '#lib/testing/page.svelte.js';
import Layout from './+layout.svelte';

// Test methodology: render the Directory's admin gate around a marker child
// with the session singleton set to each role. The parent admin layout has
// already turned away anyone below auditor, so every case here is someone
// inside the admin area. The gate is display-only (the server refuses the
// status read on its own); what is pinned is that SOC and auditors get the
// not-authorized card rather than a page that starts a fetch it cannot win.

vi.mock('$app/state', async () => {
	const { fakePage } = await import('#lib/testing/page.svelte.js');
	return { page: fakePage };
});

const children = createRawSnippet(() => ({
	render: () => '<p data-testid="directory-child">directory content</p>'
}));

/** Role flags as the server derives them: admin implies SOC implies auditor. */
interface Roles {
	is_admin: boolean;
	is_soc: boolean;
	is_auditor: boolean;
}

/** signIn puts a resolved session with the given roles in the store. */
function signIn(roles: Roles) {
	session.user = {
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
		...roles,
		detail_level: 'advanced' as const
	};
	session.resolved = true;
}

const admin: Roles = { is_admin: true, is_soc: true, is_auditor: true };
const soc: Roles = { is_admin: false, is_soc: true, is_auditor: true };
const auditor: Roles = { is_admin: false, is_soc: false, is_auditor: true };

beforeEach(() => {
	resetFakePage('http://localhost/admin/directory');
	session.clear();
});

describe('directory layout', () => {
	it('should render the Directory when the viewer is an admin', () => {
		signIn(admin);

		render(Layout, { children });

		expect(screen.getByTestId('directory-child')).toBeInTheDocument();
	});

	it('should not show the not-authorized card when the viewer is an admin', () => {
		signIn(admin);

		render(Layout, { children });

		expect(screen.queryByTestId('directory-access-denied')).not.toBeInTheDocument();
	});

	const refused: { name: string; roles: Roles }[] = [
		{ name: 'a SOC member', roles: soc },
		{ name: 'an auditor', roles: auditor }
	];

	for (const { name, roles } of refused) {
		it(`should show the not-authorized card when the viewer is ${name}`, () => {
			signIn(roles);

			render(Layout, { children });

			expect(screen.getByTestId('directory-access-denied')).toBeInTheDocument();
		});

		it(`should not render the Directory when the viewer is ${name}`, () => {
			signIn(roles);

			render(Layout, { children });

			expect(screen.queryByTestId('directory-child')).not.toBeInTheDocument();
		});

		it(`should name the admin role as the requirement when the viewer is ${name}`, () => {
			signIn(roles);

			render(Layout, { children });

			expect(screen.getByTestId('directory-access-denied')).toHaveTextContent(
				'The Directory needs the admin role.'
			);
		});

		it(`should lead back to the dashboard when the viewer is ${name}`, () => {
			signIn(roles);

			render(Layout, { children });

			expect(screen.getByTestId('directory-access-denied-dashboard')).toHaveAttribute(
				'href',
				'/dashboard'
			);
		});
	}

	// The card is the app's standard not-authorized state, the one the admin
	// layout and the error page's 403 use, not an error alert.
	it('should title the refusal as the standard not-authorized state when the viewer is not an admin', () => {
		signIn(soc);

		render(Layout, { children });

		expect(
			screen.getByRole('heading', { name: 'You do not have access to this page' })
		).toBeInTheDocument();
	});
});
