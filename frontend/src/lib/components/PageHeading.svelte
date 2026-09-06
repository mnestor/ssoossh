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
		/**
		 * An optional line under the title, for the pages that were opening
		 * with a bare h1 and a paragraph instead of this component. A snippet
		 * rather than a string because two of them are not prose: a user
		 * detail page names the account in mono beside its address.
		 */
		sub?: Snippet;
		/** Optional trailing control, right-aligned against the title. */
		action?: Snippet;
		/** Stable selector for the e2e browser tier — see test/e2e/README.md. */
		testid?: string;
	}

	let { eyebrow, title, sub, action, testid }: Props = $props();
</script>

<div data-testid={testid} class="flex items-center justify-between gap-4">
	<div class="min-w-0">
		<div class="mb-1.5 text-xs font-semibold tracking-[0.06em] text-accent uppercase">
			{eyebrow}
		</div>
		<h1 class="text-[26px] leading-tight font-bold tracking-[-0.01em]">{title}</h1>
		{#if sub}
			<p class="mt-1.5 text-sm text-ink-muted">{@render sub()}</p>
		{/if}
	</div>
	{#if action}
		<div class="shrink-0">{@render action()}</div>
	{/if}
</div>
