import { render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { session } from '#lib/session.svelte.js';
import { envelope, routeFetch, userFixture } from '#lib/testing/nss.js';

// Test methodology: the two list routes around NSSEntryList (whose own
// suite covers the list): each names itself, and the Accounts | Groups
// switch in the heading marks the page it is on and links to the other.

vi.mock('$app/state', async () => {
	const { fakePage } = await import('#lib/testing/page.svelte.js');
	return { page: fakePage };
});
vi.mock('$app/navigation', () => ({ goto: vi.fn(), afterNavigate: vi.fn() }));

import { resetFakePage } from '#lib/testing/page.svelte.js';
import AccountsPage from './accounts/+page.svelte';
import GroupsPage from './groups/+page.svelte';

const routes = [
	{ name: 'accounts', Page: AccountsPage, title: 'Stored accounts', other: 'groups' },
	{ name: 'groups', Page: GroupsPage, title: 'Stored groups', other: 'accounts' }
] as const;

beforeEach(() => {
	session.user = userFixture({ is_auditor: true });
	session.resolved = true;
	routeFetch({
		'GET /api/admin/nss/users': envelope({ accounts: [], total: 0 }),
		'GET /api/admin/nss/groups': envelope({ groups: [], total: 0 })
	});
});

afterEach(() => {
	vi.unstubAllGlobals();
	session.clear();
});

for (const { name, Page, title, other } of routes) {
	describe(`stored ${name} route`, () => {
		beforeEach(() => resetFakePage(`http://localhost/admin/nss/${name}`));

		it(`should be titled ${title} when opened`, () => {
			render(Page);

			expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(title);
		});

		it('should mark its own list current in the switch', () => {
			render(Page);

			expect(screen.getByTestId(`nss-view-${name}`)).toHaveAttribute('aria-current', 'page');
		});

		it('should link to the other list from the switch', () => {
			render(Page);

			expect(screen.getByTestId(`nss-view-${other}`)).toHaveAttribute(
				'href',
				`/admin/nss/${other}`
			);
		});
	});
}
