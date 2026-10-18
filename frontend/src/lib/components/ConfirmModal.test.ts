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

	describe('typed confirmation', () => {
		/** typedProps is a reasonless dialog that wants `expected` typed. */
		function typedProps(onconfirm: (reason: string) => void) {
			return {
				...props(onconfirm, true),
				confirmText: 'nss_k7m4qp2x',
				confirmTextNoun: 'the key id',
				confirmTextTestid: 'typed'
			};
		}

		const cases: { name: string; typed: string; enabled: boolean }[] = [
			{ name: 'nothing is typed', typed: '', enabled: false },
			{ name: 'a prefix is typed', typed: 'nss_k7m4', enabled: false },
			{ name: 'the case differs', typed: 'NSS_K7M4QP2X', enabled: false },
			{ name: 'the exact text is typed', typed: 'nss_k7m4qp2x', enabled: true },
			{ name: 'the exact text has a trailing blank', typed: 'nss_k7m4qp2x ', enabled: true }
		];

		for (const { name, typed, enabled } of cases) {
			it(`should ${enabled ? 'enable' : 'hold'} the confirming button when ${name}`, async () => {
				render(ConfirmModal, typedProps(vi.fn()));
				if (typed) {
					await userEvent.type(screen.getByTestId('typed'), typed);
				}

				expect(screen.getByTestId('confirm').hasAttribute('disabled')).toBe(!enabled);
			});
		}

		it('should name the text to type in the field label', () => {
			render(ConfirmModal, typedProps(vi.fn()));

			expect(screen.getByLabelText(/Type the key id nss_k7m4qp2x to confirm/)).toBeInTheDocument();
		});
	});
});
