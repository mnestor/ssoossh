<script lang="ts">
	import { session } from '#lib/session.svelte.js';
	import AccessDenied from '#lib/components/AccessDenied.svelte';
	import type { Snippet } from 'svelte';

	let { children }: { children: Snippet } = $props();

	// Claims echo is admin-only (issue #161), a narrower gate inside the
	// admin area's auditor one. The parent layout has already settled the
	// session and turned away anyone below auditor, so the only question
	// left here is admin or not.
	//
	// Display only: the server refuses to start an echo for SOC and auditors
	// on its own (server/controller/identityecho.go). What this buys is the
	// not-authorized card in place of a page whose only action would fail
	// with a 403.
</script>

{#if session.user?.is_admin}
	{@render children()}
{:else}
	<AccessDenied
		requirement="Claims echo needs the admin role. Ask an administrator if you need it."
		testid="echo-access-denied"
	/>
{/if}
