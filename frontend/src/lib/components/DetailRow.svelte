<script lang="ts">
	import type { Snippet } from 'svelte';
	import Icon from './Icon.svelte';

	interface Props {
		label: string;
		/** Rendered monospace — for keys, fingerprints, principals, IPs. */
		mono?: boolean;
		/** Optional icon name to display before the value. */
		icon?: string;
		children: Snippet;
	}

	let { label, mono = false, icon, children }: Props = $props();
</script>

<div class="grid grid-cols-1 gap-1 py-2 sm:grid-cols-[8.75rem_1fr] sm:gap-3">
	<dt class="text-[13px] text-ink-muted">{label}</dt>
	<dd class="flex items-center gap-2 text-[13px] break-all" class:font-mono={mono}>
		{#if icon}
			<Icon name={icon} size="sm" class="flex-shrink-0" />
		{/if}
		<span>{@render children()}</span>
	</dd>
</div>
