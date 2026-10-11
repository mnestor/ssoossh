import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { createRawSnippet } from 'svelte';
import { describe, expect, it, vi } from 'vitest';

import ConfirmModal from './ConfirmModal.svelte';

// Test methodology: open the dialog with and without a reason field and
// press the confirming button. What matters is the gate: an audited action
// cannot be confirmed without a reason, and the one action the server takes
// no reason for is not blocked on a field it does not show.

const consequence = createRawSnippet(() => ({
	render: () => '<span>What this does.</span>'
}));

/** props builds the dialog's props around one confirm callback. */
function props(onconfirm: (reason: string) => void, reasonless = false) {
	return {
		title: 'Do the thing?',
		children: consequence,
		confirmLabel: 'Do it',
		busyLabel: 'Doing…',
		confirmTestid: 'confirm',
		reasonless,
		onconfirm,
		oncancel: vi.fn()
	};
}

describe('ConfirmModal', () => {
	it('should hold the confirming button until a reason is typed', () => {
		render(ConfirmModal, props(vi.fn()));

		expect(screen.getByTestId('confirm')).toBeDisabled();
	});

	it('should show no reason field when reasonless', () => {
		render(ConfirmModal, props(vi.fn(), true));

		expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
	});

	it('should confirm at once when reasonless', async () => {
		const onconfirm = vi.fn();
		render(ConfirmModal, props(onconfirm, true));

		await userEvent.click(screen.getByTestId('confirm'));

		expect(onconfirm).toHaveBeenCalledWith('');
	});
});
