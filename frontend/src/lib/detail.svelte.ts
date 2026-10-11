import { browser } from '$app/env';
import { getContext, setContext } from 'svelte';

import { updateDisplayPreferences } from '#lib/api/endpoints.js';
import type { DetailLevel } from '#lib/api/types.js';

export type { DetailLevel };

/** Where the last level the server reported is cached, so a reload paints
 * at that level instead of flashing the default until /users/me answers.
 * A cache only: the server holds the preference (DESIGN.md, "Detail
 * levels"). */
export const DETAIL_STORAGE_KEY = 'ssoossh:detail';

/** isDetailLevel narrows a stored string to a level, so anything else
 * stored under the key reads as no preference. */
function isDetailLevel(value: string | null): value is DetailLevel {
	return value === 'simplified' || value === 'advanced';
}

/**
 * The reader's web UI detail level: one value, shared by the "Display"
 * section on /preferences and the toggle in each covered page's heading, so
 * both always agree.
 *
 * Stored on the server and delivered with /users/me, so it follows the
 * person between browsers. `simplified` is the default until the server
 * says otherwise; the server's own default is advanced for SOC and admin
 * and simplified for everyone else, fixed at their first login.
 */
class Detail {
	/** The current level. */
	level = $state<DetailLevel>('simplified');

	/** Set when the last change could not be saved, cleared by the next. */
	saveError = $state<string | null>(null);

	/** True when the simplified pages are showing, for the many readers. */
	get simplified(): boolean {
		return this.level === 'simplified';
	}

	/**
	 * start paints from the cached level until the session arrives. Called
	 * once by the root layout.
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
			const cached = localStorage.getItem(DETAIL_STORAGE_KEY);
			if (isDetailLevel(cached)) {
				this.level = cached;
			}
		} catch {
			// Site data blocked. The default holds until the session loads.
		}
	}

	/**
	 * adopt takes the level the server reported, which wins over the cache.
	 * Called by the root layout whenever the session's identity loads.
	 */
	adopt(level: DetailLevel): void {
		this.level = level;
		this.cache(level);
	}

	/**
	 * set changes the level at once and saves it to the account. A failed
	 * save puts the previous level back and says so in saveError, rather
	 * than leaving a page showing a choice the next load will not.
	 */
	async set(level: DetailLevel): Promise<void> {
		const previous = this.level;
		this.level = level;
		this.saveError = null;
		try {
			const stored = await updateDisplayPreferences(level);
			this.adopt(stored.detail_level);
		} catch (cause) {
			// Only undo when nothing newer has replaced this choice.
			if (this.level === level) {
				this.level = previous;
			}
			this.saveError = cause instanceof Error ? cause.message : 'the server did not answer';
		}
	}

	/** cache remembers level for the next first paint in this browser. */
	private cache(level: DetailLevel): void {
		if (!browser) {
			return;
		}
		try {
			localStorage.setItem(DETAIL_STORAGE_KEY, level);
		} catch {
			// Site data blocked: the next load paints the default first.
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
