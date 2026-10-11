<script lang="ts">
	import type { Snippet } from 'svelte';
	import Button from './Button.svelte';
	import Icon from './Icon.svelte';

	interface Props {
		variant?: 'info' | 'warning' | 'error';
		title?: string;
		/** Optional ARIA role override — defaults to 'status' for info/warning, 'alert' for error. */
		role?: 'status' | 'alert';
		/** Stable selector for the e2e browser tier — see test/e2e/README.md. */
		testid?: string;
		/**
		 * True when the alert sits inside a panel. An info alert is a
		 * `surface` panel on the ground and a `surface-muted` tint inside a
		 * panel, where a second white box would vanish (DESIGN.md, "Planes").
		 */
		inset?: boolean;
		/** A failed load's way out: adds a "Try again" button that calls this. */
		onretry?: () => void;
		children?: Snippet;
	}

	let { variant = 'info', title, role, testid, inset = false, onretry, children }: Props = $props();

	// Errors use assertive role='alert' so screen readers announce immediately;
	// info and warning use role='status' (polite announcement).
	const computedRole = $derived(role || (variant === 'error' ? 'alert' : 'status'));

	// No border on any of them: the fill is the plane, and an outline on top
	// of it said the same thing twice.
	const variants = $derived({
		info: inset ? 'bg-surface-muted text-ink' : 'bg-surface text-ink',
		warning: 'bg-trimmed-surface text-trimmed',
		error: 'bg-danger-surface text-danger'
	});

	// Severity walks the road-sign ladder: circle, triangle, octagon. Info
	// and error drew the same circle until now and let colour carry the
	// whole difference, which left anyone who cannot separate the two hues
	// looking at one undifferentiated banner.
	const iconMap = {
		info: 'info-circle',
		warning: 'alert-triangle',
		error: 'alert-octagon'
	};
</script>

<div
	class="rounded-lg px-4 py-3 text-sm {variants[variant]} flex items-start gap-3"
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
		<!-- One button, one wording, on every failed load, so "Try again" is
		     always where a reader looks for it (DESIGN.md, "States"). -->
		{#if onretry}
			<div class="mt-3">
				<Button variant="ghost" onclick={onretry} testid={testid ? `${testid}-retry` : undefined}>
					Try again
				</Button>
			</div>
		{/if}
	</div>
</div>
