import { browser } from '$app/environment';

/**
 * Where the collapsed preference is kept. Unlike the theme, nothing reads
 * this before paint: a rail that renders expanded for one frame and then
 * narrows is a smaller sin than blocking first paint on a storage read, and
 * the rail is not what the eye lands on first.
 */
export const RAIL_STORAGE_KEY = 'ssoossh:rail-collapsed';

/**
 * The rail's two independent pieces of state.
 *
 * `collapsed` is a preference: the viewer asked for the icon-only rail and
 * expects it on the next page and the next visit. `drawerOpen` is a
 * transient: it only means anything below the breakpoint where the rail is
 * off-canvas, and every navigation closes it. Keeping them apart is what
 * stops a phone visitor's open drawer from being remembered as a desktop
 * preference.
 */
class Rail {
	/** True when the rail is showing icons only. Persisted. */
	collapsed = $state(false);

	/** True when the off-canvas drawer is showing. Never persisted. */
	drawerOpen = $state(false);

	/**
	 * start loads the stored preference. Called once by the root layout.
	 *
	 * Storage access is guarded the same way the theme guards it: a browser
	 * with site data blocked throws on read rather than returning null, and
	 * a rail width is not worth failing a page load over.
	 */
	start(): void {
		if (!browser) {
			return;
		}
		try {
			this.collapsed = localStorage.getItem(RAIL_STORAGE_KEY) === 'true';
		} catch {
			// Site data blocked. Start expanded, same as a first visit.
		}
	}

	/** toggleCollapsed flips the rail width and remembers the choice. */
	toggleCollapsed(): void {
		this.collapsed = !this.collapsed;
		if (!browser) {
			return;
		}
		try {
			localStorage.setItem(RAIL_STORAGE_KEY, String(this.collapsed));
		} catch {
			// Site data blocked: the choice holds for this page, not beyond it.
		}
	}

	/** toggleDrawer opens or closes the narrow-viewport drawer. */
	toggleDrawer(): void {
		this.drawerOpen = !this.drawerOpen;
	}

	/** closeDrawer is what every navigation and the scrim call. */
	closeDrawer(): void {
		this.drawerOpen = false;
	}
}

export const rail = new Rail();
