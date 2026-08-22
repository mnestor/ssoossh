import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('$app/environment', () => ({ browser: true }));

import { theme } from '$lib/theme.svelte';
import ThemeToggle from './ThemeToggle.svelte';

beforeEach(() => {
	localStorage.clear();
	theme.preference = 'system';
	theme.systemPrefersDark = false;
});

describe('ThemeToggle', () => {
	it('should say it is following the system setting by default', () => {
		render(ThemeToggle);
		expect(screen.getByRole('button')).toHaveAccessibleName(/following your system setting/);
	});

	it('should name what pressing it will do', () => {
		render(ThemeToggle);
		expect(screen.getByRole('button')).toHaveAccessibleName(/Switch to light/);
	});

	it('should move to light when pressed from system', async () => {
		render(ThemeToggle);
		await userEvent.click(screen.getByRole('button'));
		expect(theme.preference).toBe('light');
	});

	it('should move to dark when pressed from light', async () => {
		theme.preference = 'light';
		render(ThemeToggle);
		await userEvent.click(screen.getByRole('button'));
		expect(theme.preference).toBe('dark');
	});

	it('should return to system when pressed from dark', async () => {
		theme.preference = 'dark';
		render(ThemeToggle);
		await userEvent.click(screen.getByRole('button'));
		expect(theme.preference).toBe('system');
	});

	it('should update its label after the theme changes', async () => {
		render(ThemeToggle);
		await userEvent.click(screen.getByRole('button'));
		expect(screen.getByRole('button')).toHaveAccessibleName(/Theme: light/);
	});
});
