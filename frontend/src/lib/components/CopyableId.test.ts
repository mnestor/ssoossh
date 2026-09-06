import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import CopyableId from './CopyableId.svelte';

// Test methodology: render the button and drive it with a stubbed clipboard.
// What matters is that the value on the clipboard is the whole identifier
// rather than the shortened one on screen -- the short form exists only so a
// UUID fits in a panel header -- and that a clipboard the browser refuses
// does not turn into an error state.

const FULL = '1f0a9c3e-0000-4000-8000-000000000001';

/** stubClipboard installs a spy writeText, or one that rejects. */
function stubClipboard(reject = false) {
	const writeText = vi.fn(() => (reject ? Promise.reject(new Error('denied')) : Promise.resolve()));
	vi.stubGlobal('navigator', { clipboard: { writeText } });
	return writeText;
}

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('CopyableId', () => {
	it('should show only the leading characters of the identifier', () => {
		stubClipboard();
		render(CopyableId, { value: FULL });
		expect(screen.getByRole('button')).toHaveTextContent('1f0a9');
	});

	it('should honour a requested length', () => {
		stubClipboard();
		render(CopyableId, { value: FULL, length: 8 });
		expect(screen.getByRole('button')).toHaveTextContent('1f0a9c3e');
	});

	// The whole point: the value on screen is truncated, and the value that
	// has to reach a search box or a ticket is not.
	it('should copy the whole identifier, not the shortened one', async () => {
		const writeText = stubClipboard();
		render(CopyableId, { value: FULL });

		await userEvent.click(screen.getByRole('button'));

		expect(writeText).toHaveBeenCalledWith(FULL);
	});

	it('should confirm the copy', async () => {
		stubClipboard();
		render(CopyableId, { value: FULL });

		await userEvent.click(screen.getByRole('button'));

		expect(screen.getByRole('button')).toHaveAccessibleName('ID copied');
	});

	// A tooltip is unreachable from a keyboard and invisible on a touch
	// screen, so the full value has to be in the accessible name too.
	it('should name the full identifier before it is copied', () => {
		stubClipboard();
		render(CopyableId, { value: FULL });
		expect(screen.getByRole('button')).toHaveAccessibleName(`Copy the full ID: ${FULL}`);
	});

	it('should keep the full identifier on the title for a pointer', () => {
		stubClipboard();
		render(CopyableId, { value: FULL });
		expect(screen.getByRole('button')).toHaveAttribute('title', FULL);
	});

	// A denied clipboard permission is not worth an error state: failing
	// loudly over a copy would be worse than not copying.
	it('should stay quiet when the browser refuses the clipboard', async () => {
		stubClipboard(true);
		render(CopyableId, { value: FULL });

		await userEvent.click(screen.getByRole('button'));

		expect(screen.getByRole('button')).not.toHaveAccessibleName('ID copied');
	});

	it('should use a supplied label', () => {
		stubClipboard();
		render(CopyableId, { value: FULL, label: 'Request' });
		expect(screen.getByRole('button')).toHaveAccessibleName(`Copy the full Request: ${FULL}`);
	});
});
