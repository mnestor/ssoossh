import { describe, expect, it, vi, afterEach } from 'vitest';

import { NARROW_QUERY, watchNarrow } from './viewport';

afterEach(() => {
	vi.unstubAllGlobals();
});

/**
 * stubMatchMedia installs the matchMedia jsdom does not provide, and hands
 * back what was asked of it: the query string, the listeners registered, and
 * the switch that resizes the viewport.
 */
function stubMatchMedia(matches: boolean) {
	const listeners = new Set<(event: MediaQueryListEvent) => void>();
	const asked: string[] = [];
	const list = {
		matches,
		addEventListener: (_: string, fn: (event: MediaQueryListEvent) => void) => {
			listeners.add(fn);
		},
		removeEventListener: (_: string, fn: (event: MediaQueryListEvent) => void) => {
			listeners.delete(fn);
		}
	};
	vi.stubGlobal('matchMedia', (query: string) => {
		asked.push(query);
		return list;
	});
	return {
		asked,
		listeners,
		resize(next: boolean) {
			list.matches = next;
			for (const fn of listeners) {
				fn({ matches: next } as MediaQueryListEvent);
			}
		}
	};
}

describe('watchNarrow', () => {
	it('should ask about the width below Tailwind’s sm breakpoint', () => {
		const media = stubMatchMedia(false);
		watchNarrow(() => {});
		expect(media.asked).toEqual([NARROW_QUERY]);
	});

	it('should report a narrow viewport straight away', () => {
		stubMatchMedia(true);
		const seen: boolean[] = [];
		watchNarrow((narrow) => seen.push(narrow));
		expect(seen).toEqual([true]);
	});

	it('should report a wide viewport straight away', () => {
		stubMatchMedia(false);
		const seen: boolean[] = [];
		watchNarrow((narrow) => seen.push(narrow));
		expect(seen).toEqual([false]);
	});

	it('should report a viewport that narrows after the first read', () => {
		const media = stubMatchMedia(false);
		const seen: boolean[] = [];
		watchNarrow((narrow) => seen.push(narrow));

		media.resize(true);

		expect(seen).toEqual([false, true]);
	});

	it('should report a viewport that widens again', () => {
		const media = stubMatchMedia(true);
		const seen: boolean[] = [];
		watchNarrow((narrow) => seen.push(narrow));

		media.resize(false);

		expect(seen).toEqual([true, false]);
	});

	it('should stop listening once torn down', () => {
		const media = stubMatchMedia(false);
		const stop = watchNarrow(() => {});

		stop();

		expect(media.listeners.size).toBe(0);
	});

	// A browser without matchMedia is one nothing can be measured on, and a
	// filter pinned to a width nobody can read is worse than one left alone.
	it('should report nothing when the browser has no matchMedia', () => {
		vi.stubGlobal('matchMedia', undefined);
		const seen: boolean[] = [];

		expect(watchNarrow((narrow) => seen.push(narrow))).toBeInstanceOf(Function);
		expect(seen).toEqual([]);
	});
});
