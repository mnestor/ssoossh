<script lang="ts">
	/* eslint-disable svelte/no-navigation-without-resolve */
	// The nav below uses plain hrefs rather than resolve(). This nav names all
	// admin sections, but each section's page lands on its own feature
	// branch, and resolve() is typed against the routes that exist in THIS
	// tree. Using it would force a placeholder page for every absent route,
	// and a placeholder sharing a path with another branch's real page is a
	// merge waiting to resolve the wrong way. Same file-level form as
	// Footer.svelte, whose links are external for a different reason.
	import { page } from '$app/state';
	import { session } from '$lib/session.svelte';
	import { goToLogin } from '$lib/auth';
	import type { Snippet } from 'svelte';

	let { children }: { children: Snippet } = $props();

	// Gate the entire admin area on auditor access. The server re-checks
	// every read, so this is display-only and provides no security.
	//
	// Only a signed-OUT visitor is sent to login. Someone signed in without
	// auditor access stays here and gets the explanation below: logging in
	// again cannot grant admin, so bouncing them to a screen they have
	// already satisfied is a loop rather than an answer.
	$effect(() => {
		if (session.resolved && !session.signedIn) {
			goToLogin(page.url.pathname);
		}
	});

	// The admin sections. This array includes routes managed by different feature branches.
	// Each branch owns a subset of these entries and expects all of them to be present.
	const adminNav = [
		{ route: '/admin/users', label: 'Users' },
		{ route: '/admin/certificates', label: 'Certificates' },
		{ route: '/admin/service-codes', label: 'Service codes' },
		{ route: '/admin/config', label: 'Config' },
		{ route: '/admin/audit', label: 'Audit log' }
	] as const satisfies ReadonlyArray<{ route: string; label: string }>;

	/** isCurrent matches a section's own page and everything under it, so a
	 * detail page (/admin/users/<id>) keeps its section marked rather than
	 * leaving the row with nothing selected. */
	function isCurrent(route: string): boolean {
		const path = page.url.pathname;
		return path === route || path.startsWith(route + '/');
	}
</script>

{#if session.user?.is_auditor}
	<!-- One tab row in the column, above each section's own heading. The
	     area used to open with a full-width band of stacked links, which
	     pushed every admin page a screenful down for navigation four of the
	     five sections do not need on arrival. The row scrolls sideways on a
	     narrow viewport rather than wrapping, so the sections stay one line
	     and the page below starts where it does on every other screen.

	     1100px is the one width the layout imposes anywhere in the app, and
	     it is the admin area's exception to a screen owning its own: the row
	     has to line up with whatever page is under it, and these pages range
	     from a 680px card list to a full-width table. -->
	<div class="flex w-full max-w-[1100px] flex-col gap-5">
		<nav
			aria-label="Admin sections"
			class="-mx-1 flex gap-1 overflow-x-auto border-b border-border-subtle px-1"
		>
			{#each adminNav as item (item.route)}
				<a
					href={item.route}
					aria-current={isCurrent(item.route) ? 'page' : undefined}
					class="border-b-2 px-3 py-2 text-[13px] font-medium whitespace-nowrap transition"
					class:border-accent={isCurrent(item.route)}
					class:text-accent={isCurrent(item.route)}
					class:border-transparent={!isCurrent(item.route)}
					class:text-ink-muted={!isCurrent(item.route)}
					class:hover:text-ink={!isCurrent(item.route)}
				>
					{item.label}
				</a>
			{/each}
		</nav>

		{@render children()}
	</div>
{:else if session.resolved}
	<!-- Signed in, but without auditor access. -->
	<div
		data-testid="admin-access-denied"
		class="flex flex-col items-center justify-center gap-4 py-12"
	>
		<p class="text-ink-muted">You do not have access to the admin area.</p>
	</div>
{:else}
	<!-- Loading -->
	<div class="flex flex-col items-center justify-center gap-4 py-12">
		<p class="text-ink-muted">Loading...</p>
	</div>
{/if}
