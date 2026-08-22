<script lang="ts">
	import { pushState } from '$app/navigation';
	import { page } from '$app/state';
	import { listCertificates } from '$lib/api/endpoints';
	import type { CertificateListResponse, CertificateRecord, CertificateType } from '$lib/api/types';
	import { errorMessage, redirectIfUnauthenticated } from '$lib/auth';
	import Alert from '$lib/components/Alert.svelte';
	import Card from '$lib/components/Card.svelte';
	import CertDetailModal from '$lib/components/CertDetailModal.svelte';
	import Icon from '$lib/components/Icon.svelte';
	import { formatDateTime, isExpired } from '$lib/format';

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

	// Icon mapping for certificate types
	const certTypeIcons: Record<string, string> = {
		user: 'user',
		pam: 'terminal',
		service: 'cog',
		host: 'server'
	};

	// Shallow routing for cert detail modal
	const modalCertId = $derived(page.url.searchParams.get('modal'));

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
		pushState(url, {});
	}

	function closeCertDetail() {
		const url = new URL(page.url);
		url.searchParams.delete('modal');
		// eslint-disable-next-line svelte/no-navigation-without-resolve
		pushState(url, {});
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

<Card
	title="Your certificate history"
	description="The certificates themselves are never stored — this is the record that one was issued."
>
	{#if loadError}
		<Alert variant="error" title="Could not load your history">{loadError}</Alert>
	{:else if !hasLoaded}
		<p class="text-sm text-ink-muted">Loading…</p>
	{:else if sorted.length === 0}
		<p class="text-sm text-ink-muted">No certificates have been issued to you yet.</p>
	{:else}
		<!-- Type filter tabs -->
		<div class="mb-4 flex flex-wrap gap-2 border-b border-border-subtle pb-3">
			{#each ['all', 'user', 'pam', 'service', 'host'] as type (type)}
				<button
					onclick={() => (selectedType = type as CertificateType | 'all')}
					class="inline-flex items-center gap-1.5 rounded-md px-3 py-2 text-sm font-medium transition"
					class:bg-accent={selectedType === type}
					class:text-accent-ink={selectedType === type}
					class:text-ink-muted={selectedType !== type}
					class:hover:bg-surface-muted={selectedType !== type}
				>
					{#if type !== 'all'}
						<Icon name={certTypeIcons[type] || 'zap'} size="sm" ariaLabel={type} />
					{:else}
						<Icon name="zap" size="sm" ariaLabel="All" />
					{/if}
					<span>{type.charAt(0).toUpperCase() + type.slice(1)}</span>
				</button>
			{/each}
		</div>

		{#if filtered.length === 0}
			<p class="text-sm text-ink-muted">No certificates match the selected filter.</p>
		{:else}
			<ul class="divide-y divide-border-subtle">
				{#each paginated as cert (cert.id)}
					<li>
						<button
							type="button"
							onclick={() => openCertDetail(cert.id)}
							class="-mx-3 w-full cursor-pointer rounded px-3 py-3 text-left transition hover:bg-surface-muted focus:ring-2 focus:ring-accent focus:ring-offset-0 focus:outline-none"
						>
							<div class="flex flex-wrap items-center justify-between gap-2">
								<div class="flex items-center gap-2">
									<div
										class="inline-flex items-center justify-center rounded bg-surface-muted px-2 py-1.5"
									>
										<Icon
											name={certTypeIcons[cert.type] || 'zap'}
											size="sm"
											ariaLabel="Certificate type: {cert.type}"
										/>
									</div>
									<div class="flex flex-wrap gap-1.5">
										{#if cert.principals}
											{#each cert.principals
												.split(',')
												.map((p) => p.trim())
												.filter((p) => p.length > 0) as principal (principal)}
												<code
													class="rounded border border-border-subtle bg-surface px-2 py-0.5 font-mono text-xs text-ink"
												>
													{principal}
												</code>
											{/each}
										{/if}
									</div>
								</div>
								<span class="text-xs text-ink-muted">
									{isExpired(cert.expires_at) ? 'expired' : 'active'}
								</span>
							</div>
							<div
								class="mt-2 flex flex-wrap items-center justify-between gap-2 text-xs text-ink-muted"
							>
								<span>issued {formatDateTime(cert.issued_at)}</span>
								<span class="font-mono text-ink-muted">{cert.key_id}</span>
								{#if cert.decided_at}
									— approved by {cert.decided_by_username || cert.decided_by_subject || "system"}
								{/if}
							</div>
						</button>
					</li>
				{/each}
			</ul>

			<!-- Pagination controls -->
			{#if totalPages > 1 || nextCursor}
				<div class="mt-4 flex items-center justify-between border-t border-border-subtle pt-3">
					<div class="text-xs text-ink-muted">
						Page {currentPage} of {totalPages} ({filtered.length} loaded)
					</div>
					<div class="flex gap-2">
						<button
							onclick={() => currentPage--}
							disabled={currentPage === 1}
							class="rounded-md border border-border-subtle p-2 transition hover:bg-surface-muted disabled:cursor-not-allowed disabled:opacity-50"
							aria-label="Previous page"
						>
							<Icon name="chevron-left" size="sm" />
						</button>
						<button
							onclick={() => currentPage++}
							disabled={currentPage === totalPages}
							class="rounded-md border border-border-subtle p-2 transition hover:bg-surface-muted disabled:cursor-not-allowed disabled:opacity-50"
							aria-label="Next page"
						>
							<Icon name="chevron-right" size="sm" />
						</button>
					</div>
				</div>

				{#if nextCursor}
					<div class="mt-3 flex justify-center">
						<button
							onclick={loadMoreCertificates}
							disabled={isLoading}
							class="rounded-md border border-border-subtle px-4 py-2 transition hover:bg-surface-muted disabled:cursor-not-allowed disabled:opacity-50"
						>
							{isLoading ? 'Loading…' : 'Load more results'}
						</button>
					</div>
				{/if}
			{/if}
		{/if}
	{/if}

	{#if modalCert}
		<CertDetailModal cert={modalCert} onclosed={closeCertDetail} />
	{/if}
</Card>
