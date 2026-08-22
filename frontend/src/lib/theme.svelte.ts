import { browser } from '$app/environment';

/**
 * What the viewer asked for. `system` is the default and defers to the OS,
 * which is a third state rather than a computed one: someone who has never
 * chosen should keep following their OS when it changes, and someone who
 * picked light on a dark machine should stay on light.
 */
export type ThemePreference = 'system' | 'light' | 'dark';

/** The theme actually rendered, once `system` has been resolved. */
export type ResolvedTheme = 'light' | 'dark';

/**
 * Where the preference is kept. Read by the pre-paint script in app.html as
 * well as by this module — change one and the other has to follow, or the
 * page flashes the wrong theme before hydration.
 */
export const THEME_STORAGE_KEY = 'ssoossh:theme';

/** isPreference narrows a value read back out of storage, which can be anything. */
function isPreference(value: string | null): value is ThemePreference {
	return value === 'system' || value === 'light' || value === 'dark';
}

/** The order the toggle steps through. */
const cycleOrder: ThemePreference[] = ['system', 'light', 'dark'];

class Theme {
	/** The viewer's choice. */
	preference = $state<ThemePreference>('system');

	/** The OS setting, tracked live so `system` follows it without a reload. */
	systemPrefersDark = $state(false);

	/** resolved is the theme to actually render. */
	get resolved(): ResolvedTheme {
		if (this.preference === 'system') {
			return this.systemPrefersDark ? 'dark' : 'light';
		}
		return this.preference;
	}

	/**
	 * start loads the stored preference and begins tracking the OS setting,
	 * returning a cleanup. Called once by the root layout.
	 *
	 * Storage access is guarded: a browser with site data blocked throws on
	 * read rather than returning null, and a theme is not worth failing a
	 * page load over — that case simply follows the OS.
	 */
	start(): () => void {
		if (!browser) {
			return () => {};
		}

		try {
			const stored = localStorage.getItem(THEME_STORAGE_KEY);
			if (isPreference(stored)) {
				this.preference = stored;
			}
		} catch {
			// Site data blocked. Follow the OS, same as a first visit.
		}

		const query = window.matchMedia('(prefers-color-scheme: dark)');
		this.systemPrefersDark = query.matches;

		const onChange = (event: MediaQueryListEvent) => {
			this.systemPrefersDark = event.matches;
		};
		query.addEventListener('change', onChange);
		return () => query.removeEventListener('change', onChange);
	}

	/** set records a choice and remembers it for next time. */
	set(preference: ThemePreference): void {
		this.preference = preference;
		if (!browser) {
			return;
		}
		try {
			localStorage.setItem(THEME_STORAGE_KEY, preference);
		} catch {
			// Site data blocked: the choice holds for this page, not beyond it.
		}
	}

	/** cycle steps system → light → dark → system. */
	cycle(): void {
		const next = cycleOrder[(cycleOrder.indexOf(this.preference) + 1) % cycleOrder.length];
		this.set(next);
	}

	/** apply puts the resolved theme on <html>, where the tokens hang off it. */
	apply(): void {
		if (!browser) {
			return;
		}
		document.documentElement.classList.toggle('dark', this.resolved === 'dark');
	}
}

export const theme = new Theme();
