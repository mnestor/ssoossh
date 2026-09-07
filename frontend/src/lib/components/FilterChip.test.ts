import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import FilterChip from './FilterChip.svelte';

// Test methodology: render one chip and press it. What matters is the
// pressed state a screen reader reads, and that the narrow-screen form
// hides the label visually without dropping it from the accessible name --
// a chip that announced as a bare icon would be the regression this
// component exists to prevent.

describe('FilterChip', () => {
	it('should report itself pressed when it is the selection', () => {
		render(FilterChip, { label: 'Denied', icon: 'circle-x', selected: true, onclick: vi.fn() });
		expect(screen.getByRole('button', { name: 'Denied' })).toHaveAttribute('aria-pressed', 'true');
	});

	it('should report itself unpressed when it is not', () => {
		render(FilterChip, { label: 'Denied', icon: 'circle-x', selected: false, onclick: vi.fn() });
		expect(screen.getByRole('button', { name: 'Denied' })).toHaveAttribute('aria-pressed', 'false');
	});

	it('should call back when pressed', async () => {
		const onclick = vi.fn();
		render(FilterChip, { label: 'Denied', icon: 'circle-x', selected: false, onclick });

		await userEvent.click(screen.getByRole('button', { name: 'Denied' }));

		expect(onclick).toHaveBeenCalledOnce();
	});

	// Named on a desktop, an icon on a phone: eight chips with their words
	// wrap a filter row onto three lines on a narrow screen.
	it('should name itself from sm up and hide the label below it', () => {
		render(FilterChip, {
			label: 'Approved',
			icon: 'circle-check',
			selected: false,
			onclick: vi.fn()
		});
		expect(screen.getByText('Approved')).toHaveClass('sr-only', 'sm:not-sr-only');
	});

	// The whole point of hiding the label rather than omitting it: a chip
	// must never announce as a bare icon.
	it('should keep its accessible name at every width', () => {
		render(FilterChip, {
			label: 'Approved',
			icon: 'circle-check',
			selected: false,
			onclick: vi.fn()
		});
		expect(screen.getByRole('button', { name: 'Approved' })).toBeInTheDocument();
	});

	it('should not be pressable while the list behind it is reloading', () => {
		render(FilterChip, {
			label: 'PAM',
			icon: 'terminal-2',
			selected: false,
			disabled: true,
			onclick: vi.fn()
		});
		expect(screen.getByRole('button', { name: 'PAM' })).toBeDisabled();
	});
});
