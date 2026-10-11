<script lang="ts">
	import { resolve } from '$app/paths';
	import { listCertificates } from '#lib/api/endpoints.js';
	import type { CertificateListResponse, CertificateRecord } from '#lib/api/types.js';
	import { errorMessage, redirectIfUnauthenticated } from '#lib/auth.js';
	import Alert from '#lib/components/Alert.svelte';
	import CertRow from '#lib/components/CertRow.svelte';
	import EmptyState from '#lib/components/EmptyState.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import LoadingBlock from '#lib/components/LoadingBlock.svelte';
	import PageHeading from '#lib/components/PageHeading.svelte';
	import PageShell from '#lib/components/PageShell.svelte';
	import { certificateEvent } from '#lib/rowEvents.js';

	// Your recent certificates, newest first — there is deliberately no list of
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
	let certificates = $state<CertificateRecord[]>([]);
	let loadError = $state<string | null>(null);
	let hasLoaded = $state(false);
	// Bumped by Try again; the first load reads it, so a bump re-runs it.
	let attempt = $state(0);

	// How many rows the summary shows. Older ones are the history page's,
	// which "View all history" leads to: paging here made the landing page a
	// second history with no refusals, no filters and no search, that a
	// reader could take for the whole record.
	const recentCount = 5;

	// Recomputed against a clock that ticks, not against load time: this page
	// is the sort of thing that stays open in a tab, and "issued 2h ago"
	// should not still say that tomorrow.
	let now = $state(new Date());
	$effect(() => {
		const timer = setInterval(() => (now = new Date()), 30_000);
		return () => clearInterval(timer);
	});

	// A row opens the certificate's own page rather than a dialog over the
	// list. `from` is what the back chip there reads, so a reader who came
	// from this list is returned to it rather than to the full history.
	function certHref(certId: string): string {
		return `${resolve(`certs/${certId}`)}?from=dashboard`;
	}

	// The way out of a failed load: back to the loading shape, then the
	// same request again.
	function retry() {
		loadError = null;
		hasLoaded = false;
		attempt += 1;
	}

	$effect(() => {
		void attempt;
		const controller = new AbortController();

		listCertificates(controller.signal, null, recentCount)
			.then((result: CertificateListResponse) => {
				certificates = result.certificates.slice(0, recentCount);
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
	<PageHeading title="Recent certificates">
		{#snippet action()}
			<a
				href={resolve('logs/me')}
				class="inline-flex min-h-target items-center gap-1 text-dense font-medium whitespace-nowrap text-accent hover:underline"
				data-testid="dashboard-history-link"
			>
				View all history
				<Icon name="arrow-right" size="xs" />
			</a>
		{/snippet}
	</PageHeading>

	{#if loadError}
		<Alert
			variant="error"
			title="Could not load your recent certificates"
			onretry={retry}
			testid="dashboard-error"
		>
			{loadError}
		</Alert>
	{:else if !hasLoaded}
		<LoadingBlock shape="rows" count={3} testid="dashboard-loading" />
	{:else if certificates.length === 0}
		<EmptyState icon="certificate-off" title="No certificates yet" testid="dashboard-empty">
			{#snippet action()}
				<a
					href={resolve('console')}
					class="inline-flex min-h-target items-center text-dense font-medium text-accent hover:underline"
					data-testid="dashboard-empty-console">Enter a console code</a
				>
			{/snippet}
			Run <code class="font-mono">ssoossh login</code> to request a certificate, or log in at a machine's
			console and enter the code it shows. Each one appears here as soon as it is issued.
		</EmptyState>
	{:else}
		<div class="flex flex-col gap-2">
			{#each certificates as cert (cert.id)}
				<CertRow
					{cert}
					{now}
					event={certificateEvent(cert.type)}
					testid="cert-row"
					href={certHref(cert.id)}
				/>
			{/each}
		</div>
	{/if}
</PageShell>
