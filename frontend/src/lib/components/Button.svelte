<script lang="ts">
	import type { Snippet } from 'svelte';

	// A plain button with the app's variants. Deliberately not a component
	// library: the UI needs a handful of primitives, and a dependency that
	// ships hundreds would still need wrapping to carry these tokens.
	interface Props {
		variant?: 'primary' | 'danger' | 'ghost';
		type?: 'button' | 'submit';
		disabled?: boolean;
		/** Stretches the button to its container's width, for a page's single primary action. */
		full?: boolean;
		/** Indicates the button is processing an action. Disables the button and sets aria-busy. */
		busy?: boolean;
		onclick?: () => void;
		/** Stable selector for the e2e browser tier — see test/e2e/README.md. */
		testid?: string;
		children: Snippet;
	}

	let {
		variant = 'primary',
		type = 'button',
		disabled = false,
		full = false,
		busy = false,
		onclick,
		testid,
		children
	}: Props = $props();

	// A press has to answer inside 100ms or it does not read as a press at
	// all, which is why the whole transition runs at 100 rather than at the
	// 150 Tailwind gives a bare `transition`. The scale is 2%: enough to
	// register under a finger, small enough that a row of buttons does not
	// appear to wobble.
	const variants = {
		primary: 'bg-accent text-accent-ink hover:bg-accent-hover',
		danger: 'bg-danger-surface text-danger hover:brightness-95',
		ghost: 'border border-border-control text-ink hover:bg-surface-muted'
	};
</script>

<button
	{type}
	disabled={disabled || busy}
	{onclick}
	aria-busy={busy}
	data-testid={testid}
	class="inline-flex items-center justify-center gap-2 rounded-md px-4 py-2 text-sm font-medium transition duration-100 active:scale-98 disabled:opacity-50 {full
		? 'w-full'
		: ''} {variants[variant]}"
>
	{@render children()}
</button>
