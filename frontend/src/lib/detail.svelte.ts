import { browser } from '$app/env';
import { getContext, setContext } from 'svelte';

/** Where the detail level is kept. Read after first paint, like the rail:
 * one frame of the default is a smaller cost than blocking paint on a
 * storage read. */
export const DETAIL_STORAGE_KEY = 'ssoossh:detail';

/**
 * How much of the approval and information pages a reader sees.
 *
 * `advanced` is the full page and the default, so nobody loses detail
 * without choosing to. `simplified` keeps what a reader needs to decide or
 * to understand the result, and drops the diagnostic detail an expert
 * compares (DESIGN.md, "Detail levels").
 */
export type DetailLevel = 'simplified' | 'advanced';

/** isDetailLevel narrows a stored string to a level, so anything else
 * stored under the key reads as no preference. */
function isDetailLevel(value: string | null): value is DetailLevel {
	return value === 'simplified' || value === 'advanced';
}

/**
 * The reader's detail preference: one value, shared by the "Display"
 * section on /preferences and the toggle in each covered page's heading,
 * so both always agree.
 */
class Detail {
	/** The current level. Persisted. */
	level = $state<DetailLevel>('advanced');

	/** True when the simplified pages are showing, for the many readers. */
	get simplified(): boolean {
		return this.level === 'simplified';
	}

	/**
	 * start loads the stored preference. Called once by the root layout.
	 *
	 * Guarded like the rail and the theme: a browser with site data blocked
	 * throws on read, and a display preference is not worth failing a page
	 * load over.
	 */
	start(): void {
		if (!browser) {
			return;
		}
		try {
			const stored = localStorage.getItem(DETAIL_STORAGE_KEY);
			if (isDetailLevel(stored)) {
				this.level = stored;
			}
		} catch {
			// Site data blocked. The default holds.
		}
	}

	/** set changes the level and remembers it. */
	set(level: DetailLevel): void {
		this.level = level;
		if (!browser) {
			return;
		}
		try {
			localStorage.setItem(DETAIL_STORAGE_KEY, level);
		} catch {
			// Site data blocked: the choice holds for this page, not beyond it.
		}
	}
}

export const detail = new Detail();

const SCOPE = Symbol('detail-scope');

/**
 * coverWithDetail marks the calling page as one the detail level covers.
 * Call it once in the page's script. DetailRows inside it then set their
 * facts one size larger at the simplified level (DESIGN.md, "Simplified
 * type"); everywhere else they keep the dense size, whatever the level.
 */
export function coverWithDetail(): void {
	setContext(SCOPE, true);
}

/** inDetailScope reports whether the calling component sits on a page the
 * detail level covers. Call it during component initialisation. */
export function inDetailScope(): boolean {
	return getContext<boolean | undefined>(SCOPE) ?? false;
}
