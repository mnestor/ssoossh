import { render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import LoadingBlock from './LoadingBlock.svelte';

describe('LoadingBlock', () => {
	beforeEach(() => {
		vi.useFakeTimers();
	});

	afterEach(() => {
		vi.useRealTimers();
	});

	/** settle advances past the 300ms gate and lets Svelte flush the update. */
	async function settle(ms = 300) {
		await vi.advanceTimersByTimeAsync(ms);
	}

	it('should render nothing before the 300ms gate has elapsed', async () => {
		render(LoadingBlock, { testid: 'block' });
		await settle(299);
		expect(screen.queryByTestId('block')).not.toBeInTheDocument();
	});

	it('should render the placeholder once the 300ms gate has elapsed', async () => {
		render(LoadingBlock, { testid: 'block' });
		await settle();
		expect(screen.getByTestId('block')).toBeInTheDocument();
	});

	it('should hide the placeholder from assistive technology', async () => {
		render(LoadingBlock, { testid: 'block' });
		await settle();
		expect(screen.getByTestId('block')).toHaveAttribute('aria-hidden', 'true');
	});

	it('should announce nothing when no label is given', async () => {
		render(LoadingBlock, { testid: 'block' });
		await settle();
		expect(screen.queryByRole('status')).not.toBeInTheDocument();
	});

	it('should announce the label when one is given', () => {
		render(LoadingBlock, { label: 'Loading your account…' });
		expect(screen.getByRole('status')).toHaveTextContent('Loading your account…');
	});

	it('should announce the label before the visual gate has elapsed', () => {
		render(LoadingBlock, { label: 'Loading your account…' });
		expect(screen.getByRole('status')).toBeInTheDocument();
	});

	// One shimmering bar per stand-in row, so a reader gets the shape of the
	// answer rather than a count of grey boxes that means nothing.
	const shapes: { shape: 'rows' | 'table' | 'lines'; count: number; bars: number }[] = [
		{ shape: 'rows', count: 3, bars: 9 },
		{ shape: 'table', count: 4, bars: 12 },
		{ shape: 'lines', count: 5, bars: 5 }
	];

	for (const { shape, count, bars } of shapes) {
		it(`should draw ${bars} bars for ${count} ${shape}`, async () => {
			const { container } = render(LoadingBlock, { shape, count, testid: 'block' });
			await settle();
			expect(container.querySelectorAll('.skeleton')).toHaveLength(bars);
		});
	}

	it('should draw nothing when the count is zero', async () => {
		const { container } = render(LoadingBlock, { shape: 'lines', count: 0, testid: 'block' });
		await settle();
		expect(container.querySelectorAll('.skeleton')).toHaveLength(0);
	});
});
