<script lang="ts">
	import { page } from '$app/state';
	import type { Snippet } from 'svelte';

	import { goToLogin } from '#lib/auth.js';
	import Alert from '#lib/components/Alert.svelte';
	import LoadingBlock from '#lib/components/LoadingBlock.svelte';
	import PageHeading from '#lib/components/PageHeading.svelte';
	import PageShell from '#lib/components/PageShell.svelte';
	import { session } from '#lib/session.svelte.js';

	let { children }: { children: Snippet } = $props();

	// The fleet key pages sit outside /admin on purpose: a key's own group
	// manages it without any admin role, and the admin layout would turn
	// those people away. So this layout does the two things the admin one
	// does for its area, and no role check: who may see which key is the
	// server's answer, page by page.
	//
	// - A signed-out visitor goes to sign in, carrying where they were.
	// - With nss.enabled off every /api/nss route answers 404, so the pages
	//   would only fail; they get one sentence saying why instead.
	$effect(() => {
		if (session.resolved && !session.signedIn) {
			goToLogin(page.url.pathname);
		}
	});
</script>

<svelte:head>
	{#if session.user && !session.user.nss_enabled}<title>NSS keys · ssoossh</title>{/if}
</svelte:head>

{#if session.user && !session.user.nss_enabled}
	<PageShell width="wide">
		<PageHeading title="NSS keys" />
		<Alert variant="info" title="The NSS directory is off" testid="nss-keys-disabled">
			<code class="font-mono">nss.enabled</code> is false on this server, so hosts do not look up accounts
			here and there are no fleet keys to manage.
		</Alert>
	</PageShell>
{:else if session.user}
	{@render children()}
{:else}
	<PageShell width="wide">
		<LoadingBlock shape="lines" count={3} label="Loading…" testid="nss-keys-session-loading" />
	</PageShell>
{/if}
