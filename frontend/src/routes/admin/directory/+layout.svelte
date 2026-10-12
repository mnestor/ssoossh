<script lang="ts">
	import { session } from '#lib/session.svelte.js';
	import AccessDenied from '#lib/components/AccessDenied.svelte';
	import type { Snippet } from 'svelte';

	let { children }: { children: Snippet } = $props();

	// The Directory is admin-only (issue #136), a narrower gate inside the
	// admin area's auditor one. The parent layout has already settled the
	// session and turned away anyone below auditor, so the only question
	// left here is admin or not.
	//
	// Display only: the server refuses the status read to SOC and auditors on
	// its own. What this buys is the not-authorized card in place of the page
	// starting a fetch that can only fail with a 403.
</script>

{#if session.user?.is_admin}
	{@render children()}
{:else}
	<AccessDenied
		requirement="The Directory needs the admin role. Ask an administrator if you need it."
		testid="directory-access-denied"
	/>
{/if}
