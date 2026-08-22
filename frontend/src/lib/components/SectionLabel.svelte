<script lang="ts">
	import type { Snippet } from 'svelte';

	// The small uppercase label that opens a group of fields. Quieter than the
	// eyebrow on PageHeading — this one names a section within a card, so it
	// stays muted rather than taking the accent.
	interface Props {
		children: Snippet;
	}

	let { children }: Props = $props();
</script>

<div class="mb-1.5 text-[11px] font-semibold tracking-[0.06em] text-ink-muted uppercase">
	{@render children()}
</div>
