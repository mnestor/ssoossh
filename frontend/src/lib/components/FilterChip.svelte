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
	// The label is shown from `sm` up and hidden below it, in the component
	// rather than per caller: "which of these collapse" is not a decision a
	// filter row should get to make differently from the next one.
	//
	// A button rather than a link: filtering is a change of view, not a
	// change of address. The list is paged client-side over what has been
	// loaded, so there is no URL that would restore it anyway.
	interface Props {
		label: string;
		/** Icon name from the registry — see Icon.svelte. */
		icon: string;
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
	class="inline-flex items-center gap-1.5 rounded-full border px-3.5 py-1.5 text-xs font-semibold transition focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:opacity-50"
	class:border-accent={selected}
	class:bg-accent={selected}
	class:text-accent-ink={selected}
	class:border-border-subtle={!selected}
	class:text-ink-muted={!selected}
	class:hover:bg-surface-muted={!selected && !disabled}
>
	<Icon name={icon} size="xs" />
	<!-- Named on a desktop, an icon on a phone. Eight chips with their words
	     wrap a filter row onto three lines on a narrow screen, and the icons
	     are the same glyphs the rows they filter carry. `sr-only` rather
	     than `hidden`, so the button never announces as a bare icon. -->
	<span class="sr-only sm:not-sr-only">{label}</span>
</button>
