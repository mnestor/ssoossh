<script lang="ts">
	import type { Snippet } from 'svelte';

	// A plain button with the app's variants. Deliberately not a component
	// library: the UI needs a handful of primitives, and a dependency that
	// ships hundreds would still need wrapping to carry these tokens.
	interface Props {
		variant?: 'primary' | 'danger' | 'ghost';
		type?: 'button' | 'submit';
		disabled?: boolean;
		onclick?: () => void;
		children: Snippet;
	}

	let {
		variant = 'primary',
		type = 'button',
		disabled = false,
		onclick,
		children
	}: Props = $props();

	const variants = {
		primary: 'bg-accent text-white hover:bg-accent-hover',
		danger: 'bg-danger-surface text-danger hover:brightness-95',
		ghost: 'border border-border-subtle text-ink hover:bg-surface-muted'
	};
</script>

<button
	{type}
	{disabled}
	{onclick}
	class="rounded-md px-4 py-2 text-sm font-medium transition disabled:opacity-50 {variants[
		variant
	]}"
>
	{@render children()}
</button>
