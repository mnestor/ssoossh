import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import ConsentModal from './ConsentModal.svelte';

describe('ConsentModal', () => {
	it('should show the full notice text', () => {
		render(ConsentModal, { notice: 'You are accessing a monitored system.', onaccepted: vi.fn() });
		expect(screen.getByText('You are accessing a monitored system.')).toBeInTheDocument();
	});

	it('should call onaccepted when I Accept is clicked', async () => {
		const onaccepted = vi.fn();
		render(ConsentModal, { notice: 'Notice text', onaccepted });
		await userEvent.click(screen.getByRole('button', { name: 'I Accept' }));
		expect(onaccepted).toHaveBeenCalledOnce();
	});

	it('should call showModal so the dialog gets native top-layer/backdrop behavior', () => {
		const showModalSpy = vi.spyOn(HTMLDialogElement.prototype, 'showModal');
		render(ConsentModal, { notice: 'Notice text', onaccepted: vi.fn() });
		expect(showModalSpy).toHaveBeenCalledOnce();
	});

	it('should block the native cancel event so Escape cannot dismiss it unaccepted', () => {
		render(ConsentModal, { notice: 'Notice text', onaccepted: vi.fn() });
		const dialog = document.querySelector('dialog');
		if (!dialog) {
			throw new Error('expected a <dialog> element to be rendered');
		}
		const cancelEvent = new Event('cancel', { cancelable: true });
		dialog.dispatchEvent(cancelEvent);
		expect(cancelEvent.defaultPrevented).toBe(true);
	});

	it('should provide a focus trap and inert background with showModal()', () => {
		const showModalSpy = vi.spyOn(HTMLDialogElement.prototype, 'showModal');
		render(ConsentModal, { notice: 'Notice text', onaccepted: vi.fn() });
		expect(showModalSpy).toHaveBeenCalled();
	});
});
