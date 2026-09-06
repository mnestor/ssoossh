<script lang="ts">
	/* eslint-disable svelte/no-navigation-without-resolve */
	// The href arrives already final. Most of them came through resolve()
	// in lib/nav.ts, but the admin entries cannot: resolve() is typed
	// against the routes that exist in this tree, and those sections land
	// on separate feature branches. See the header of lib/nav.ts.
	import Icon from './Icon.svelte';
	import { railRowClass } from './railClasses';

	// One destination in the rail. Icon and label always both render; when
	// the rail is collapsed the label goes visually hidden rather than being
	// removed, so the accessible name stays the words either way and a
	// screen reader never meets a row of unlabelled icons. The title
	// attribute covers the sighted mouse user the same tooltip would.
	interface Props {
		/** A final URL, not a route id — see lib/nav.ts. */
		href: string;
		label: string;
		/** Icon name from Icon.svelte's map. */
		icon: string;
		/** True when this item names the page on show. */
		current?: boolean;
		/** True when the rail is showing icons only. */
		collapsed?: boolean;
		/** Called on activation, so the drawer can close behind a tap. */
		onnavigate?: () => void;
	}

	let { href, label, icon, current = false, collapsed = false, onnavigate }: Props = $props();
</script>

<a
	{href}
	title={collapsed ? label : undefined}
	aria-current={current ? 'page' : undefined}
	onclick={onnavigate}
	data-testid="rail-item"
	class={railRowClass(collapsed, current)}
>
	<Icon name={icon} size="sm" />
	<span class:sr-only={collapsed} class="truncate">{label}</span>
</a>
