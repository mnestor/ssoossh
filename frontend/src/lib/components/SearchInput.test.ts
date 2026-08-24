import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import SearchInput from './SearchInput.svelte';

beforeEach(() => {
	vi.useFakeTimers();
});

afterEach(() => {
	vi.useRealTimers();
});

/** typeInto types into the search box under fake timers. */
async function typeInto(text: string) {
	const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
	await user.type(screen.getByRole('searchbox'), text);
	return user;
}

describe('SearchInput', () => {
	it('should label the box with the supplied label', () => {
		render(SearchInput, { label: 'Search users', onsearch: vi.fn() });
		expect(screen.getByRole('searchbox')).toHaveAccessibleName('Search users');
	});

	it('should not fire on every keystroke', async () => {
		const onsearch = vi.fn();
		render(SearchInput, { label: 'Search users', onsearch });
		await typeInto('ali');
		expect(onsearch).not.toHaveBeenCalled();
	});

	it('should fire once the typing settles', async () => {
		const onsearch = vi.fn();
		render(SearchInput, { label: 'Search users', onsearch });
		await typeInto('ali');
		await vi.advanceTimersByTimeAsync(400);
		expect(onsearch).toHaveBeenCalledExactlyOnceWith('ali');
	});

	it('should trim the term before reporting it', async () => {
		const onsearch = vi.fn();
		render(SearchInput, { label: 'Search users', onsearch });
		await typeInto('  ali  ');
		await vi.advanceTimersByTimeAsync(400);
		expect(onsearch).toHaveBeenCalledWith('ali');
	});

	it('should not re-fire when the settled term has not changed', async () => {
		const onsearch = vi.fn();
		render(SearchInput, { label: 'Search users', onsearch, value: 'ali' });
		await typeInto(' ');
		await vi.advanceTimersByTimeAsync(400);
		expect(onsearch).not.toHaveBeenCalled();
	});

	it('should clear the term and report it immediately', async () => {
		const onsearch = vi.fn();
		render(SearchInput, { label: 'Search users', onsearch, value: 'alice' });
		const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
		await user.click(screen.getByRole('button', { name: /clear/i }));
		expect(onsearch).toHaveBeenCalledExactlyOnceWith('');
	});

	it('should offer no clear button when the box is empty', () => {
		render(SearchInput, { label: 'Search users', onsearch: vi.fn() });
		expect(screen.queryByRole('button', { name: /clear/i })).not.toBeInTheDocument();
	});
});
