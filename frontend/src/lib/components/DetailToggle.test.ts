import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { detail } from '#lib/detail.svelte.js';
import DetailToggle from './DetailToggle.svelte';

// Test methodology: render the toggle over the real detail store with
// storage stubbed out. What matters is the pressed state a screen reader
// reads and that a press changes the one shared preference.

beforeEach(() => {
	vi.stubGlobal('localStorage', { getItem: () => null, setItem: () => undefined });
	detail.level = 'advanced';
});

describe('DetailToggle', () => {
	it('should report the current level as pressed', () => {
		render(DetailToggle);

		expect(screen.getByRole('button', { name: 'Advanced' })).toHaveAttribute(
			'aria-pressed',
			'true'
		);
	});

	it('should report the other level as not pressed', () => {
		render(DetailToggle);

		expect(screen.getByRole('button', { name: 'Simplified' })).toHaveAttribute(
			'aria-pressed',
			'false'
		);
	});

	it('should change the shared preference when pressed', async () => {
		render(DetailToggle);

		await userEvent.click(screen.getByRole('button', { name: 'Simplified' }));

		expect(detail.level).toBe('simplified');
	});
});
