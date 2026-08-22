<script lang="ts">
	import { pushState } from '$app/navigation';
	import { page } from '$app/state';
	import { listCertificates } from '$lib/api/endpoints';
	import type { CertificateListResponse, CertificateRecord, CertificateType } from '$lib/api/types';
	import { errorMessage, redirectIfUnauthenticated } from '$lib/auth';
	import Alert from '$lib/components/Alert.svelte';
	import Button from '$lib/components/Button.svelte';
	import CertDetailModal from '$lib/components/CertDetailModal.svelte';
	import CertRow from '$lib/components/CertRow.svelte';
	import Icon from '$lib/components/Icon.svelte';
	import PageHeading from '$lib/components/PageHeading.svelte';

	// Cursor-paginated certificate history. The type filter and client-side
	// pagination apply only to loaded results — if the user filters to "host"
	// and all the currently-loaded certificates are "user" type, they'll see no
	// results until they load more pages. This is the accepted tradeoff of
	// load-more pagination over offset pagination.
	let allCertificates = $state<CertificateRecord[]>([]);
	let nextCursor = $state<string | null>(null);
	let loadError = $state<string | null>(null);
	let isLoading = $state(false);
	let hasLoaded = $state(false);

	// Filter and pagination state
	let selectedType = $state<CertificateType | 'all'>('all');
	let currentPage = $state(1);
	const pageSize = 10;

	// The filter tabs, in the order they read. "All" leads because it is the
	// state the page opens in.
	const tabs = [
		{ value: 'all' as const, label: 'All', icon: 'layout-grid' },
		{ value: 'user' as const, label: 'User', icon: 'user' },
		{ value: 'pam' as const, label: 'PAM', icon: 'terminal' },
		{ value: 'service' as const, label: 'Service', icon: 'cog' },
		{ value: 'host' as const, label: 'Host', icon: 'server' }
	];

	// What each certificate type's row is a record of.
	const rowEvents: Record<string, string> = {
		user: 'certificate requested',
		pam: 'certificate requested',
		service: 'service key requested',
		host: 'host enrollment requested'
	};

	// Rows say how long ago something was, so they need a clock that moves.
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

	const sorted = $derived(
		[...allCertificates].sort(
			(a, b) => new Date(b.issued_at).getTime() - new Date(a.issued_at).getTime()
		)
	);

	const filtered = $derived(
		selectedType === 'all' ? sorted : sorted.filter((c) => c.type === selectedType)
	);

	// Reset to page 1 when filter changes. Use void operator to suppress linter warning about unused value.
	$effect(() => {
		void selectedType;
		currentPage = 1;
	});

	const paginated = $derived(filtered.slice((currentPage - 1) * pageSize, currentPage * pageSize));

	const totalPages = $derived(Math.ceil(filtered.length / pageSize));

	const modalCert = $derived(filtered.find((c) => c.id === modalCertId));

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

<svelte:head><title>Certificate history · ssoossh</title></svelte:head>

<div class="flex w-full max-w-[680px] flex-col gap-5">
	<PageHeading eyebrow="History" title="Certificate history" />

	{#if loadError}
		<Alert variant="error" title="Could not load your history">{loadError}</Alert>
	{:else if !hasLoaded}
		<p class="text-sm text-ink-muted">Loading…</p>
	{:else if sorted.length === 0}
		<p class="text-sm text-ink-muted">No certificates have been issued to you yet.</p>
	{:else}
		<div class="flex flex-wrap items-center gap-2">
			{#each tabs as tab (tab.value)}
				<button
					type="button"
					onclick={() => (selectedType = tab.value)}
					aria-pressed={selectedType === tab.value}
					class="inline-flex items-center gap-1.5 rounded-full border px-3.5 py-1.5 text-xs font-semibold transition"
					class:border-accent={selectedType === tab.value}
					class:bg-accent={selectedType === tab.value}
					class:text-accent-ink={selectedType === tab.value}
					class:border-border-subtle={selectedType !== tab.value}
					class:text-ink-muted={selectedType !== tab.value}
					class:hover:bg-surface-muted={selectedType !== tab.value}
				>
					<Icon name={tab.icon} size="xs" />
					{tab.label}
				</button>
			{/each}
		</div>

		{#if filtered.length === 0}
			<p class="text-sm text-ink-muted">No certificates match the selected filter.</p>
		{:else}
			<div class="flex flex-col gap-2.5">
				{#each paginated as cert (cert.id)}
					<CertRow
						{cert}
						{now}
						event={rowEvents[cert.type] ?? 'certificate requested'}
						onclick={() => openCertDetail(cert.id)}
					/>
				{/each}
			</div>

			{#if totalPages > 1 || nextCursor}
				<div class="flex items-center justify-between gap-3 border-t border-border-subtle pt-2">
					<Button variant="ghost" disabled={currentPage === 1} onclick={() => currentPage--}>
						Previous
					</Button>
					<span class="text-xs text-ink-muted">
						Page {currentPage} of {totalPages} ({filtered.length} loaded)
					</span>
					<Button
						variant="ghost"
						disabled={currentPage === totalPages}
						onclick={() => currentPage++}
					>
						Next
					</Button>
				</div>

				{#if nextCursor}
					<div class="flex justify-center">
						<Button variant="ghost" busy={isLoading} onclick={loadMoreCertificates}>
							{isLoading ? 'Loading…' : 'Load more results'}
						</Button>
					</div>
				{/if}
			{/if}
		{/if}
	{/if}

	{#if modalCert}
		<CertDetailModal cert={modalCert} onclosed={closeCertDetail} />
	{/if}
</div>
