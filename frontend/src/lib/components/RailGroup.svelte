<script lang="ts">
	import Icon from './Icon.svelte';
	import type { Snippet } from 'svelte';

	// A named, collapsible band of rail items. The head is a button rather
	// than a link: the group has no page of its own, and giving it one would
	// mean inventing an admin landing screen whose only content is the list
	// already on show.
	//
	// The caret is the only thing that rotates. A group that animated its
	// height would fight the rail's own width transition on a collapse, and
	// the two together read as the whole panel wobbling.
	interface Props {
		label: string;
		/** Icon name from Icon.svelte's map. */
		icon: string;
		open: boolean;
		/** True when the rail is showing icons only. */
		collapsed?: boolean;
		/** Stable selector for the e2e browser tier — see test/e2e/README.md. */
		testid?: string;
		ontoggle: () => void;
		children: Snippet;
	}

	let { label, icon, open, collapsed = false, testid, ontoggle, children }: Props = $props();
</script>

<div>
	<button
		type="button"
		onclick={ontoggle}
		aria-expanded={open}
		title={collapsed ? label : undefined}
		data-testid={testid}
		class="flex h-9 w-full items-center gap-2.5 rounded-md font-mono text-meta font-medium tracking-widest text-ink-muted uppercase transition hover:bg-surface-muted hover:text-ink"
		class:justify-center={collapsed}
		class:px-2.5={!collapsed}
	>
		<Icon name={icon} size="sm" />
		<span class:sr-only={collapsed} class="truncate">{label}</span>
		{#if !collapsed}
			<span class="ml-auto transition-transform" class:-rotate-90={!open}>
				<Icon name="chevron-down" size="xs" />
			</span>
		{/if}
	</button>

	{#if open}
		<!-- The rule down the left is what tells a reader the indented items
		     belong to the head above them rather than to the rail at large.
		     It goes with the indent when the rail is collapsed, where there
		     is no room for either and the icons stand on their own. -->
		<div
			class:ml-4={!collapsed}
			class:border-l={!collapsed}
			class:border-border-subtle={!collapsed}
		>
			<div class:pl-2={!collapsed}>
				{@render children()}
			</div>
		</div>
	{/if}
</div>
