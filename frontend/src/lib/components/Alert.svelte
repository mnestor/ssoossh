<script lang="ts">
	import type { Snippet } from 'svelte';
	import Icon from './Icon.svelte';

	interface Props {
		variant?: 'info' | 'warning' | 'error';
		title?: string;
		/** Optional ARIA role override — defaults to 'status' for info/warning, 'alert' for error. */
		role?: 'status' | 'alert';
		/** Stable selector for the e2e browser tier — see test/e2e/README.md. */
		testid?: string;
		children?: Snippet;
	}

	let { variant = 'info', title, role, testid, children }: Props = $props();

	// Errors use assertive role='alert' so screen readers announce immediately;
	// info and warning use role='status' (polite announcement).
	const computedRole = $derived(role || (variant === 'error' ? 'alert' : 'status'));

	const variants = {
		info: 'bg-surface-muted text-ink border-border-subtle',
		warning: 'bg-trimmed-surface text-trimmed border-trimmed/30',
		error: 'bg-danger-surface text-danger border-danger/30'
	};

	const iconMap = {
		info: 'alert-circle',
		warning: 'alert-triangle',
		error: 'alert-circle'
	};
</script>

<div
	class="rounded-md border px-4 py-3 text-sm {variants[variant]} flex items-start gap-3"
	role={computedRole}
	data-testid={testid}
>
	<Icon name={iconMap[variant]} size="sm" class="mt-0.5 flex-shrink-0" />
	<div class="flex-1">
		{#if title}
			<p class="font-semibold">{title}</p>
		{/if}
		{#if children}
			<div class:mt-1={!!title}>{@render children()}</div>
		{/if}
	</div>
</div>
