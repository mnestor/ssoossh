/**
 * The shared shape of a row in the rail.
 *
 * Rail rows are three different elements — a link to a page, a button that
 * cycles the theme, a button that ends the session — and the one thing that
 * must not vary between them is the box: same height, same icon position,
 * same gap, so the column reads as one list rather than three stacked
 * controls. Keeping the string in one place is what stops the three from
 * drifting apart the next time one of them is edited.
 *
 * State is returned as part of the string rather than layered on by the
 * caller. Two utilities setting the same property (`text-ink-muted` over
 * `text-accent`) resolve by their order in the generated stylesheet, not by
 * the order they appear in the attribute, so a caller adding an override
 * would be relying on something Tailwind does not promise.
 *
 * The classes are written out in full for the same reason: Tailwind scans
 * source text for literal class names, and a string assembled at runtime
 * from fragments would never be generated.
 */
export function railRowClass(collapsed: boolean, current: boolean = false): string {
	const base =
		'flex h-9 w-full items-center gap-2.5 rounded-md text-[13.5px] whitespace-nowrap transition';
	const width = collapsed ? 'justify-center' : 'px-2.5';
	const state = current
		? 'bg-accent-wash text-accent font-semibold'
		: 'text-ink-muted hover:bg-surface-muted hover:text-ink';
	return `${base} ${width} ${state}`;
}
