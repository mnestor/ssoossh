import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import Alert from './Alert.svelte';

// Test methodology: render an error alert with and without a retry
// callback. The retry button is the way out of every failed load, so what
// matters is that it appears only when there is something to retry and
// that pressing it reaches the page.

describe('Alert', () => {
	it('should call back when Try again is pressed', async () => {
		const onretry = vi.fn();
		render(Alert, { variant: 'error', title: 'Could not load', onretry });

		await userEvent.click(screen.getByRole('button', { name: 'Try again' }));

		expect(onretry).toHaveBeenCalledOnce();
	});

	it('should offer no retry when there is nothing to retry', () => {
		render(Alert, { variant: 'error', title: 'That did not go through' });

		expect(screen.queryByRole('button', { name: 'Try again' })).not.toBeInTheDocument();
	});

	it('should derive the retry selector from the alert test id when one is given', () => {
		render(Alert, { variant: 'error', title: 'Could not load', testid: 'load', onretry: vi.fn() });

		expect(screen.getByTestId('load-retry')).toBeInTheDocument();
	});
});
