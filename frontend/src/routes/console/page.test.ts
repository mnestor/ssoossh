import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

// The page normalizes as it is typed, submits a code, and on success sends
// the browser to the approval page. Neither a router nor a real navigation
// belongs in a component test, so both are stubbed.
//
// vi.hoisted: vi.mock is lifted above the imports, so the state the
// factories close over has to be created up there with them.
const { goto, redirectIfUnauthenticated } = vi.hoisted(() => ({
	goto: vi.fn(() => Promise.resolve()),
	redirectIfUnauthenticated: vi.fn(() => false)
}));

vi.mock('$app/navigation', () => ({ goto }));
vi.mock('#lib/auth.js', async () => {
	const actual = await vi.importActual<typeof import('#lib/auth.js')>('#lib/auth.js');
	return { ...actual, redirectIfUnauthenticated };
});

import Page from './+page.svelte';

/** mockFetch stubs the global fetch with one enveloped response. */
function mockFetch(status: number, body: object = {}) {
	const fetchMock = vi.fn();
	fetchMock.mockResolvedValue(
		new Response(JSON.stringify({ data: body, error: status === 200 ? null : 'nope' }), {
			status,
			headers: { 'Content-Type': 'application/json' }
		})
	);
	vi.stubGlobal('fetch', fetchMock);
	return fetchMock;
}

/** settle lets the submit promise chain run to completion. */
function settle(): Promise<void> {
	return new Promise((resolve) => setTimeout(resolve, 0));
}

function codeInput(): HTMLInputElement {
	return screen.getByTestId('console-code-input') as HTMLInputElement;
}

afterEach(() => {
	vi.unstubAllGlobals();
	goto.mockClear();
	redirectIfUnauthenticated.mockClear();
});

describe('the console code entry page', () => {
	it('should render the code box', () => {
		render(Page);
		expect(screen.getByTestId('console-code-entry')).toBeInTheDocument();
	});

	it('should show the code grouped as the console prints it', async () => {
		render(Page);
		await userEvent.type(codeInput(), 'K7M4QP2X');
		expect(codeInput().value).toBe('K7M4-QP2X');
	});

	it('should upper-case a code typed in lower case', async () => {
		render(Page);
		await userEvent.type(codeInput(), 'k7m4qp2x');
		expect(codeInput().value).toBe('K7M4-QP2X');
	});

	it('should read a typed letter O as the digit zero', async () => {
		render(Page);
		await userEvent.type(codeInput(), 'K7M4QP2O');
		expect(codeInput().value).toBe('K7M4-QP20');
	});

	it('should ignore a character outside the alphabet', async () => {
		render(Page);
		await userEvent.type(codeInput(), 'K7M4!QP2X');
		expect(codeInput().value).toBe('K7M4-QP2X');
	});

	it('should disable the submit button until the code is complete', async () => {
		render(Page);
		await userEvent.type(codeInput(), 'K7M4QP2');
		expect(screen.getByTestId('console-code-submit')).toBeDisabled();
	});

	it('should enable the submit button once the code is complete', async () => {
		render(Page);
		await userEvent.type(codeInput(), 'K7M4QP2X');
		expect(screen.getByTestId('console-code-submit')).toBeEnabled();
	});

	it('should submit the normalized code, not the displayed one', async () => {
		const fetchMock = mockFetch(200, {
			request_id: 'req-1',
			approval_url: '/approve/req-1'
		});

		render(Page);
		await userEvent.type(codeInput(), 'k7m4qp2x');
		await userEvent.click(screen.getByTestId('console-code-submit'));
		await settle();

		const [, init] = fetchMock.mock.calls[0];
		const body = JSON.parse(String(init.body));
		expect(body.code).toBe('K7M4QP2X');
	});

	it('should send the browser to the approval page on success', async () => {
		mockFetch(200, { request_id: 'req-1', approval_url: '/approve/req-1' });

		render(Page);
		await userEvent.type(codeInput(), 'K7M4QP2X');
		await userEvent.click(screen.getByTestId('console-code-submit'));
		await settle();

		expect(goto).toHaveBeenCalledWith('/approve/req-1');
	});

	// Each failure gets its own testid so the e2e tier selects on that
	// rather than on prose.
	const failures: Array<{ name: string; status: number; testid: string }> = [
		{ name: 'an unknown code', status: 404, testid: 'console-code-failure-not-found' },
		{ name: 'an expired login', status: 410, testid: 'console-code-failure-expired' },
		{ name: 'a login someone else claimed', status: 403, testid: 'console-code-failure-claimed' }
	];

	for (const { name, status, testid } of failures) {
		it(`should distinguish ${name}`, async () => {
			mockFetch(status);

			render(Page);
			await userEvent.type(codeInput(), 'K7M4QP2X');
			await userEvent.click(screen.getByTestId('console-code-submit'));
			await settle();

			expect(screen.getByTestId(testid)).toBeInTheDocument();
			expect(goto).not.toHaveBeenCalled();
		});
	}

	it('should not render a failure when a signed-out visitor is redirected to login', async () => {
		redirectIfUnauthenticated.mockReturnValueOnce(true);
		mockFetch(401);

		render(Page);
		await userEvent.type(codeInput(), 'K7M4QP2X');
		await userEvent.click(screen.getByTestId('console-code-submit'));
		await settle();

		expect(screen.queryByTestId('console-code-failure-unknown')).not.toBeInTheDocument();
	});

	it('should clear a previous failure when the code is edited', async () => {
		mockFetch(404);

		render(Page);
		await userEvent.type(codeInput(), 'K7M4QP2X');
		await userEvent.click(screen.getByTestId('console-code-submit'));
		await settle();
		expect(screen.getByTestId('console-code-failure-not-found')).toBeInTheDocument();

		await userEvent.type(codeInput(), 'A');
		expect(screen.queryByTestId('console-code-failure-not-found')).not.toBeInTheDocument();
	});

	// The defence against being talked into approving someone else's login
	// starts here: a valid code comes from a screen in front of you.
	it('should say only to enter a code from a screen in front of you', () => {
		render(Page);
		expect(screen.getByTestId('console-code-entry')).toHaveTextContent(
			'Never enter one someone sends or reads to you.'
		);
	});

	it('should say I, L and O count as digits rather than ask the reader to map them', () => {
		render(Page);
		expect(screen.getByText(/I, L and O count as 1 and 0/)).toBeInTheDocument();
	});

	it('should say how long a code lasts', () => {
		render(Page);
		expect(screen.getByText(/Codes last a minute or two/)).toBeInTheDocument();
	});

	// A typo in the middle used to send the caret to the end, so fixing it
	// meant deleting back to it.
	it('should keep the caret where a character was inserted mid-code', async () => {
		render(Page);
		const input = codeInput() as HTMLInputElement;
		await userEvent.type(input, 'K7M4QP2');
		input.value = 'K7XM4-QP2';
		input.setSelectionRange(3, 3);
		input.dispatchEvent(new Event('input', { bubbles: true }));
		expect(input.selectionStart).toBe(3);
	});
});
