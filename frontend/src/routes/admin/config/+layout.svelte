<script lang="ts">
	import { session } from '#lib/session.svelte.js';
	import AccessDenied from '#lib/components/AccessDenied.svelte';
	import type { Snippet } from 'svelte';

	let { children }: { children: Snippet } = $props();

	// Config is admin-only (SSOOSSH.md), a narrower gate inside the admin
	// area's auditor one. The parent layout has already settled the session
	// and turned away anyone below auditor, so the only question left here is
	// admin or not.
	//
	// Display only: the server refuses the config to SOC and auditors on its
	// own (server/controller/admin.go). What this buys is the not-authorized
	// card in place of a page whose only load would fail with a 403.
</script>

{#if session.user?.is_admin}
	{@render children()}
{:else}
	<AccessDenied
		requirement="Config needs the admin role. Ask an administrator if you need it."
		testid="config-access-denied"
	/>
{/if}
