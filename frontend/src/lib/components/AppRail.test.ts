import { render, screen, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import AppRail from './AppRail.svelte';
import { rail } from '$lib/rail.svelte';
import { session } from '$lib/session.svelte';
import { resetFakePage } from '$lib/testing/page.svelte';

// Test methodology: render the rail directly with the session singleton put
// into each state and the page URL driven by the fake. The rail is now the
// only way to every screen in the app, so the behaviours pinned are the ones
// that would strand someone: which destinations appear for which identity,
// which one reads as current, and whether the label survives a collapse.

vi.mock('$app/state', async () => {
	const { fakePage } = await import('$lib/testing/page.svelte');
	return { page: fakePage };
});

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

/** labelsIn lists the visible link text inside the rail, in order. */
function railLinkLabels(): string[] {
	const nav = screen.getByRole('navigation', { name: 'Main' });
	return within(nav)
		.getAllByTestId('rail-item')
		.map((item) => item.textContent?.trim() ?? '');
}

beforeEach(() => {
	vi.unstubAllGlobals();
	vi.stubGlobal('localStorage', {
		getItem: () => null,
		setItem: () => {}
	});
	resetFakePage('http://localhost/dashboard');
	session.clear();
	rail.collapsed = false;
	rail.drawerOpen = false;
});

describe('app rail', () => {
	it('should offer every primary destination to a signed-in user', () => {
		session.user = signedInUser(false);
		session.resolved = true;

		render(AppRail, { onsignout: () => {} });

		expect(railLinkLabels()).toEqual([
			'Dashboard',
			'History',
			'Service codes',
			'Console login',
			'alice@example.com',
			'Preferences'
		]);
	});

	// Admin used to hang off a line in the account dropdown. It is a peer of
	// the other destinations now, but still only for an auditor.
	it('should not offer the admin group to a non-auditor', () => {
		session.user = signedInUser(false);
		session.resolved = true;

		render(AppRail, { onsignout: () => {} });

		expect(screen.queryByTestId('rail-admin-group')).not.toBeInTheDocument();
		expect(screen.queryByRole('link', { name: 'Audit log' })).not.toBeInTheDocument();
	});

	it('should name every admin section for an auditor', () => {
		// On an admin route, where the group is open on arrival.
		resetFakePage('http://localhost/admin/users');
		session.user = signedInUser(true);
		session.resolved = true;

		render(AppRail, { onsignout: () => {} });

		const labels = railLinkLabels();
		expect(labels.slice(4, 12)).toEqual([
			'Users',
			'Certificates',
			'Service codes',
			'Config',
			'Directory',
			'Claims echo',
			'Audit log',
			'Diagnostics'
		]);
	});

	it('should mark the destination being viewed as the current page', () => {
		session.user = signedInUser(false);
		session.resolved = true;

		render(AppRail, { onsignout: () => {} });

		expect(screen.getByRole('link', { name: 'Dashboard' })).toHaveAttribute('aria-current', 'page');
		expect(screen.getByRole('link', { name: 'History' })).not.toHaveAttribute('aria-current');
	});

	// A detail page sits under its section rather than at it, and must not
	// leave the rail with nothing marked.
	it('should keep a section current on a detail page beneath it', () => {
		resetFakePage('http://localhost/admin/users/user-123');
		session.user = signedInUser(true);
		session.resolved = true;

		render(AppRail, { onsignout: () => {} });

		expect(screen.getByRole('link', { name: 'Users' })).toHaveAttribute('aria-current', 'page');
	});

	// Arriving in the admin area with the group shut would hide the section
	// list from the very page that needs it.
	it('should open the admin group on arrival at an admin route', () => {
		resetFakePage('http://localhost/admin/audit');
		session.user = signedInUser(true);
		session.resolved = true;

		render(AppRail, { onsignout: () => {} });

		expect(screen.getByTestId('rail-admin-group')).toHaveAttribute('aria-expanded', 'true');
	});

	it('should keep the admin group shut elsewhere', () => {
		session.user = signedInUser(true);
		session.resolved = true;

		render(AppRail, { onsignout: () => {} });

		expect(screen.getByTestId('rail-admin-group')).toHaveAttribute('aria-expanded', 'false');
	});

	it('should reveal the admin sections when the group is opened', async () => {
		session.user = signedInUser(true);
		session.resolved = true;

		render(AppRail, { onsignout: () => {} });
		await userEvent.click(screen.getByTestId('rail-admin-group'));

		expect(screen.getByRole('link', { name: 'Audit log' })).toBeInTheDocument();
	});

	// The whole point of the icon rail is that it is still navigable, so the
	// accessible name has to survive the labels going visually hidden.
	it('should keep every destination named when the rail is collapsed', () => {
		rail.collapsed = true;
		session.user = signedInUser(false);
		session.resolved = true;

		render(AppRail, { onsignout: () => {} });

		expect(screen.getByRole('link', { name: 'Dashboard' })).toBeInTheDocument();
		expect(screen.getByRole('link', { name: 'Console login' })).toBeInTheDocument();
	});

	// A control that says "Collapse rail" on an already-collapsed rail names
	// the opposite of what it does.
	it('should offer to expand the rail when it is collapsed', () => {
		rail.collapsed = true;
		session.user = signedInUser(false);
		session.resolved = true;

		render(AppRail, { onsignout: () => {} });

		expect(screen.getByTestId('rail-collapse')).toHaveAccessibleName('Expand rail');
	});

	it('should offer to collapse the rail when it is expanded', () => {
		session.user = signedInUser(false);
		session.resolved = true;

		render(AppRail, { onsignout: () => {} });

		expect(screen.getByTestId('rail-collapse')).toHaveAccessibleName('Collapse rail');
	});

	it('should collapse the rail when the control is pressed', async () => {
		session.user = signedInUser(false);
		session.resolved = true;

		render(AppRail, { onsignout: () => {} });
		await userEvent.click(screen.getByTestId('rail-collapse'));

		expect(rail.collapsed).toBe(true);
	});

	it('should state the identity the session is acting as', () => {
		session.user = signedInUser(false);
		session.resolved = true;

		render(AppRail, { onsignout: () => {} });

		expect(screen.getByRole('link', { name: 'alice@example.com' })).toBeInTheDocument();
	});

	// An identity with no address still has to be nameable, or the rail's
	// bottom row becomes an unlabelled icon.
	it('should fall back to the username when the identity carries no address', () => {
		session.user = { ...signedInUser(false), email: '' };
		session.resolved = true;

		render(AppRail, { onsignout: () => {} });

		expect(screen.getByRole('link', { name: 'alice' })).toBeInTheDocument();
	});

	it('should raise the sign-out request when sign out is pressed', async () => {
		const onsignout = vi.fn();
		session.user = signedInUser(false);
		session.resolved = true;

		render(AppRail, { onsignout });
		await userEvent.click(screen.getByRole('button', { name: 'Sign out' }));

		expect(onsignout).toHaveBeenCalledOnce();
	});

	it('should refuse a second sign-out while one is in flight', () => {
		session.user = signedInUser(false);
		session.resolved = true;

		render(AppRail, { onsignout: () => {}, signingOut: true });

		expect(screen.getByRole('button', { name: 'Signing out…' })).toBeDisabled();
	});

	it('should show the deployment name beside the wordmark', () => {
		session.user = signedInUser(false);
		session.resolved = true;

		render(AppRail, { onsignout: () => {}, orgName: 'Acme Corp' });

		expect(screen.getByText('Acme Corp')).toBeInTheDocument();
	});

	// The drawer is open behind the tap that navigated; leaving it over the
	// page the viewer just asked for is the bug.
	it('should close the drawer when a destination is activated', async () => {
		rail.drawerOpen = true;
		session.user = signedInUser(false);
		session.resolved = true;

		render(AppRail, { onsignout: () => {} });
		await userEvent.click(screen.getByRole('link', { name: 'History' }));

		expect(rail.drawerOpen).toBe(false);
	});
});
