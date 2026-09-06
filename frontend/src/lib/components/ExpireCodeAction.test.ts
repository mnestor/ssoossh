import { render, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import { ApiError } from '$lib/api/client';
import ExpireCodeAction from './ExpireCodeAction.svelte';

// Test methodology: render the control with a stub expiry and drive it the
// way a person would. The behaviour worth pinning is the one that was
// broken: the server requires a reason for enrollment.expired, and the
// admin panel used to send none, so every press came back 400. The control
// must therefore be unable to submit without one.

/** open puts the control into its confirming state, where the field is. */
async function open(user: ReturnType<typeof userEvent.setup>) {
	await user.click(screen.getByRole('button', { name: 'Expire this code' }));
}

describe('expire code action', () => {
	it('should ask for confirmation before retiring anything', () => {
		const expire = vi.fn();

		render(ExpireCodeAction, { expire, onexpired: () => {} });

		expect(screen.getByRole('button', { name: 'Expire this code' })).toBeInTheDocument();
		expect(screen.queryByTestId('expire-reason')).not.toBeInTheDocument();
		expect(expire).not.toHaveBeenCalled();
	});

	// The reason is server-validated, so a control that let it be empty
	// would be a button whose only outcome is a 400.
	it('should refuse to submit without a reason', async () => {
		const user = userEvent.setup();
		const expire = vi.fn();

		render(ExpireCodeAction, { expire, onexpired: () => {} });
		await open(user);

		expect(screen.getByTestId('expire-confirm')).toBeDisabled();
	});

	it('should refuse to submit a reason of only spaces', async () => {
		const user = userEvent.setup();
		const expire = vi.fn();

		render(ExpireCodeAction, { expire, onexpired: () => {} });
		await open(user);
		await user.type(screen.getByTestId('expire-reason'), '   ');

		expect(screen.getByTestId('expire-confirm')).toBeDisabled();
	});

	it('should send the reason it was given', async () => {
		const user = userEvent.setup();
		const expire = vi.fn().mockResolvedValue({ expired: true });

		render(ExpireCodeAction, { expire, onexpired: () => {} });
		await open(user);
		await user.type(screen.getByTestId('expire-reason'), 'job decommissioned');
		await user.click(screen.getByTestId('expire-confirm'));

		await waitFor(() => expect(expire).toHaveBeenCalledWith('job decommissioned'));
	});

	// Leading and trailing space is the caller's slip, not their intent, and
	// the server trims it anyway.
	it('should trim the reason before sending it', async () => {
		const user = userEvent.setup();
		const expire = vi.fn().mockResolvedValue({ expired: true });

		render(ExpireCodeAction, { expire, onexpired: () => {} });
		await open(user);
		await user.type(screen.getByTestId('expire-reason'), '  retired  ');
		await user.click(screen.getByTestId('expire-confirm'));

		await waitFor(() => expect(expire).toHaveBeenCalledWith('retired'));
	});

	it('should report the code retired once the call succeeds', async () => {
		const user = userEvent.setup();
		const onexpired = vi.fn();

		render(ExpireCodeAction, { expire: vi.fn().mockResolvedValue({}), onexpired });
		await open(user);
		await user.type(screen.getByTestId('expire-reason'), 'done with it');
		await user.click(screen.getByTestId('expire-confirm'));

		await waitFor(() => expect(onexpired).toHaveBeenCalledOnce());
	});

	// A refusal has to say which of the two things went wrong, because the
	// fix is different: one is somebody else's code, the other is gone.
	it('should explain a refusal as not holding the account', async () => {
		const user = userEvent.setup();
		const expire = vi.fn().mockRejectedValue(new ApiError(403, 'forbidden'));

		render(ExpireCodeAction, { expire, onexpired: () => {} });
		await open(user);
		await user.type(screen.getByTestId('expire-reason'), 'trying it on');
		await user.click(screen.getByTestId('expire-confirm'));

		expect(await screen.findByText(/do not hold the service account/i)).toBeInTheDocument();
	});

	it('should explain a missing code as no longer existing', async () => {
		const user = userEvent.setup();
		const expire = vi.fn().mockRejectedValue(new ApiError(404, 'gone'));

		render(ExpireCodeAction, { expire, onexpired: () => {} });
		await open(user);
		await user.type(screen.getByTestId('expire-reason'), 'tidying up');
		await user.click(screen.getByTestId('expire-confirm'));

		expect(await screen.findByText(/no longer exists/i)).toBeInTheDocument();
	});

	// A failed attempt must not report success to the caller, which would
	// close the panel over an error nobody read.
	it('should not report the code retired when the call fails', async () => {
		const user = userEvent.setup();
		const onexpired = vi.fn();

		render(ExpireCodeAction, {
			expire: vi.fn().mockRejectedValue(new ApiError(403, 'forbidden')),
			onexpired
		});
		await open(user);
		await user.type(screen.getByTestId('expire-reason'), 'nope');
		await user.click(screen.getByTestId('expire-confirm'));

		await screen.findByText(/do not hold the service account/i);
		expect(onexpired).not.toHaveBeenCalled();
	});

	it('should drop the reason when the confirmation is cancelled', async () => {
		const user = userEvent.setup();

		render(ExpireCodeAction, { expire: vi.fn(), onexpired: () => {} });
		await open(user);
		await user.type(screen.getByTestId('expire-reason'), 'changed my mind');
		await user.click(screen.getByRole('button', { name: 'Cancel' }));
		await open(user);

		expect(screen.getByTestId('expire-reason')).toHaveValue('');
	});
});
