<script lang="ts">
	import { resolve } from '$app/paths';
	import { listCertificates } from '#lib/api/endpoints.js';
	import type { CertificateListResponse, CertificateRecord } from '#lib/api/types.js';
	import { errorMessage, redirectIfUnauthenticated } from '#lib/auth.js';
	import Alert from '#lib/components/Alert.svelte';
	import Button from '#lib/components/Button.svelte';
	import CertRow from '#lib/components/CertRow.svelte';
	import EmptyState from '#lib/components/EmptyState.svelte';
	import LoadingBlock from '#lib/components/LoadingBlock.svelte';
	import PageHeading from '#lib/components/PageHeading.svelte';
	import PageShell from '#lib/components/PageShell.svelte';

	// Your recent decisions, newest first — there is deliberately no list of
	// requests awaiting approval. A request has no owner until someone opens
	// it, so such a list could only ever show everyone's, and approving a
	// stranger's request issues a certificate carrying *your* principals to
	// *their* key. The approval URL reaches a human one way: their own client
	// prints it.
	//
	// Every row here is an approval, which is now a choice rather than a
	// limit: /api/decisions/denied exists and the full history at /logs/me
	// interleaves it. This page is the five most recent things, and it is
	// the one screen somebody lands on rather than navigates to — a refusal
	// belongs in the history they open deliberately, not in the summary
	// they get by signing in.
	let allCertificates = $state<CertificateRecord[]>([]);
	let nextCursor = $state<string | null>(null);
	let loadError = $state<string | null>(null);
	// A failed "Load more" is its own error: the rows already shown are
	// still right, so it sits by the button rather than replacing them.
	let moreError = $state<string | null>(null);
	let isLoading = $state(false);
	let hasLoaded = $state(false);
	// Bumped by Try again; the first load reads it, so a bump re-runs it.
	let attempt = $state(0);

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
		return `${resolve(`certs/${certId}`)}?from=dashboard`;
	}

	async function loadMoreCertificates() {
		if (isLoading || !nextCursor) {
			return;
		}
		isLoading = true;
		moreError = null;
		const controller = new AbortController();
		try {
			const result = await listCertificates(controller.signal, nextCursor, 25);
			allCertificates = [...allCertificates, ...result.certificates];
			nextCursor = result.next_cursor ?? null;
		} catch (cause) {
			if (!controller.signal.aborted && !redirectIfUnauthenticated(cause)) {
				moreError = errorMessage(cause);
			}
		} finally {
			isLoading = false;
		}
	}

	// The way out of a failed first load: back to the loading shape, then
	// the same request again.
	function retry() {
		loadError = null;
		hasLoaded = false;
		attempt += 1;
	}

	$effect(() => {
		void attempt;
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
	<PageHeading title="Recent decisions">
		{#snippet action()}
			<a
				href={resolve('logs/me')}
				class="text-dense font-medium whitespace-nowrap text-accent hover:underline"
				>View all history →</a
			>
		{/snippet}
	</PageHeading>

	{#if loadError}
		<Alert
			variant="error"
			title="Could not load your certificates"
			onretry={retry}
			testid="dashboard-error"
		>
			{loadError}
		</Alert>
	{:else if !hasLoaded}
		<LoadingBlock shape="rows" count={3} testid="dashboard-loading" />
	{:else if allCertificates.length === 0}
		<EmptyState icon="certificate-off" title="Nothing yet" testid="dashboard-empty">
			Run <code class="font-mono">ssoossh login</code> to request a certificate. It appears here as soon
			as it is issued.
		</EmptyState>
	{:else}
		<div class="flex flex-col gap-2">
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

	{#if moreError}
		<Alert
			variant="error"
			title="Could not load more certificates"
			onretry={loadMoreCertificates}
			testid="dashboard-more-error"
		>
			{moreError}
		</Alert>
	{/if}

	{#if nextCursor && !loadError}
		<div class="flex justify-center">
			<Button variant="ghost" busy={isLoading} onclick={loadMoreCertificates}>
				{isLoading ? 'Loading…' : 'Load more'}
			</Button>
		</div>
	{/if}
</PageShell>
