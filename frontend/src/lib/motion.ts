import { browser } from '$app/environment';
import { cubicIn, cubicOut } from 'svelte/easing';

/**
 * The two durations every appearance and disappearance in the app is drawn
 * from.
 *
 * An entrance is slow enough to be followed and short enough not to be waited
 * on. An exit is about 40% faster, because a reader who has dismissed
 * something has already stopped looking at it, and matching the two makes the
 * dismissal feel like it did not take.
 *
 * These are also stated as `--duration-enter` and `--duration-exit` in
 * `app.css`, for the modal backdrop, which is painted by the browser rather
 * than by a Svelte transition and so has to be timed in CSS. The two must
 * agree; there is no way to read one from the other.
 */
export const ENTER_MS = 220;
export const EXIT_MS = 130;

/**
 * enterMs and exitMs are the durations to hand a Svelte transition.
 *
 * A Svelte transition is driven from JavaScript, so the
 * `prefers-reduced-motion` block in `app.css` cannot reach it: the duration
 * has to be asked for here instead. Zero rather than "no transition", so the
 * element still mounts and unmounts through the same code path and a
 * transition-end callback still fires.
 */
export function enterMs(): number {
	return reduced() ? 0 : ENTER_MS;
}

export function exitMs(): number {
	return reduced() ? 0 : EXIT_MS;
}

/** The query the CSS block in `app.css` is keyed on, stated once. */
const REDUCED_QUERY = '(prefers-reduced-motion: reduce)';

/**
 * reduced answers whether the reader has asked their system for less motion.
 *
 * Read straight from `matchMedia` rather than through Svelte's
 * `prefersReducedMotion`, which builds its `MediaQuery` at module scope and
 * so needs a `matchMedia` to exist the moment anything imports this file.
 * Nothing here needs the reactivity that buys: the answer is wanted once, at
 * the instant a transition starts.
 *
 * The guard is the one `watchNarrow` carries, for the same two reasons:
 * jsdom provides no `matchMedia` and neither does the prerender pass. Both
 * mean nobody has asked for anything, which is the full duration.
 *
 * not covered: the `!browser` half of this test. The unit tier renders in
 * jsdom, where `browser` is true, so only the matchMedia half can be
 * exercised; the other exists for the prerender pass, which has no window to
 * ask at all.
 */
function reduced(): boolean {
	if (!browser || typeof window.matchMedia !== 'function') {
		return false;
	}
	return window.matchMedia(REDUCED_QUERY).matches;
}

/**
 * Ease out on the way in, ease in on the way out.
 *
 * Something arriving decelerates into place, which is how a thing that has
 * come to rest behaves. Something leaving accelerates away, which is how a
 * thing that is gone behaves. Linear is for a spinner and a shimmer, and for
 * nothing that starts or stops.
 */
export const easeEnter = cubicOut;
export const easeExit = cubicIn;
