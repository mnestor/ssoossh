<script lang="ts">
	import type { Snippet } from 'svelte';

	// A single monospace value shown as a bordered chip — one principal, one
	// IP. Chips rather than a comma-separated string so that where a list is
	// really a set, the boundaries between members are unambiguous.
	interface Props {
		children: Snippet;
	}

	let { children }: Props = $props();
</script>

<code
	class="rounded border border-border-subtle px-1.5 py-0.5 font-mono text-xs break-all text-ink"
>
	{@render children()}
</code>
