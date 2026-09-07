import { browser } from '$app/environment';

/**
 * Where the phone layout ends, as a media query.
 *
 * Tailwind's `sm` breakpoint is 640px, so the phone layout is everything
 * under it — the same boundary `max-sm:` classes draw in markup. Stated
 * once because a control hidden by CSS at one width while script pins its
 * value at another is two layouts, not one.
 */
export const NARROW_QUERY = '(max-width: 639px)';

/**
 * watchNarrow reports whether the viewport is in the phone layout, now and
 * whenever it changes, and returns the teardown — so a caller can hand the
 * whole thing to an `$effect`.
 *
 * The subscription is the point rather than the initial read: a phone
 * rotates and a desktop window gets dragged narrow, and a control that only
 * exists above `sm` has to disappear and come back with it.
 */
export function watchNarrow(onchange: (narrow: boolean) => void): () => void {
	// jsdom provides no matchMedia, and neither does the server. Both mean
	// "nothing to measure": the caller keeps whatever it started with.
	//
	// not covered: the `!browser` half of this test. The unit tier renders
	// in jsdom, where `browser` is true, so only the matchMedia half can be
	// exercised; the other exists for the server pass, which has no window
	// to measure at all.
	if (!browser || typeof window.matchMedia !== 'function') {
		return () => {};
	}

	const query = window.matchMedia(NARROW_QUERY);
	onchange(query.matches);

	const handler = (event: MediaQueryListEvent) => onchange(event.matches);
	query.addEventListener('change', handler);
	return () => query.removeEventListener('change', handler);
}
