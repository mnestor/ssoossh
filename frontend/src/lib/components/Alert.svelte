<script lang="ts">
	import type { Snippet } from 'svelte';

	interface Props {
		variant?: 'info' | 'warning' | 'error';
		title?: string;
		/** Stable selector for the e2e browser tier — see test/e2e/README.md. */
		testid?: string;
		children?: Snippet;
	}

	let { variant = 'info', title, testid, children }: Props = $props();

	const variants = {
		info: 'bg-surface-muted text-ink border-border-subtle',
		warning: 'bg-trimmed-surface text-trimmed border-trimmed/30',
		error: 'bg-danger-surface text-danger border-danger/30'
	};
</script>

<div
	class="rounded-md border px-4 py-3 text-sm {variants[variant]}"
	role="status"
	data-testid={testid}
>
	{#if title}
		<p class="font-semibold">{title}</p>
	{/if}
	{#if children}
		<div class:mt-1={!!title}>{@render children()}</div>
	{/if}
</div>
