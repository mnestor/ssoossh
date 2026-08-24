import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import type { PageMeta } from '$lib/api/types';
import Pager from './Pager.svelte';

/** meta builds a page envelope, overriding only what a case cares about. */
function meta(overrides: Partial<PageMeta> = {}): PageMeta {
	return { total: 120, limit: 25, offset: 0, page: 1, page_count: 5, ...overrides };
}

describe('Pager', () => {
	it('should say which page of how many is showing', () => {
		render(Pager, { meta: meta({ offset: 50, page: 3 }), onpage: vi.fn() });
		expect(screen.getByText(/Page 3 of 5/)).toBeInTheDocument();
	});

	it('should report the total matching the search, not the page size', () => {
		render(Pager, { meta: meta(), onpage: vi.fn() });
		expect(screen.getByText(/120/)).toBeInTheDocument();
	});

	it('should disable previous on the first page', () => {
		render(Pager, { meta: meta(), onpage: vi.fn() });
		expect(screen.getByRole('button', { name: /previous/i })).toBeDisabled();
	});

	it('should disable next on the last page', () => {
		render(Pager, { meta: meta({ offset: 100, page: 5 }), onpage: vi.fn() });
		expect(screen.getByRole('button', { name: /next/i })).toBeDisabled();
	});

	it('should ask for the previous window by offset', async () => {
		const onpage = vi.fn();
		render(Pager, { meta: meta({ offset: 50, page: 3 }), onpage });
		await userEvent.click(screen.getByRole('button', { name: /previous/i }));
		expect(onpage).toHaveBeenCalledWith(25);
	});

	it('should ask for the next window by offset', async () => {
		const onpage = vi.fn();
		render(Pager, { meta: meta({ offset: 50, page: 3 }), onpage });
		await userEvent.click(screen.getByRole('button', { name: /next/i }));
		expect(onpage).toHaveBeenCalledWith(75);
	});

	it('should jump straight to a numbered page', async () => {
		const onpage = vi.fn();
		render(Pager, { meta: meta(), onpage });
		await userEvent.click(screen.getByRole('button', { name: 'Page 4' }));
		expect(onpage).toHaveBeenCalledWith(75);
	});

	it('should mark the current page for assistive technology', () => {
		render(Pager, { meta: meta({ offset: 50, page: 3 }), onpage: vi.fn() });
		expect(screen.getByRole('button', { name: 'Page 3' })).toHaveAttribute('aria-current', 'page');
	});

	it('should elide the middle of a long run of pages', () => {
		render(Pager, {
			meta: meta({ total: 1000, page: 20, offset: 475, page_count: 40 }),
			onpage: vi.fn()
		});
		expect(screen.getByRole('button', { name: 'Page 1' })).toBeInTheDocument();
		expect(screen.getByRole('button', { name: 'Page 40' })).toBeInTheDocument();
		expect(screen.queryByRole('button', { name: 'Page 10' })).not.toBeInTheDocument();
	});

	it('should render nothing when a single page holds everything', () => {
		const { container } = render(Pager, {
			meta: meta({ total: 4, page_count: 1 }),
			onpage: vi.fn()
		});
		expect(container.querySelector('nav')).toBeNull();
	});

	it('should disable every control while a page is loading', () => {
		render(Pager, { meta: meta({ offset: 50, page: 3 }), onpage: vi.fn(), busy: true });
		expect(screen.getByRole('button', { name: /previous/i })).toBeDisabled();
		expect(screen.getByRole('button', { name: /next/i })).toBeDisabled();
	});

	it('should name itself for assistive technology', () => {
		render(Pager, { meta: meta(), onpage: vi.fn() });
		expect(screen.getByRole('navigation', { name: /pagination/i })).toBeInTheDocument();
	});
});
