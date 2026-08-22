<script lang="ts">
	import { pushState } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { listCertificates } from '$lib/api/endpoints';
	import type { CertificateListResponse, CertificateRecord } from '$lib/api/types';
	import { errorMessage, redirectIfUnauthenticated } from '$lib/auth';
	import Alert from '$lib/components/Alert.svelte';
	import Button from '$lib/components/Button.svelte';
	import CertDetailModal from '$lib/components/CertDetailModal.svelte';
	import CertRow from '$lib/components/CertRow.svelte';
	import PageHeading from '$lib/components/PageHeading.svelte';

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

	// Shallow routing keeps the open certificate in page.state; the search
	// parameter is what a pasted link arrives with, and is the fallback until
	// something on this page opens or closes a modal.
	const modalCertId = $derived(
		'modalCertId' in page.state ? page.state.modalCertId : page.url.searchParams.get('modal')
	);
	const modalCert = $derived(allCertificates.find((c) => c.id === modalCertId));

	// What each certificate type's row is a record of.
	const rowEvents: Record<string, string> = {
		user: 'certificate requested',
		pam: 'certificate requested',
		service: 'service key requested',
		host: 'host enrollment requested'
	};

	// Shallow-route within this same page (a modal query param), not a
	// navigation to a different route id — resolve() is for the latter, so
	// it doesn't apply here, same reasoning as the caller-supplied returnTo
	// path on the login page.
	function openCertDetail(certId: string) {
		const url = new URL(page.url);
		url.searchParams.set('modal', certId);
		// eslint-disable-next-line svelte/no-navigation-without-resolve
		pushState(url, { modalCertId: certId });
	}

	// Closing records an explicit null rather than an empty state: an absent
	// modalCertId means "nothing has been opened or closed here yet", which
	// falls back to the search parameter — and on a page reached by a pasted
	// ?modal= link, that would reopen the certificate the moment it closed.
	function closeCertDetail() {
		const url = new URL(page.url);
		url.searchParams.delete('modal');
		// eslint-disable-next-line svelte/no-navigation-without-resolve
		pushState(url, { modalCertId: null });
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

<div class="flex w-full max-w-[680px] flex-col gap-5">
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
					onclick={() => openCertDetail(cert.id)}
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

	{#if modalCert}
		<CertDetailModal cert={modalCert} onclosed={closeCertDetail} />
	{/if}
</div>
