<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';

	import BrandMark from './BrandMark.svelte';
	import Icon from './Icon.svelte';
	import RailGroup from './RailGroup.svelte';
	import RailItem from './RailItem.svelte';
	import RailUserMenu from './RailUserMenu.svelte';
	import { adminNav, isAdminRoute, isCurrent, primaryNav } from '$lib/nav';
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
		onsignout: () => void;
	}

	let { orgName, signingOut = false, onsignout }: Props = $props();

	const collapsed = $derived(rail.collapsed);
	const isAuditor = $derived(session.user?.is_auditor ?? false);

	// The identity is the label on its own row rather than a heading above
	// one: on a tool that issues credentials, "as whom" is the question the
	// rail's bottom edge exists to answer.
	const identity = $derived(session.user?.email || session.user?.username || 'Account');

	// The admin group opens by itself on an admin route, and stays wherever
	// the viewer last put it once they have said. Null means "nobody has
	// said", which is what makes arrival open it without overriding a
	// deliberate close on the next navigation.
	let adminOverride = $state<boolean | null>(null);
	const adminOpen = $derived(adminOverride ?? isAdminRoute(page.url.pathname));

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
	<!-- Brand, and the control for the rail's own width beside it. The width
	     control belongs at the head of the column it resizes: it is the one
	     thing here that acts on the rail rather than on the app, and down in
	     the footer it sat among controls that act on the session.
	  -->
	<div class="flex h-14 shrink-0 items-center border-b border-border-subtle">
		{#if collapsed}
			<!-- At 60px there is one slot and it has to do both jobs. The mark
			     stays, so the column still starts with the product rather than
			     mid-list, and pressing it is what brings the rail back. Home
			     gives up its link here: Dashboard is the row directly below,
			     and stranding someone at 60px wide costs more.

			     Not hidden below `lg`, unlike the expanded form: a drawer
			     opened while the stored preference is collapsed has no other
			     way out of icon width. -->
			<button
				type="button"
				onclick={() => rail.toggleCollapsed()}
				aria-label={collapseLabel}
				title={collapseLabel}
				data-testid="rail-collapse"
				class="flex h-full w-full items-center justify-center transition hover:bg-surface-muted"
			>
				<BrandMark size={22} />
			</button>
		{:else}
			<a
				href={resolve('/')}
				onclick={navigated}
				class="flex h-full min-w-0 flex-1 items-center gap-2 pl-3.5 font-semibold"
			>
				<BrandMark size={22} />
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
			</a>

			<!-- max-lg:hidden rather than "hidden lg:flex": the row already
			     carries `flex`, and two unmodified utilities setting `display`
			     resolve by stylesheet order rather than by the order they are
			     written. A variant beats the plain utility, so hiding it at
			     narrow widths is the form that is actually guaranteed. Narrow
			     widths have no rail to collapse — the drawer is either open or
			     it is not. -->
			<button
				type="button"
				onclick={() => rail.toggleCollapsed()}
				aria-label={collapseLabel}
				title={collapseLabel}
				data-testid="rail-collapse"
				class="mr-2 flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-ink-muted transition hover:bg-surface-muted hover:text-ink max-lg:hidden"
			>
				<Icon name="panel-left" size="sm" />
			</button>
		{/if}
	</div>

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
				ontoggle={() => (adminOverride = !adminOpen)}
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

	<!-- The identity, pinned to the bottom edge so it stays in reach of a
	     thumb in the drawer and out of the way of the destination list on a
	     desktop. One row now: account, preferences, theme and sign out are
	     all things done to the session, and they belong behind the row that
	     names it rather than stacked in the column beside destinations. -->
	<div class="shrink-0 border-t border-border-subtle p-2">
		<RailUserMenu {identity} {collapsed} {signingOut} onnavigate={navigated} {onsignout} />
	</div>
</nav>
