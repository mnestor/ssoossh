<script lang="ts">
	import type { Snippet } from 'svelte';

	// The small uppercase label that opens a group of fields. It is the only
	// small uppercase label left in the app now that PageHeading's accent
	// eyebrow is gone, and it stays muted: it names a section within a card,
	// not the page.
	interface Props {
		children: Snippet;
	}

	let { children }: Props = $props();
</script>

<div class="mb-1.5 text-[11px] font-semibold tracking-[0.06em] text-ink-muted uppercase">
	{@render children()}
</div>
