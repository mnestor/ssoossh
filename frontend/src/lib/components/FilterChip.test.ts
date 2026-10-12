import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import FilterChip from './FilterChip.svelte';

// Test methodology: render one chip and press it. What matters is the
// pressed state a screen reader reads, and that the label is on screen and
// in the accessible name -- a chip that showed or announced as a bare icon
// would be the regression this component exists to prevent.

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

	// A chip is never its glyph alone: the label is visible text, not a
	// hidden name. Below `sm` FilterGroup shows a select instead of chips.
	it('should show its label visibly when rendered', () => {
		render(FilterChip, {
			label: 'Approved',
			icon: 'circle-check',
			selected: false,
			onclick: vi.fn()
		});
		expect(screen.getByText('Approved')).not.toHaveClass('sr-only');
	});

	// A chip must never announce as a bare icon.
	it('should carry its label as its accessible name', () => {
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
