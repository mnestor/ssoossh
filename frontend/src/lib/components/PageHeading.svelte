<script lang="ts">
	import type { Snippet } from 'svelte';

	// Every screen opens the same way: a small accent eyebrow naming the
	// area, then the page's own h1. The eyebrow is what makes a page
	// identifiable at a glance without reading the title, so it is required
	// rather than optional.
	interface Props {
		/** Short area name — "Activity", "History", "Certificate request". */
		eyebrow: string;
		title: string;
		/** Optional trailing control, right-aligned against the title. */
		action?: Snippet;
		/** Stable selector for the e2e browser tier — see test/e2e/README.md. */
		testid?: string;
	}

	let { eyebrow, title, action, testid }: Props = $props();
</script>

<div data-testid={testid} class="flex items-center justify-between gap-4">
	<div>
		<div class="mb-1.5 text-xs font-semibold tracking-[0.06em] text-accent uppercase">
			{eyebrow}
		</div>
		<h1 class="text-[26px] leading-tight font-bold tracking-[-0.01em]">{title}</h1>
	</div>
	{#if action}
		{@render action()}
	{/if}
</div>
