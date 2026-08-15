<script lang="ts">
	import type { Snippet } from 'svelte';

	interface Props {
		title?: string;
		description?: string;
		children: Snippet;
		footer?: Snippet;
	}

	let { title, description, children, footer }: Props = $props();
</script>

<section class="rounded-lg border border-border-subtle bg-surface shadow-sm">
	{#if title}
		<header class="border-b border-border-subtle px-5 py-4">
			<h2 class="text-base font-semibold">{title}</h2>
			{#if description}
				<p class="mt-1 text-sm text-ink-muted">{description}</p>
			{/if}
		</header>
	{/if}

	<div class="px-5 py-4">
		{@render children()}
	</div>

	{#if footer}
		<footer class="rounded-b-lg border-t border-border-subtle bg-surface-muted px-5 py-4">
			{@render footer()}
		</footer>
	{/if}
</section>
