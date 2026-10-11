<script lang="ts">
	import { detail, type DetailLevel } from '#lib/detail.svelte.js';
	import Icon from './Icon.svelte';

	// The two detail levels as one segmented control. Bound to the web UI
	// level by default, for the heading row of each page the preference
	// covers and the "Display" section on /preferences, so those never
	// disagree. Given `value` and `onselect` it is a plain control instead,
	// which is how /preferences sets the separate email level.
	//
	// A group of toggles rather than tabs or a select: two mutually
	// exclusive views, each a press away (DESIGN.md, "Accessibility").
	// Selected is an ink fill with the check glyph and aria-pressed, never
	// cobalt, which stays the primary action's.
	interface Props {
		/** Stable selector for the e2e browser tier — see test/e2e/README.md. */
		testid?: string;
		/** The level shown as selected. Omitted: the web UI level. */
		value?: DetailLevel;
		/** Called with the chosen level. Omitted: saves the web UI level. */
		onselect?: (level: DetailLevel) => void;
		/** The group's accessible name. */
		label?: string;
	}

	let { testid = 'detail-toggle', value, onselect, label = 'Detail level' }: Props = $props();

	const levels: { value: DetailLevel; label: string }[] = [
		{ value: 'simplified', label: 'Simplified' },
		{ value: 'advanced', label: 'Advanced' }
	];

	// Bound to the store unless the caller supplies both halves.
	const controlled = $derived(value !== undefined && onselect !== undefined);
	const current = $derived(controlled ? value : detail.level);

	function choose(level: DetailLevel) {
		if (controlled) {
			onselect?.(level);
			return;
		}
		void detail.set(level);
	}
</script>

<div class="inline-flex flex-wrap items-center gap-x-3 gap-y-1">
	<div
		class="inline-flex overflow-hidden rounded-md border border-border-control"
		role="group"
		aria-label={label}
		data-testid={testid}
	>
		{#each levels as level (level.value)}
			{@const pressed = current === level.value}
			<button
				type="button"
				aria-pressed={pressed}
				data-testid="{testid}-{level.value}"
				onclick={() => choose(level.value)}
				class="inline-flex min-h-target items-center gap-1.5 px-3 text-xs font-medium transition focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent {pressed
					? 'bg-ink text-surface'
					: 'text-ink-muted hover:bg-surface-muted'}"
			>
				{#if pressed}<Icon name="check" size="xs" />{/if}
				{level.label}
			</button>
		{/each}
	</div>
	{#if !controlled && detail.saveError}
		<!-- The selection has already snapped back; this says why. -->
		<span role="alert" class="text-xs text-danger" data-testid="{testid}-error">
			Not saved. Try again.
		</span>
	{/if}
</div>
