<script lang="ts">
	import Icon from './Icon.svelte';

	// One choice in a filter row: an icon, a label, and a pressed state.
	//
	// It exists because the certificate history grew a second filter and the
	// two were written differently — the type row as pill chips with icons
	// and no focus ring, the outcome row as a joined segmented control with
	// one. Two controls answering two questions about the same list should
	// not look like two different kinds of thing, and the missing focus ring
	// on one of them is what a second copy of a pattern reliably loses.
	//
	// Always named: the label is on screen at every width the chip is. It
	// used to shrink to its glyph below `sm`, which left a reader nothing to
	// read without hovering; FilterGroup now shows a select there instead.
	//
	// A button rather than a link: filtering is a change of view, not a
	// change of address. The list is paged client-side over what has been
	// loaded, so there is no URL that would restore it anyway.
	interface Props {
		label: string;
		/** Icon name from the registry (see Icon.svelte). Left out for a chip
		 *  whose words are the whole value ("30 days"), where a glyph
		 *  repeated on every chip would say nothing. */
		icon?: string;
		selected: boolean;
		onclick: () => void;
		/** Greyed and unpressable while the list behind it is reloading. */
		disabled?: boolean;
		/** Stable selector for the e2e browser tier — see test/e2e/README.md. */
		testid?: string;
	}

	let { label, icon, selected, onclick, disabled = false, testid }: Props = $props();
</script>

<button
	type="button"
	{onclick}
	aria-pressed={selected}
	{disabled}
	data-testid={testid}
	class="inline-flex min-h-target items-center gap-1.5 rounded border px-3 text-xs font-semibold transition focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:opacity-50"
	class:border-ink={selected}
	class:bg-ink={selected}
	class:text-surface={selected}
	class:border-border-control={!selected}
	class:text-ink-muted={!selected}
	class:hover:bg-surface-muted={!selected && !disabled}
>
	<!-- Selected is an ink fill with the check glyph, never cobalt
	     (DESIGN.md, "Selected, current and status"): cobalt is the primary
	     action's, and a row of cobalt chips competed with it. -->
	{#if selected}<Icon name="check" size="xs" />{/if}
	{#if icon}<Icon name={icon} size="xs" />{/if}
	<span>{label}</span>
</button>
