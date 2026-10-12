import { render, screen, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import { outcomeFilters, statusFilters, typeFilters } from '#lib/filters.js';
import FilterGroup from './FilterGroup.svelte';

// Test methodology: render one group (or two, for the id case) and read it
// the way a reader would, by role and name. jsdom applies no Tailwind, so
// both forms are in the tree at once: the chips (from `sm` up) and the
// select (below `sm`). Which one shows is asserted through the classes CSS
// chooses by, since there is no layout here to measure. What matters is
// that the select is named, says the same thing as the chips, and reports
// the same values.

/** renderGroup renders the type group with a spy for onselect. */
function renderGroup(selected = '', disabled = false) {
	const onselect = vi.fn();
	render(FilterGroup, {
		label: 'Type',
		options: typeFilters,
		selected,
		onselect,
		disabled,
		testid: 'type-filter'
	});
	return onselect;
}

describe('FilterGroup', () => {
	describe('the select below sm', () => {
		it('should take its accessible name from the group label', () => {
			renderGroup();
			expect(screen.getByRole('combobox', { name: 'Type' })).toBeInTheDocument();
		});

		it('should show the group label as visible text, not only as a name', () => {
			renderGroup();
			expect(screen.getByText('Type', { selector: 'label' })).not.toHaveClass('sr-only');
		});

		// One option per chip, with the same value and the same word, for
		// every group the certificate lists use.
		it.each([
			{ name: 'type', options: typeFilters },
			{ name: 'status', options: statusFilters },
			{ name: 'outcome', options: outcomeFilters }
		])('should offer the same values and words as the $name chips', ({ options }) => {
			render(FilterGroup, { label: 'Group', options, selected: '', onselect: vi.fn() });
			const select = screen.getByRole('combobox', { name: 'Group' });
			const offered = within(select)
				.getAllByRole('option')
				.map((option) => ({
					value: option.getAttribute('value'),
					label: option.textContent
				}));
			expect(offered).toEqual(options.map(({ value, label }) => ({ value, label })));
		});

		it.each([
			{ selected: '', want: 'All' },
			{ selected: 'pam', want: 'PAM' },
			{ selected: 'console', want: 'Console' }
		])('should show $want as chosen when the selection is "$selected"', ({ selected, want }) => {
			renderGroup(selected);
			expect(screen.getByRole('option', { name: want, selected: true })).toBeInTheDocument();
		});

		it('should report the chosen value when the reader picks an option', async () => {
			const onselect = renderGroup();
			await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Type' }), 'service');
			expect(onselect).toHaveBeenCalledWith('service');
		});

		it('should report the "any" value when the reader picks All', async () => {
			const onselect = renderGroup('pam');
			await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Type' }), 'All');
			expect(onselect).toHaveBeenCalledWith('');
		});

		it('should not be operable while the list behind it is reloading', () => {
			renderGroup('', true);
			expect(screen.getByRole('combobox', { name: 'Type' })).toBeDisabled();
		});

		it('should carry a test id derived from the group', () => {
			renderGroup();
			expect(screen.getByTestId('type-filter-select')).toBe(
				screen.getByRole('combobox', { name: 'Type' })
			);
		});

		// Two groups on one page must not share an id, or the second label
		// would name the first select.
		it('should name each select after its own group when two render', () => {
			render(FilterGroup, { label: 'Type', options: typeFilters, selected: '', onselect: vi.fn() });
			render(FilterGroup, {
				label: 'Status',
				options: statusFilters,
				selected: '',
				onselect: vi.fn()
			});
			expect(screen.getByRole('combobox', { name: 'Status' })).not.toBe(
				screen.getByRole('combobox', { name: 'Type' })
			);
		});
	});

	describe('the chips from sm up', () => {
		it('should name the chip group after the filter', () => {
			renderGroup();
			expect(screen.getByRole('group', { name: 'Filter by type' })).toBeInTheDocument();
		});

		it('should press the chip that matches the selection', () => {
			renderGroup('pam');
			expect(screen.getByRole('button', { name: 'PAM' })).toHaveAttribute('aria-pressed', 'true');
		});

		it('should report the chip value when a chip is pressed', async () => {
			const onselect = renderGroup();
			await userEvent.click(screen.getByRole('button', { name: 'Console' }));
			expect(onselect).toHaveBeenCalledWith('console');
		});
	});

	// The collapse is the component's own decision, made by CSS at `sm`.
	describe('which form shows', () => {
		it('should hide the chips below sm', () => {
			renderGroup();
			expect(screen.getByRole('group', { name: 'Filter by type' })).toHaveClass(
				'hidden',
				'sm:flex'
			);
		});

		it('should hide the select from sm up', () => {
			renderGroup();
			expect(screen.getByRole('combobox', { name: 'Type' }).parentElement).toHaveClass('sm:hidden');
		});
	});
});
