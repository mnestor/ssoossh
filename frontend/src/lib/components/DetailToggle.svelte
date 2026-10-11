<script lang="ts">
	import { detail, type DetailLevel } from '#lib/detail.svelte.js';
	import Icon from './Icon.svelte';

	// The two detail levels as one segmented control, for the heading row of
	// each page the preference covers. It writes the same value as the
	// "Display" section on /preferences, so the two never disagree.
	//
	// A group of toggles rather than tabs or a select: two mutually
	// exclusive views, each a press away (DESIGN.md, "Accessibility").
	// Selected is an ink fill with the check glyph and aria-pressed, never
	// cobalt, which stays the primary action's.
	interface Props {
		/** Stable selector for the e2e browser tier — see test/e2e/README.md. */
		testid?: string;
	}

	let { testid = 'detail-toggle' }: Props = $props();

	const levels: { value: DetailLevel; label: string }[] = [
		{ value: 'simplified', label: 'Simplified' },
		{ value: 'advanced', label: 'Advanced' }
	];
</script>

<div
	class="inline-flex overflow-hidden rounded-md border border-border-control"
	role="group"
	aria-label="Detail level"
	data-testid={testid}
>
	{#each levels as level (level.value)}
		{@const pressed = detail.level === level.value}
		<button
			type="button"
			aria-pressed={pressed}
			data-testid="{testid}-{level.value}"
			onclick={() => detail.set(level.value)}
			class="inline-flex min-h-target items-center gap-1.5 px-3 text-xs font-medium transition focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent {pressed
				? 'bg-ink text-surface'
				: 'text-ink-muted hover:bg-surface-muted'}"
		>
			{#if pressed}<Icon name="check" size="xs" />{/if}
			{level.label}
		</button>
	{/each}
</div>
