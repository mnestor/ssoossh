import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import UserMenu from './UserMenu.svelte';

describe('UserMenu', () => {
	it('should show the signed-in identity', () => {
		render(UserMenu, { label: 'alice@example.com', onsignout: vi.fn() });
		expect(screen.getByText('alice@example.com')).toBeInTheDocument();
	});

	// The address is hidden below the sm breakpoint, where a person icon
	// stands in for it. The accessible name has to carry it regardless, or
	// the trigger reads as an unlabelled button on a phone.
	it('should name the trigger with the identity even when it shows an icon', () => {
		render(UserMenu, { label: 'alice@example.com', onsignout: vi.fn() });
		expect(screen.getByRole('button', { name: 'alice@example.com' })).toBeInTheDocument();
	});

	it('should keep the menu closed until it is opened', () => {
		render(UserMenu, { label: 'alice@example.com', onsignout: vi.fn() });
		expect(screen.queryByRole('menu')).not.toBeInTheDocument();
	});

	it('should report its collapsed state to assistive technology', () => {
		render(UserMenu, { label: 'alice@example.com', onsignout: vi.fn() });
		expect(screen.getByRole('button')).toHaveAttribute('aria-expanded', 'false');
	});

	it('should open the menu when the trigger is clicked', async () => {
		render(UserMenu, { label: 'alice@example.com', onsignout: vi.fn() });
		await userEvent.click(screen.getByRole('button'));
		expect(screen.getByRole('menu')).toBeInTheDocument();
	});

	it('should call onsignout when Sign out is chosen', async () => {
		const onsignout = vi.fn();
		render(UserMenu, { label: 'alice@example.com', onsignout });
		await userEvent.click(screen.getByRole('button'));
		await userEvent.click(screen.getByRole('menuitem', { name: 'Sign out' }));
		expect(onsignout).toHaveBeenCalledOnce();
	});

	it('should close the menu when Escape is pressed', async () => {
		render(UserMenu, { label: 'alice@example.com', onsignout: vi.fn() });
		await userEvent.click(screen.getByRole('button'));
		await userEvent.keyboard('{Escape}');
		expect(screen.queryByRole('menu')).not.toBeInTheDocument();
	});

	it('should close the menu when a click lands outside it', async () => {
		render(UserMenu, { label: 'alice@example.com', onsignout: vi.fn() });
		await userEvent.click(screen.getByRole('button'));
		await userEvent.click(document.body);
		expect(screen.queryByRole('menu')).not.toBeInTheDocument();
	});

	it('should show a busy label while signing out', async () => {
		render(UserMenu, { label: 'alice@example.com', busy: true, onsignout: vi.fn() });
		await userEvent.click(screen.getByRole('button'));
		expect(screen.getByRole('menuitem', { name: 'Signing out…' })).toBeInTheDocument();
	});

	it('should disable the sign-out item while signing out', async () => {
		render(UserMenu, { label: 'alice@example.com', busy: true, onsignout: vi.fn() });
		await userEvent.click(screen.getByRole('button'));
		expect(screen.getByRole('menuitem', { name: 'Signing out…' })).toBeDisabled();
	});

	it('should link to the account page', async () => {
		render(UserMenu, { label: 'alice@example.com', onsignout: vi.fn() });
		await userEvent.click(screen.getByRole('button'));
		expect(screen.getByRole('menuitem', { name: 'Account' })).toHaveAttribute('href', '/account');
	});

	it('should link to the preferences page', async () => {
		render(UserMenu, { label: 'alice@example.com', onsignout: vi.fn() });
		await userEvent.click(screen.getByRole('button'));
		expect(screen.getByRole('menuitem', { name: 'Preferences' })).toHaveAttribute(
			'href',
			'/preferences'
		);
	});

	it('should close the menu when the account link is chosen', async () => {
		render(UserMenu, { label: 'alice@example.com', onsignout: vi.fn() });
		await userEvent.click(screen.getByRole('button'));
		await userEvent.click(screen.getByRole('menuitem', { name: 'Account' }));
		expect(screen.queryByRole('menu')).not.toBeInTheDocument();
	});
});
