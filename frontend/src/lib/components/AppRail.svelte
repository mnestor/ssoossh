<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';

	import BrandMark from './BrandMark.svelte';
	import Icon from './Icon.svelte';
	import RailGroup from './RailGroup.svelte';
	import RailItem from './RailItem.svelte';
	import ThemeToggle from './ThemeToggle.svelte';
	import { railRowClass } from './railClasses';
	import { accountNav, adminNav, isAdminRoute, isCurrent, primaryNav } from '$lib/nav';
	import { rail } from '$lib/rail.svelte';
	import { session } from '$lib/session.svelte';

	// The app's one navigation surface. It replaced a header nav of four
	// links and a separate admin tab strip of eight, which between them meant
	// the admin area was reachable only from a line inside the account
	// dropdown — a whole half of the product behind a control that looked
	// like a sign-out button.
	//
	// Everything the old header carried lives here now: brand, destinations,
	// theme, identity, sign out. Above `lg` the rail is the chrome and there
	// is no header at all; below it the rail is an off-canvas drawer and a
	// slim bar carries the trigger.
	interface Props {
		/** Deployment name from /api/branding, shown beside the wordmark. */
		orgName?: string;
		/** True while the sign-out call is in flight. */
		signingOut?: boolean;
		/**
		 * Overrides the stored width preference.
		 *
		 * The drawer passes `false`. Collapsed is a desktop preference kept
		 * in one persisted flag, and the only control that clears it is
		 * hidden below `lg` — so a viewer who collapsed the rail on a laptop
		 * and later opened the app on a phone got a 60px strip of unlabelled
		 * icons with no way in the app to widen it again.
		 */
		collapsed?: boolean;
		onsignout: () => void;
	}

	let { orgName, signingOut = false, collapsed: forceCollapsed, onsignout }: Props = $props();

	const collapsed = $derived(forceCollapsed ?? rail.collapsed);
	const isAuditor = $derived(session.user?.is_auditor ?? false);

	// The identity is the label on its own row rather than a heading above
	// one: on a tool that issues credentials, "as whom" is the question the
	// rail's bottom edge exists to answer.
	const identity = $derived(session.user?.email || session.user?.username || 'Account');

	// The admin group is open unless the viewer has shut it, and an admin
	// route forces it open whatever they last chose: arriving in the admin
	// area with the section list hidden is the one case where their
	// preference cannot be what they meant.
	//
	// It used to default to shut everywhere but /admin, which read as the
	// admin menu having gone missing — the sections had been one click away
	// in a dropdown before, and were now two behind a collapsed group with
	// nothing to say what was inside it.
	const adminOpen = $derived(rail.adminOpen || isAdminRoute(page.url.pathname));

	// Named for what pressing it does, not for the state it is in: a control
	// labelled "Collapse rail" on an already-collapsed rail tells a screen
	// reader user the opposite of what will happen.
	const collapseLabel = $derived(collapsed ? 'Expand rail' : 'Collapse rail');

	/** closeDrawer runs on every activation. Above `lg` the drawer is not
	 * showing and this is a no-op; below it, leaving the drawer open over
	 * the page the viewer just asked for is the bug. */
	function navigated() {
		rail.closeDrawer();
	}
</script>

<nav
	aria-label="Main"
	data-testid="app-rail"
	class="flex h-full flex-col border-r border-border-subtle bg-surface transition-[width]"
	class:w-[244px]={!collapsed}
	class:w-[60px]={collapsed}
>
	<!-- Brand. Stays a link home at both widths: the mark is the only thing
	     in the rail that is not a destination list, and dropping it on
	     collapse would leave the column starting mid-list. -->
	<a
		href={resolve('/')}
		onclick={navigated}
		class="flex h-14 shrink-0 items-center gap-2 border-b border-border-subtle font-semibold"
		class:justify-center={collapsed}
		class:px-3.5={!collapsed}
	>
		<BrandMark size={22} />
		{#if !collapsed}
			<span class="flex min-w-0 items-baseline gap-2">
				<span>ssoossh</span>
				<!-- Capped rather than dropped: a deployment that set a name
				     wants it on every screen, but an unbounded one would push
				     the wordmark out of its own row. -->
				{#if orgName}
					<span
						class="max-w-[6.5rem] truncate border-l border-border-subtle pl-2 text-xs font-normal text-ink-muted"
					>
						{orgName}
					</span>
				{/if}
			</span>
		{/if}
	</a>

	<div class="flex-1 overflow-y-auto p-2">
		{#each primaryNav() as item (item.href)}
			<RailItem
				href={item.href}
				label={item.label}
				icon={item.icon}
				current={isCurrent(item.href, page.url.pathname)}
				{collapsed}
				onnavigate={navigated}
			/>
		{/each}

		{#if isAuditor}
			<div class="my-2 border-t border-border-subtle" class:mx-1.5={!collapsed}></div>

			<RailGroup
				label="Admin"
				icon="shield-check"
				open={adminOpen}
				{collapsed}
				testid="rail-admin-group"
				ontoggle={() => rail.toggleAdminOpen()}
			>
				{#each adminNav as item (item.href)}
					<RailItem
						href={item.href}
						label={item.label}
						icon={item.icon}
						current={isCurrent(item.href, page.url.pathname)}
						{collapsed}
						onnavigate={navigated}
					/>
				{/each}
			</RailGroup>
		{/if}
	</div>

	<!-- Identity and the controls that act on it, pinned to the bottom edge
	     so they stay in reach of a thumb in the drawer and out of the way of
	     the destination list on a desktop. -->
	<div class="shrink-0 border-t border-border-subtle p-2">
		<RailItem
			href={resolve('/account')}
			label={identity}
			icon="user"
			current={isCurrent(resolve('/account'), page.url.pathname)}
			{collapsed}
			onnavigate={navigated}
		/>

		{#each accountNav() as item (item.href)}
			<RailItem
				href={item.href}
				label={item.label}
				icon={item.icon}
				current={isCurrent(item.href, page.url.pathname)}
				{collapsed}
				onnavigate={navigated}
			/>
		{/each}

		<ThemeToggle variant="rail" {collapsed} />

		<!-- max-lg:hidden rather than "hidden lg:flex": the row already
		     carries `flex` from railRowClass, and two unmodified utilities
		     setting `display` resolve by stylesheet order rather than by the
		     order they are written. A variant beats the plain utility, so
		     hiding it at narrow widths is the form that is actually
		     guaranteed. Narrow widths have no rail to collapse — the drawer
		     is either open or it is not. -->
		<button
			type="button"
			onclick={() => rail.toggleCollapsed()}
			aria-label={collapseLabel}
			title={collapseLabel}
			data-testid="rail-collapse"
			class="{railRowClass(collapsed)} max-lg:hidden"
		>
			<Icon name="panel-left" size="sm" />
			<span class:sr-only={collapsed} class="truncate">{collapseLabel}</span>
		</button>

		<button
			type="button"
			disabled={signingOut}
			onclick={onsignout}
			title={collapsed ? 'Sign out' : undefined}
			class="{railRowClass(collapsed)} disabled:opacity-50"
		>
			<Icon name="log-out" size="sm" />
			<span class:sr-only={collapsed} class="truncate">
				{signingOut ? 'Signing out…' : 'Sign out'}
			</span>
		</button>
	</div>
</nav>
