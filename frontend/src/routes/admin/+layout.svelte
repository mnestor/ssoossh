<script lang="ts">
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
</script>

{#if session.user?.is_auditor}
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
