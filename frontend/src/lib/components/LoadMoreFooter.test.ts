import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import LoadMoreFooter from './LoadMoreFooter.svelte';

// Test methodology: render the footer, press its control, and have the
// load stand in for a page by rerendering the footer with the outcome
// (another page, the last page, a failure) before it resolves. What is
// asserted is where focus lands once the load has settled, because that is
// the behaviour the footer owns: the control the reader pressed may be gone.

/** The props the footer is opened with: one page shown, more on the server. */
const base = {
	summary: '25 shown, 25 older not loaded',
	hasMore: true,
	busy: false,
	error: null as string | null,
	errorTitle: 'Could not load more things',
	testid: 'things'
};

/** Outcome is the footer's props once a load has finished. */
type Outcome = Partial<typeof base>;

const morePage: Outcome = { summary: '50 shown, 25 older not loaded', hasMore: true };
const lastPage: Outcome = { summary: '50 shown', hasMore: false };
const failed: Outcome = { error: 'The server did not answer.' };

/** The element focus should be on, found by its role in the footer. */
type Landing = 'count' | 'load-more' | 'retry';

/** landing finds the element a case expects focus on. */
function landing(where: Landing): HTMLElement {
	switch (where) {
		case 'count':
			return screen.getByTestId('things-count');
		case 'load-more':
			return screen.getByTestId('things-load-more');
		case 'retry':
			return screen.getByTestId('things-more-error-retry');
	}
}

/**
 * mount renders the footer with a load that applies each outcome in turn,
 * one per press, the way a page's state changes under it.
 */
function mount(start: Outcome, ...outcomes: Outcome[]) {
	let props = { ...base, ...start };
	let call = 0;
	const onmore = vi.fn(async () => {
		// Busy, then settled: the button is disabled while the page is read.
		await view.rerender({ ...props, busy: true, error: null });
		props = { ...props, ...outcomes[Math.min(call++, outcomes.length - 1)], busy: false };
		await view.rerender(props);
	});
	const view = render(LoadMoreFooter, { ...props, onmore });
	return { view, onmore };
}

afterEach(() => {
	document.body.innerHTML = '';
});

describe('LoadMoreFooter focus', () => {
	const cases: {
		name: string;
		start: Outcome;
		press: Landing;
		outcome: Outcome;
		lands: Landing;
	}[] = [
		{
			name: 'should keep focus on Load more when another page remains',
			start: {},
			press: 'load-more',
			outcome: morePage,
			lands: 'load-more'
		},
		{
			name: 'should move focus to the count line when the last page loads',
			start: {},
			press: 'load-more',
			outcome: lastPage,
			lands: 'count'
		},
		{
			name: 'should move focus to Try again when Load more fails',
			start: {},
			press: 'load-more',
			outcome: failed,
			lands: 'retry'
		},
		{
			name: 'should move focus to Load more when a retry loads a page with more after it',
			start: failed,
			press: 'retry',
			outcome: { ...morePage, error: null },
			lands: 'load-more'
		},
		{
			name: 'should move focus to the count line when a retry loads the last page',
			start: failed,
			press: 'retry',
			outcome: { ...lastPage, error: null },
			lands: 'count'
		},
		{
			name: 'should move focus back to Try again when a retry fails again',
			start: failed,
			press: 'retry',
			outcome: failed,
			lands: 'retry'
		}
	];

	for (const { name, start, press, outcome, lands } of cases) {
		it(name, async () => {
			mount(start, outcome);

			await userEvent.click(landing(press));

			await vi.waitFor(() => expect(document.activeElement).toBe(landing(lands)));
		});
	}

	it('should leave focus where the reader put it when they moved on during the load', async () => {
		const elsewhere = document.body.appendChild(document.createElement('input'));
		let props = { ...base };
		const view = render(LoadMoreFooter, {
			...props,
			onmore: async () => {
				elsewhere.focus();
				props = { ...props, ...lastPage };
				await view.rerender(props);
			}
		});

		await userEvent.click(screen.getByTestId('things-load-more'));

		await vi.waitFor(() => expect(screen.queryByTestId('things-load-more')).toBeNull());
		expect(document.activeElement).toBe(elsewhere);
	});

	it('should not move focus when the footer is gone by the time the load settles', async () => {
		let resolve = () => {};
		const view = render(LoadMoreFooter, {
			...base,
			onmore: () => new Promise<void>((done) => (resolve = done))
		});
		await userEvent.click(screen.getByTestId('things-load-more'));

		view.unmount();
		resolve();

		await vi.waitFor(() => expect(document.activeElement).toBe(document.body));
	});

	it('should leave focus alone when the last page loads into a footer with no count line', async () => {
		mount({ summary: '' }, { ...lastPage, summary: '' });

		await userEvent.click(screen.getByTestId('things-load-more'));

		await vi.waitFor(() => expect(screen.queryByTestId('things-load-more')).toBeNull());
		expect(document.activeElement).toBe(document.body);
	});
});

describe('LoadMoreFooter', () => {
	it('should keep the count line out of the Tab order', () => {
		mount({});

		expect(screen.getByTestId('things-count')).toHaveAttribute('tabindex', '-1');
	});

	it('should render the summary as given', () => {
		mount({});

		expect(screen.getByTestId('things-count')).toHaveTextContent(/^25 shown, 25 older not loaded$/);
	});

	it('should render no count line when the summary is empty', () => {
		mount({ summary: '' });

		expect(screen.queryByTestId('things-count')).toBeNull();
	});

	it('should render neither button nor alert once the last page is in', () => {
		mount(lastPage);

		expect(screen.queryByRole('button')).toBeNull();
	});

	it('should show the error under its title in the button place when a page failed', () => {
		mount(failed);

		expect(screen.getByTestId('things-more-error')).toHaveTextContent(
			/Could not load more things.*The server did not answer\./
		);
	});

	it('should show Loading on a disabled button while a page is read', () => {
		mount({ busy: true });

		expect(screen.getByTestId('things-load-more')).toHaveTextContent('Loading…');
	});

	const retries: { name: string; withRetry: boolean; called: 'onmore' | 'onretry' }[] = [
		{ name: 'should retry with onretry when one is given', withRetry: true, called: 'onretry' },
		{
			name: 'should retry with onmore when no onretry is given',
			withRetry: false,
			called: 'onmore'
		}
	];

	for (const { name, withRetry, called } of retries) {
		it(name, async () => {
			const onmore = vi.fn();
			const onretry = vi.fn();
			render(LoadMoreFooter, { ...base, ...failed, onmore, ...(withRetry ? { onretry } : {}) });

			await userEvent.click(screen.getByTestId('things-more-error-retry'));

			expect({ onmore, onretry }[called]).toHaveBeenCalledOnce();
		});
	}
});
