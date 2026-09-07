import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import CopyableId from './CopyableId.svelte';

// Test methodology: render the button and drive it with a stubbed clipboard.
// What matters is that the identifier is on screen in full, that the
// shortened form is there as the narrow-viewport fallback rather than as the
// only thing rendered, that the clipboard gets the whole value either way,
// and that a clipboard the browser refuses does not turn into an error
// state.

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
	// The whole identifier is what an operator quotes in a ticket or pastes
	// into a search box, and it is what the audit events and log lines
	// carry. A five-character stub made them read it out of a tooltip.
	it('should show the identifier in full', () => {
		stubClipboard();
		render(CopyableId, { value: FULL });
		expect(screen.getByText(FULL)).toBeInTheDocument();
	});

	// Both forms are rendered and CSS picks one, so the shortened form is
	// the fallback for a row with no space rather than the default.
	it('should keep a shortened form for a row with no room for the whole thing', () => {
		stubClipboard();
		render(CopyableId, { value: FULL });

		expect(screen.getByText(FULL)).toHaveClass('hidden', 'sm:inline');
		expect(screen.getByText('1f0a9')).toHaveClass('sm:hidden');
	});

	it('should honour a requested length', () => {
		stubClipboard();
		render(CopyableId, { value: FULL, length: 8 });
		expect(screen.getByText('1f0a9c3e')).toBeInTheDocument();
	});

	// True in both forms, and the reason the shortened one is safe to fall
	// back to: what reaches the clipboard never depends on the viewport.
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
