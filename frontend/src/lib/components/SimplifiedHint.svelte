<script lang="ts">
	import { detail } from '#lib/detail.svelte.js';

	// The line closing every simplified page, so a reader who chose the
	// short version, or inherited it, knows what is left out and is one
	// press from it. Renders nothing at the advanced level.
	interface Props {
		/** Stable selector for the e2e browser tier — see test/e2e/README.md. */
		testid?: string;
	}

	let { testid = 'detail-hint' }: Props = $props();
</script>

{#if detail.simplified}
	<p class="text-dense text-ink-muted" data-testid={testid}>
		Showing the essentials.
		<button
			type="button"
			onclick={() => detail.set('advanced')}
			class="inline-flex min-h-target items-center text-accent hover:underline"
			data-testid="{testid}-advanced">Switch to Advanced</button
		>
		for ids, fingerprints and policy detail.
	</p>
{/if}
