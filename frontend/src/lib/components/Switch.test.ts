import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import Switch from './Switch.svelte';

// Test methodology: render the switch alone. What matters is what a screen
// reader is told (role, state, a short name and the sentence as its
// description), that a press reports the opposite value, and that a press
// while saving is ignored.

describe('Switch', () => {
	it('should be a switch reporting its state', () => {
		render(Switch, { checked: true, title: 'Service code created', onchange: () => undefined });

		expect(screen.getByRole('switch')).toHaveAttribute('aria-checked', 'true');
	});

	it('should be named by its title alone', () => {
		render(Switch, {
			checked: false,
			title: 'Service code created',
			description: 'Sent when a code is created.',
			onchange: () => undefined
		});

		expect(screen.getByRole('switch')).toHaveAccessibleName('Service code created');
	});

	it('should be described by its sentence', () => {
		render(Switch, {
			checked: false,
			title: 'Service code created',
			description: 'Sent when a code is created.',
			onchange: () => undefined
		});

		expect(screen.getByRole('switch')).toHaveAccessibleDescription('Sent when a code is created.');
	});

	it('should report the opposite value when pressed', async () => {
		const onchange = vi.fn();
		render(Switch, { checked: false, title: 'Service code created', onchange });

		await userEvent.click(screen.getByRole('switch'));

		expect(onchange).toHaveBeenCalledWith(true);
	});

	it('should ignore a press while saving', async () => {
		const onchange = vi.fn();
		render(Switch, { checked: false, title: 'Service code created', onchange, busy: true });

		await userEvent.click(screen.getByRole('switch'));

		expect(onchange).not.toHaveBeenCalled();
	});

	it('should say it is busy while saving', () => {
		render(Switch, {
			checked: false,
			title: 'Service code created',
			onchange: () => undefined,
			busy: true
		});

		expect(screen.getByRole('switch')).toHaveAttribute('aria-busy', 'true');
	});
});
