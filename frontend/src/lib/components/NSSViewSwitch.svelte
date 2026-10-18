<script lang="ts">
	import { resolve } from '$app/paths';

	import Icon from './Icon.svelte';

	// The NSS directory's two lists, stored accounts and stored groups, as one
	// segmented control in the heading. They are mutually exclusive views of
	// one section, so the current one is filled with the check glyph
	// (DESIGN.md, "Selected, current and status"); each is a link, because
	// each is its own address with its own filters.
	interface Props {
		current: 'accounts' | 'groups';
	}

	let { current }: Props = $props();

	const views = [
		{ id: 'accounts', label: 'Accounts', href: resolve('/admin/nss/accounts') },
		{ id: 'groups', label: 'Groups', href: resolve('/admin/nss/groups') }
	] as const;
</script>

<nav
	aria-label="NSS directory lists"
	class="inline-flex overflow-hidden rounded-md border border-border-control"
	data-testid="nss-view-switch"
>
	{#each views as view (view.id)}
		<a
			href={view.href}
			aria-current={current === view.id ? 'page' : undefined}
			data-testid="nss-view-{view.id}"
			class="inline-flex min-h-target items-center gap-1.5 px-3 text-sm transition focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent {current ===
			view.id
				? 'bg-ink text-surface'
				: 'text-ink-muted hover:bg-surface-muted'}"
		>
			{#if current === view.id}<Icon name="check" size="xs" />{/if}
			{view.label}
		</a>
	{/each}
</nav>
