import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import SecretPanel from './SecretPanel.svelte';

// Test methodology: render the panel as a create or rotate answer would and
// drive the acknowledgement. The behaviour that matters is that Done cannot
// be pressed until the reader says the secret is stored, and that the file
// a host reads is the two-line form nssd expects.

const props = {
	keyId: 'nss_k7m4qp2x',
	secret: 'c2VjcmV0LXNlY3JldA',
	ondismiss: vi.fn()
};

afterEach(() => {
	vi.unstubAllGlobals();
	props.ondismiss.mockReset();
});

describe('SecretPanel', () => {
	it('should show the nss.key file as the key id then the secret', () => {
		render(SecretPanel, props);

		expect(screen.getByTestId('secret-panel-file').textContent).toBe(
			'nss_k7m4qp2x\nc2VjcmV0LXNlY3JldA\n'
		);
	});

	it('should hold Done until the secret is acknowledged', () => {
		render(SecretPanel, props);

		expect(screen.getByTestId('secret-panel-done')).toBeDisabled();
	});

	it('should say why Done is held when nothing is ticked', () => {
		render(SecretPanel, props);

		expect(screen.getByTestId('secret-panel-done-reason')).toHaveTextContent(
			'Tick the box once the secret is stored.'
		);
	});

	it('should dismiss when Done is pressed after the box is ticked', async () => {
		render(SecretPanel, props);

		await userEvent.click(screen.getByLabelText('I have stored this secret'));
		await userEvent.click(screen.getByTestId('secret-panel-done'));

		expect(props.ondismiss).toHaveBeenCalledOnce();
	});

	it('should copy the secret alone when its copy button is pressed', async () => {
		const writeText = vi.fn(() => Promise.resolve());
		vi.stubGlobal('navigator', { clipboard: { writeText } });
		render(SecretPanel, props);

		await userEvent.click(screen.getByTestId('secret-panel-copy-secret'));

		expect(writeText).toHaveBeenCalledWith('c2VjcmV0LXNlY3JldA');
	});

	it('should say the copy failed when the clipboard refuses', async () => {
		vi.stubGlobal('navigator', {
			clipboard: { writeText: () => Promise.reject(new Error('denied')) }
		});
		render(SecretPanel, props);

		await userEvent.click(screen.getByTestId('secret-panel-copy-file'));

		expect(screen.getByTestId('secret-panel-copy-failed')).toHaveTextContent('Copy failed');
	});

	it('should name when the previous secret stops when it is given', () => {
		render(SecretPanel, { ...props, previousExpiresAt: '2026-11-17T12:00:00Z' });

		expect(screen.getByTestId('secret-panel-grace')).toHaveTextContent(
			'The previous secret is accepted until'
		);
	});

	it('should not mention a previous secret when none is given', () => {
		render(SecretPanel, props);

		expect(screen.queryByTestId('secret-panel-grace')).not.toBeInTheDocument();
	});
});
