<script lang="ts">
	import { page } from '$app/state';
	import { session } from '#lib/session.svelte.js';
	import { goToLogin } from '#lib/auth.js';
	import AccessDenied from '#lib/components/AccessDenied.svelte';
	import LoadingBlock from '#lib/components/LoadingBlock.svelte';
	import { adminRouteOpen } from '#lib/nav.js';
	import type { Snippet } from 'svelte';

	let { children }: { children: Snippet } = $props();

	// Gate the entire admin area on auditor access, with one exception the
	// nav model names (adminRouteOpen): the fleet key admin role opens All
	// NSS keys, the keys it manages, and only that. Every other admin page
	// refuses it below. The server re-checks every read, so this is
	// display-only and provides no security.
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
</script>

{#if adminRouteOpen(session.user, page.url.pathname)}
	<!-- The gate, and nothing else.

	     This layout used to own two things it had no business owning. It
	     carried a tab row naming the eight admin sections, which the rail
	     now carries alongside every other destination in the app; and it
	     pinned a 1100px width on pages that then re-declared 680px or
	     max-w-full inside it, so an admin page's width was settled in two
	     files that disagreed. Both are gone: each page states its own width
	     through PageShell, the same way every non-admin page does. -->
	{@render children()}
{:else if session.resolved}
	<!-- Signed in, but without auditor access (or a fleet key admin on a
	     page other than All NSS keys). The same card the error page gives a
	     403, rather than one muted sentence with no heading and no way
	     out. -->
	<AccessDenied
		requirement="The admin area needs an admin, SOC or auditor role. Ask an administrator if you need it."
		testid="admin-access-denied"
	/>
{:else}
	<!-- Loading -->
	<div class="flex flex-col justify-center gap-4 py-12">
		<LoadingBlock shape="lines" count={3} label="Loading…" testid="admin-loading" />
	</div>
{/if}
