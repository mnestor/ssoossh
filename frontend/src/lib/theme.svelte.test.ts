import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('$app/environment', () => ({ browser: true }));

import { THEME_STORAGE_KEY, theme } from './theme.svelte';

/** listeners holds the change handlers the store registered, so a test can
 * simulate the OS theme changing under it. */
let listeners: ((event: MediaQueryListEvent) => void)[] = [];

/** stubMatchMedia installs a matchMedia jsdom does not provide. */
function stubMatchMedia(matches: boolean) {
	listeners = [];
	vi.stubGlobal('matchMedia', (query: string) => ({
		matches,
		media: query,
		addEventListener: (_: string, handler: (event: MediaQueryListEvent) => void) =>
			listeners.push(handler),
		removeEventListener: (_: string, handler: (event: MediaQueryListEvent) => void) => {
			listeners = listeners.filter((l) => l !== handler);
		}
	}));
}

/** osChangesTo fires the media query change the browser would. */
function osChangesTo(dark: boolean) {
	for (const listener of listeners) {
		listener({ matches: dark } as MediaQueryListEvent);
	}
}

beforeEach(() => {
	localStorage.clear();
	theme.preference = 'system';
	theme.systemPrefersDark = false;
	document.documentElement.classList.remove('dark');
	stubMatchMedia(false);
});

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('theme', () => {
	describe('resolution', () => {
		it('should follow a light OS when the preference is system', () => {
			theme.systemPrefersDark = false;
			expect(theme.resolved).toBe('light');
		});

		it('should follow a dark OS when the preference is system', () => {
			theme.systemPrefersDark = true;
			expect(theme.resolved).toBe('dark');
		});

		it('should stay light on a dark OS when light was chosen explicitly', () => {
			theme.systemPrefersDark = true;
			theme.set('light');
			expect(theme.resolved).toBe('light');
		});

		it('should stay dark on a light OS when dark was chosen explicitly', () => {
			theme.systemPrefersDark = false;
			theme.set('dark');
			expect(theme.resolved).toBe('dark');
		});
	});

	describe('start', () => {
		it('should default to system when nothing is stored', () => {
			theme.start();
			expect(theme.preference).toBe('system');
		});

		it('should restore a stored preference', () => {
			localStorage.setItem(THEME_STORAGE_KEY, 'dark');
			theme.start();
			expect(theme.preference).toBe('dark');
		});

		it('should ignore a stored value that is not a preference', () => {
			localStorage.setItem(THEME_STORAGE_KEY, 'chartreuse');
			theme.start();
			expect(theme.preference).toBe('system');
		});

		it('should pick up the OS setting at startup', () => {
			stubMatchMedia(true);
			theme.start();
			expect(theme.systemPrefersDark).toBe(true);
		});

		it('should follow the OS switching theme while it is running', () => {
			theme.start();
			osChangesTo(true);
			expect(theme.resolved).toBe('dark');
		});

		it('should stop following the OS once cleaned up', () => {
			const stop = theme.start();
			stop();
			osChangesTo(true);
			expect(theme.systemPrefersDark).toBe(false);
		});

		it('should follow the OS even when an explicit choice is stored', () => {
			// The OS setting is still tracked, so switching back to system
			// resolves correctly without a reload.
			localStorage.setItem(THEME_STORAGE_KEY, 'light');
			theme.start();
			osChangesTo(true);
			expect(theme.systemPrefersDark).toBe(true);
		});
	});

	describe('cycle', () => {
		it('should step from system to light', () => {
			theme.set('system');
			theme.cycle();
			expect(theme.preference).toBe('light');
		});

		it('should step from light to dark', () => {
			theme.set('light');
			theme.cycle();
			expect(theme.preference).toBe('dark');
		});

		it('should step from dark back to system', () => {
			theme.set('dark');
			theme.cycle();
			expect(theme.preference).toBe('system');
		});
	});

	describe('persistence', () => {
		it('should remember an explicit choice', () => {
			theme.set('dark');
			expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('dark');
		});

		it('should remember a return to system', () => {
			theme.set('system');
			expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('system');
		});

		it('should keep the choice for this page when storage refuses writes', () => {
			vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
				throw new Error('site data blocked');
			});
			theme.set('dark');
			expect(theme.preference).toBe('dark');
		});

		it('should follow the OS when storage refuses reads', () => {
			vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
				throw new Error('site data blocked');
			});
			theme.start();
			expect(theme.preference).toBe('system');
		});
	});

	describe('apply', () => {
		it('should add the dark class when the resolved theme is dark', () => {
			theme.set('dark');
			theme.apply();
			expect(document.documentElement).toHaveClass('dark');
		});

		it('should remove the dark class when the resolved theme is light', () => {
			document.documentElement.classList.add('dark');
			theme.set('light');
			theme.apply();
			expect(document.documentElement).not.toHaveClass('dark');
		});
	});
});
