import { render, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { detail } from '#lib/detail.svelte.js';
import DetailToggle from './DetailToggle.svelte';

// Test methodology: render the toggle over the real detail store with
// storage and the save stubbed out. What matters is the pressed state a
// screen reader reads, that a press changes the one shared preference, that
// a failed save says so, and that a controlled toggle leaves the store
// alone.

/** stubSave answers PUT /api/users/me/display, or fails it. */
function stubSave(ok: boolean) {
	vi.stubGlobal(
		'fetch',
		vi.fn((_url: string, init: RequestInit) =>
			Promise.resolve(
				new Response(
					JSON.stringify(
						ok
							? { data: JSON.parse(String(init.body)), error: null }
							: { data: null, error: 'database is down' }
					),
					{ status: ok ? 200 : 500, headers: { 'Content-Type': 'application/json' } }
				)
			)
		)
	);
}

beforeEach(() => {
	vi.stubGlobal('localStorage', { getItem: () => null, setItem: () => undefined });
	detail.level = 'advanced';
	detail.saveError = null;
});

afterEach(() => {
	vi.unstubAllGlobals();
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
		stubSave(true);
		render(DetailToggle);

		await userEvent.click(screen.getByRole('button', { name: 'Simplified' }));

		await waitFor(() => expect(detail.level).toBe('simplified'));
	});

	it('should say so when the change could not be saved', async () => {
		stubSave(false);
		render(DetailToggle);

		await userEvent.click(screen.getByRole('button', { name: 'Simplified' }));

		expect(await screen.findByRole('alert')).toHaveTextContent('Not saved');
	});

	it('should report the given value when controlled', () => {
		render(DetailToggle, { value: 'simplified', onselect: () => undefined });

		expect(screen.getByRole('button', { name: 'Simplified' })).toHaveAttribute(
			'aria-pressed',
			'true'
		);
	});

	it('should hand a press to onselect and leave the shared preference alone when controlled', async () => {
		const onselect = vi.fn();
		render(DetailToggle, { value: 'advanced', onselect });

		await userEvent.click(screen.getByRole('button', { name: 'Simplified' }));

		expect([onselect.mock.calls[0]?.[0], detail.level]).toEqual(['simplified', 'advanced']);
	});
});
