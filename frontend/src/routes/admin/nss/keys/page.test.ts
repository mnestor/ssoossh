import { render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { session } from '#lib/session.svelte.js';
import { envelope, routeFetch, userFixture } from '#lib/testing/nss.js';

// Test methodology: the route around NSSKeyAdminList (whose own suite
// covers the list): its title, and New key for the fleet key admin role
// alone, whatever other role it holds, pointing the create form back here.
// Who reaches the page at all is the admin layout's (layout.test.ts).

vi.mock('$app/state', async () => {
	const { fakePage } = await import('#lib/testing/page.svelte.js');
	return { page: fakePage };
});
vi.mock('$app/navigation', () => ({ goto: vi.fn(), afterNavigate: vi.fn() }));

import { resetFakePage } from '#lib/testing/page.svelte.js';
import Page from './+page.svelte';

beforeEach(() => {
	resetFakePage('http://localhost/admin/nss/keys');
	session.resolved = true;
	routeFetch({
		'GET /api/admin/nss/keys': envelope({ keys: [], total: 0, usage_retention_seconds: 7776000 })
	});
});

afterEach(() => {
	vi.unstubAllGlobals();
	session.clear();
});

describe('all NSS keys route', () => {
	it('should be titled All NSS keys when opened', () => {
		session.user = userFixture({ is_auditor: true });
		render(Page);

		expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('All NSS keys');
	});

	it('should offer New key back to this list when the viewer holds the fleet key admin role', () => {
		session.user = userFixture({ is_auditor: true, is_nss_key_admin: true });
		render(Page);

		expect(screen.getByTestId('nss-admin-keys-new')).toHaveAttribute(
			'href',
			'/nss-keys/new?from=admin'
		);
	});

	it('should offer New key when the viewer is a fleet key admin with no admin area role', () => {
		session.user = userFixture({ is_nss_key_admin: true });
		render(Page);

		expect(screen.getByTestId('nss-admin-keys-new')).toBeInTheDocument();
	});

	it('should not offer New key when the viewer does not hold the fleet key admin role', () => {
		session.user = userFixture({ is_auditor: true });
		render(Page);

		expect(screen.queryByTestId('nss-admin-keys-new')).not.toBeInTheDocument();
	});
});
