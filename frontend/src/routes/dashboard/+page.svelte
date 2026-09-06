<script lang="ts">
	import { resolve } from '$app/paths';
	import { listCertificates } from '$lib/api/endpoints';
	import type { CertificateListResponse, CertificateRecord } from '$lib/api/types';
	import { errorMessage, redirectIfUnauthenticated } from '$lib/auth';
	import Alert from '$lib/components/Alert.svelte';
	import Button from '$lib/components/Button.svelte';
	import CertRow from '$lib/components/CertRow.svelte';
	import PageHeading from '$lib/components/PageHeading.svelte';
	import PageShell from '$lib/components/PageShell.svelte';

	// Your recent decisions, newest first — there is deliberately no list of
	// requests awaiting approval. A request has no owner until someone opens
	// it, so such a list could only ever show everyone's, and approving a
	// stranger's request issues a certificate carrying *your* principals to
	// *their* key. The approval URL reaches a human one way: their own client
	// prints it.
	//
	// Denied requests cannot appear here yet: the list endpoint returns
	// issued certificates, and a denial never produces one. Every row is
	// therefore an approval until the server exposes decisions in their own
	// right.
	let allCertificates = $state<CertificateRecord[]>([]);
	let nextCursor = $state<string | null>(null);
	let loadError = $state<string | null>(null);
	let isLoading = $state(false);
	let hasLoaded = $state(false);

	// Recomputed against a clock that ticks, not against load time: this page
	// is the sort of thing that stays open in a tab, and "requested 2h ago"
	// should not still say that tomorrow.
	let now = $state(new Date());
	$effect(() => {
		const timer = setInterval(() => (now = new Date()), 30_000);
		return () => clearInterval(timer);
	});

	// What each certificate type's row is a record of.
	const rowEvents: Record<string, string> = {
		user: 'certificate requested',
		pam: 'certificate requested',
		console: 'console login',
		service: 'service key requested'
	};

	// A row opens the certificate's own page rather than a dialog over the
	// list. `from` is what the back chip there reads, so a reader who came
	// from this list is returned to it rather than to the full history.
	function certHref(certId: string): string {
		return `${resolve(`/certs/${certId}`)}?from=dashboard`;
	}

	async function loadMoreCertificates() {
		if (isLoading || !nextCursor) {
			return;
		}
		isLoading = true;
		const controller = new AbortController();
		try {
			const result = await listCertificates(controller.signal, nextCursor, 25);
			allCertificates = [...allCertificates, ...result.certificates];
			nextCursor = result.next_cursor ?? null;
		} catch (cause) {
			if (!controller.signal.aborted && !redirectIfUnauthenticated(cause)) {
				loadError = errorMessage(cause);
			}
		} finally {
			isLoading = false;
		}
	}

	$effect(() => {
		const controller = new AbortController();

		listCertificates(controller.signal, null, 25)
			.then((result: CertificateListResponse) => {
				allCertificates = result.certificates;
				nextCursor = result.next_cursor ?? null;
				hasLoaded = true;
			})
			.catch((cause) => {
				if (controller.signal.aborted || redirectIfUnauthenticated(cause)) {
					return;
				}
				loadError = errorMessage(cause);
				hasLoaded = true;
			});

		return () => controller.abort();
	});
</script>

<svelte:head><title>Dashboard · ssoossh</title></svelte:head>

<PageShell width="wide">
	<PageHeading eyebrow="Activity" title="Recent decisions">
		{#snippet action()}
			<a
				href={resolve('/logs/me')}
				class="text-[13px] font-medium whitespace-nowrap text-accent hover:underline"
			>
				View all history &rarr;
			</a>
		{/snippet}
	</PageHeading>

	{#if loadError}
		<Alert variant="error" title="Could not load your certificates">{loadError}</Alert>
	{/if}

	{#if !hasLoaded}
		<p class="text-sm text-ink-muted">Loading…</p>
	{:else if allCertificates.length === 0}
		<p class="text-sm text-ink-muted">
			Nothing yet. Run <code class="font-mono">ssoossh login</code> to request a certificate.
		</p>
	{:else}
		<div class="flex flex-col gap-2.5">
			{#each allCertificates as cert (cert.id)}
				<CertRow
					{cert}
					{now}
					event={rowEvents[cert.type] ?? 'certificate requested'}
					testid="cert-row"
					href={certHref(cert.id)}
				/>
			{/each}
		</div>
	{/if}

	{#if nextCursor}
		<div class="flex justify-center">
			<Button variant="ghost" busy={isLoading} onclick={loadMoreCertificates}>
				{isLoading ? 'Loading…' : 'Load more'}
			</Button>
		</div>
	{/if}
</PageShell>
