import { render, screen, waitFor, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import RailUserMenu from './RailUserMenu.svelte';
import { session } from '$lib/session.svelte';
import { resetFakePage } from '$lib/testing/page.svelte';

// Test methodology: render the drop-up on its own with the session singleton
// and the page URL driven by the fake. What is pinned here is the disclosure
// contract — the popover is shut until asked for, everything the footer used
// to show in the open column is inside it, and every way out of it works,
// because a popover that will not close is a trap over the rail's own
// trigger.

vi.mock('$app/state', async () => {
	const { fakePage } = await import('$lib/testing/page.svelte');
	return { page: fakePage };
});

/** signedInUser is a minimal session user, for the name in the popover head. */
function signedInUser() {
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
		is_auditor: false
	};
}

/** open renders the menu shut and then presses its trigger. */
async function open(props: Record<string, unknown> = {}) {
	render(RailUserMenu, { identity: 'alice@example.com', onsignout: () => {}, ...props });
	await userEvent.click(screen.getByTestId('rail-user-trigger'));
}

beforeEach(() => {
	vi.unstubAllGlobals();
	vi.stubGlobal('localStorage', {
		getItem: () => null,
		setItem: () => {}
	});
	resetFakePage('http://localhost/dashboard');
	session.clear();
	session.user = signedInUser();
	session.resolved = true;
});

describe('rail user menu', () => {
	it('should keep the popover shut until the identity row is pressed', () => {
		render(RailUserMenu, { identity: 'alice@example.com', onsignout: () => {} });

		expect(screen.queryByTestId('rail-user-menu')).not.toBeInTheDocument();
	});

	it('should say the popover is shut before it is opened', () => {
		render(RailUserMenu, { identity: 'alice@example.com', onsignout: () => {} });

		expect(screen.getByTestId('rail-user-trigger')).toHaveAttribute('aria-expanded', 'false');
	});

	it('should say the popover is open once it is opened', async () => {
		await open();

		expect(screen.getByTestId('rail-user-trigger')).toHaveAttribute('aria-expanded', 'true');
	});

	// These are the destinations the rail's footer used to stack in the open
	// column. One lost to the popover would be one lost entirely.
	it('should carry every destination the footer used to stack', async () => {
		await open();

		const menu = within(screen.getByTestId('rail-user-menu'));
		expect(
			menu.getAllByTestId('rail-user-menu-item').map((item) => item.textContent?.trim())
		).toEqual(['Account', 'Preferences']);
	});

	it('should offer the theme control inside the popover', async () => {
		await open();

		const menu = within(screen.getByTestId('rail-user-menu'));
		expect(menu.getByRole('button', { name: /^Theme:/ })).toBeInTheDocument();
	});

	it('should offer sign out inside the popover', async () => {
		await open();

		expect(screen.getByTestId('rail-sign-out')).toBeInTheDocument();
	});

	it('should raise the sign-out request when sign out is pressed', async () => {
		const onsignout = vi.fn();
		await open({ onsignout });
		await userEvent.click(screen.getByTestId('rail-sign-out'));

		expect(onsignout).toHaveBeenCalledOnce();
	});

	// The trigger truncates to the rail's width, so the popover's head is the
	// one place a long identity is legible in full.
	it('should name the identity in full at the head of the popover', async () => {
		await open();

		const menu = within(screen.getByTestId('rail-user-menu'));
		expect(menu.getByText('Alice Ashworth')).toBeInTheDocument();
	});

	it('should omit the display name when the identity carries none', async () => {
		session.user = { ...signedInUser(), name: '' };
		await open();

		const menu = within(screen.getByTestId('rail-user-menu'));
		expect(menu.queryByText('Alice Ashworth')).not.toBeInTheDocument();
	});

	it('should close the popover on Escape', async () => {
		await open();
		await userEvent.keyboard('{Escape}');

		expect(screen.queryByTestId('rail-user-menu')).not.toBeInTheDocument();
	});

	// Escape must not leave the caret at the top of the document, or a
	// keyboard user has to tab the whole rail again to get back.
	it('should return focus to the trigger when Escape closes the popover', async () => {
		await open();
		await userEvent.keyboard('{Escape}');

		expect(screen.getByTestId('rail-user-trigger')).toHaveFocus();
	});

	it('should close the popover on a press outside it', async () => {
		await open();
		await userEvent.click(document.body);

		await waitFor(() => expect(screen.queryByTestId('rail-user-menu')).not.toBeInTheDocument());
	});

	it('should close the popover when its trigger is pressed again', async () => {
		await open();
		await userEvent.click(screen.getByTestId('rail-user-trigger'));

		expect(screen.queryByTestId('rail-user-menu')).not.toBeInTheDocument();
	});

	// A destination inside the popover leaves two layers over the page it
	// just asked for: the popover, and the drawer the popover sits in.
	it('should close the drawer when a destination inside it is activated', async () => {
		const onnavigate = vi.fn();
		await open({ onnavigate });
		await userEvent.click(screen.getByRole('link', { name: 'Preferences' }));

		expect(onnavigate).toHaveBeenCalledOnce();
	});

	// With the rows behind a shut popover, the trigger is the only thing left
	// that can say the viewer is standing on one of them.
	it('should mark the trigger as current on a page the popover owns', async () => {
		resetFakePage('http://localhost/preferences');

		render(RailUserMenu, { identity: 'alice@example.com', onsignout: () => {} });

		expect(screen.getByTestId('rail-user-trigger')).toHaveClass('text-accent');
	});

	it('should leave the trigger unmarked on a page the popover does not own', () => {
		render(RailUserMenu, { identity: 'alice@example.com', onsignout: () => {} });

		expect(screen.getByTestId('rail-user-trigger')).not.toHaveClass('text-accent');
	});

	// Collapsed, the label goes visually hidden rather than away, so the
	// trigger is never an unnamed icon.
	it('should keep the identity named when the rail is collapsed', () => {
		render(RailUserMenu, {
			identity: 'alice@example.com',
			collapsed: true,
			onsignout: () => {}
		});

		expect(screen.getByTestId('rail-user-trigger')).toHaveAccessibleName('alice@example.com');
	});
});
