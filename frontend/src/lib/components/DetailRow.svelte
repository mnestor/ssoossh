<script lang="ts">
	import type { Snippet } from 'svelte';
	import { detail, inDetailScope } from '#lib/detail.svelte.js';
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

	// On a page the detail level covers, a simplified reader sees fewer facts,
	// each one size larger and with more room between them (DESIGN.md,
	// "Simplified type"). Off those pages the row is always dense.
	const covered = inDetailScope();
	const large = $derived(covered && detail.simplified);
</script>

<div class="grid grid-cols-1 gap-1 sm:grid-cols-[8.75rem_1fr] sm:gap-3 {large ? 'py-3' : 'py-2'}">
	<dt class="text-ink-muted {large ? 'text-sm' : 'text-dense'}">{label}</dt>
	<dd
		class="flex items-center gap-2 break-all {large ? 'text-base' : 'text-dense'}"
		class:font-mono={mono}
	>
		{#if icon}
			<Icon name={icon} size="sm" class="flex-shrink-0" />
		{/if}
		<span>{@render children()}</span>
	</dd>
</div>
