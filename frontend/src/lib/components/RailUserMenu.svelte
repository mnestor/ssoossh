<script lang="ts">
	/* eslint-disable svelte/no-navigation-without-resolve */
	// Both hrefs here are already final: the account link is resolve()d just
	// below, and the rest come through resolve() in lib/nav.ts. The rule
	// fires where an href reaches the DOM rather than where it is built, so
	// the exemption has to live in this file. Same reason as RailItem.svelte.
	import { resolve } from '$app/paths';
	import { page } from '$app/state';

	import Icon from './Icon.svelte';
	import ThemeToggle from './ThemeToggle.svelte';
	import { railRowClass } from './railClasses';
	import { accountNav, isCurrent } from '$lib/nav';
	import { session } from '$lib/session.svelte';

	// The rail's bottom edge, as one row that opens upward.
	//
	// It used to be four stacked rows — identity, preferences, theme, sign
	// out — which made the footer as tall as the admin group and left the
	// session-ending button permanently one row away from the destination
	// list. Everything that acts on "who am I signed in as" now sits behind
	// the row that names that identity, so the rail's visible column is
	// destinations and nothing else.
	//
	// A disclosure, not a `role="menu"`: the contents are ordinary links and
	// buttons, and the tab order a popover already gives them is what a
	// keyboard user expects from links. Declaring the menu role would promise
	// arrow-key roving that these rows do not implement.
	interface Props {
		/** What the session is acting as — an address, else a username. */
		identity: string;
		/** True when the rail is showing icons only. */
		collapsed?: boolean;
		/** True while the sign-out call is in flight. */
		signingOut?: boolean;
		/** Called on anything that navigates, so the drawer closes behind it. */
		onnavigate?: () => void;
		onsignout: () => void;
	}

	let { identity, collapsed = false, signingOut = false, onnavigate, onsignout }: Props = $props();

	let open = $state(false);
	let root = $state<HTMLDivElement>();
	let trigger = $state<HTMLButtonElement>();

	const accountHref = $derived(resolve('/account'));

	// The trigger carries the selected state for every page behind it.
	// Without this, standing on /preferences would leave the rail with
	// nothing marked, because the row that owns that page is inside a shut
	// popover.
	const ownsCurrentPage = $derived(
		isCurrent(accountHref, page.url.pathname) ||
			accountNav().some((item) => isCurrent(item.href, page.url.pathname))
	);

	// The display name, when the identity carries one distinct from the
	// address. The trigger truncates to the rail's width, so the popover's
	// head is the one place the whole identity is legible.
	const fullName = $derived(session.user?.name ?? '');

	/** close returns focus to the trigger: a popover dismissed from the
	 * keyboard must not drop the caret back at the top of the document. */
	function close(refocus = true) {
		open = false;
		if (refocus) {
			trigger?.focus();
		}
	}

	/** navigated shuts the popover and lets the rail close the drawer under
	 * it, so one tap does not leave two layers over the page just asked for. */
	function navigated() {
		open = false;
		onnavigate?.();
	}

	// Escape is handled on this wrapper rather than on the document because
	// the drawer listens for Escape too. Stopping it here is what makes the
	// key close the popover first and the drawer only once the popover has
	// already gone.
	function onkeydown(event: KeyboardEvent) {
		if (open && event.key === 'Escape') {
			event.stopPropagation();
			close();
		}
	}

	// A popover that only closes by re-pressing its own trigger is a trap
	// once it covers that trigger. pointerdown rather than click, so the
	// popover is gone before the press lands on whatever is underneath.
	$effect(() => {
		if (!open) {
			return;
		}
		function onPointerDown(event: PointerEvent) {
			if (root && !root.contains(event.target as Node)) {
				close(false);
			}
		}
		document.addEventListener('pointerdown', onPointerDown);
		return () => document.removeEventListener('pointerdown', onPointerDown);
	});
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div bind:this={root} class="relative" {onkeydown}>
	{#if open}
		<!-- Anchored over the rail where there is width for labels, and flown
		     out to the right where there is not: a 60px-wide popover would
		     truncate every row it exists to spell out. -->
		<div
			data-testid="rail-user-menu"
			class="absolute z-50 rounded-lg border border-border-subtle bg-surface p-1.5 shadow-lg"
			class:right-0={!collapsed}
			class:left-0={!collapsed}
			class:bottom-full={!collapsed}
			class:mb-1.5={!collapsed}
			class:bottom-0={collapsed}
			class:left-full={collapsed}
			class:ml-1.5={collapsed}
			class:w-60={collapsed}
		>
			<div class="border-b border-border-subtle px-2.5 pt-1 pb-2">
				{#if fullName}
					<p class="truncate text-[13px] font-semibold">{fullName}</p>
				{/if}
				<p class="truncate text-xs text-ink-muted">{identity}</p>
			</div>

			<div class="pt-1.5">
				<a
					href={accountHref}
					aria-current={isCurrent(accountHref, page.url.pathname) ? 'page' : undefined}
					onclick={navigated}
					data-testid="rail-user-menu-item"
					class={railRowClass(false, isCurrent(accountHref, page.url.pathname))}
				>
					<Icon name="user" size="sm" />
					<span class="truncate">Account</span>
				</a>

				{#each accountNav() as item (item.href)}
					<a
						href={item.href}
						aria-current={isCurrent(item.href, page.url.pathname) ? 'page' : undefined}
						onclick={navigated}
						data-testid="rail-user-menu-item"
						class={railRowClass(false, isCurrent(item.href, page.url.pathname))}
					>
						<Icon name={item.icon} size="sm" />
						<span class="truncate">{item.label}</span>
					</a>
				{/each}

				<!-- Never collapsed, whatever the rail is doing: inside the
				     popover there is always room for the label. -->
				<ThemeToggle variant="rail" />

				<div class="my-1.5 border-t border-border-subtle"></div>

				<button
					type="button"
					disabled={signingOut}
					onclick={onsignout}
					data-testid="rail-sign-out"
					class="{railRowClass(false)} disabled:opacity-50"
				>
					<Icon name="log-out" size="sm" />
					<span class="truncate">{signingOut ? 'Signing out…' : 'Sign out'}</span>
				</button>
			</div>
		</div>
	{/if}

	<!-- The chevron points the way the popover will go, and turns over once
	     it has gone there. -->
	<button
		bind:this={trigger}
		type="button"
		onclick={() => (open = !open)}
		aria-expanded={open}
		aria-haspopup="true"
		title={collapsed ? identity : undefined}
		data-testid="rail-user-trigger"
		class={railRowClass(collapsed, ownsCurrentPage)}
	>
		<Icon name="user" size="sm" />
		<span class:sr-only={collapsed} class="truncate">{identity}</span>
		{#if !collapsed}
			<Icon name={open ? 'chevron-down' : 'chevron-up'} size="xs" class="ml-auto" />
		{/if}
	</button>
</div>
