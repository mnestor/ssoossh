<script lang="ts">
	import type { Snippet } from 'svelte';

	interface Props {
		label: string;
		/** Rendered monospace — for keys, fingerprints, principals, IPs. */
		mono?: boolean;
		children: Snippet;
	}

	let { label, mono = false, children }: Props = $props();
</script>

<div class="grid grid-cols-1 gap-1 py-2 sm:grid-cols-[10rem_1fr] sm:gap-4">
	<dt class="text-sm text-ink-muted">{label}</dt>
	<dd class="text-sm break-all" class:font-mono={mono}>{@render children()}</dd>
</div>
