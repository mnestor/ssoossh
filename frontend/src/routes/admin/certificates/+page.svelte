<script lang="ts">
	import { resolve } from '$app/paths';
	import { listAdminCertificates } from '$lib/api/endpoints';
	import type { CertificateResponse } from '$lib/api/types';
	import { errorMessage, redirectIfUnauthenticated } from '$lib/auth';
	import Alert from '$lib/components/Alert.svelte';
	import CertRow from '$lib/components/CertRow.svelte';
	import PageHeading from '$lib/components/PageHeading.svelte';
	import Pager from '$lib/components/Pager.svelte';
	import SearchInput from '$lib/components/SearchInput.svelte';

	let certificates = $state<CertificateResponse[]>([]);
	let pageInfo = $state({ total: 0, limit: 25, offset: 0, page: 1, page_count: 1 });
	let searchQuery = $state('');
	let typeFilter = $state('');
	let statusFilter = $state('');
	let loadError = $state<string | null>(null);
	let isLoading = $state(false);
	let hasLoaded = $state(false);

	const certTypes = ['user', 'service', 'pam'];
	const statusOptions = ['live', 'expired'];

	async function loadCertificates(offset = 0) {
		isLoading = true;
		const controller = new AbortController();
		try {
			const result = await listAdminCertificates(controller.signal, {
				offset,
				limit: 25,
				q: searchQuery || undefined,
				type: typeFilter || undefined,
				status: statusFilter || undefined
			});
			certificates = result.certificates;
			pageInfo = result.page_meta;
			hasLoaded = true;
		} catch (cause) {
			if (!controller.signal.aborted && !redirectIfUnauthenticated(cause)) {
				loadError = errorMessage(cause);
				hasLoaded = true;
			}
		} finally {
			isLoading = false;
		}
	}

	// Initial load
	$effect(() => {
		loadCertificates(0);
	});

	function handleSearch(query: string) {
		searchQuery = query;
		loadCertificates(0);
	}

	function handleTypeFilter(type: string) {
		typeFilter = typeFilter === type ? '' : type;
		loadCertificates(0);
	}

	function handleStatusFilter(status: string) {
		statusFilter = statusFilter === status ? '' : status;
		loadCertificates(0);
	}

	function handlePage(offset: number) {
		loadCertificates(offset);
	}
</script>

<svelte:head><title>Certificates · ssoossh</title></svelte:head>

<div class="flex w-full max-w-[680px] flex-col gap-5">
	<PageHeading eyebrow="Admin" title="Certificate history" />

	{#if loadError}
		<Alert variant="error" title="Could not load certificates">{loadError}</Alert>
	{/if}

	<div class="flex flex-col gap-3">
		<SearchInput
			label="Search certificates"
			placeholder="Key ID, principal, fingerprint, username, email…"
			value={searchQuery}
			onsearch={handleSearch}
			testid="search-input"
		/>

		<div class="flex flex-col gap-2">
			<div data-testid="type-filter" class="flex gap-2">
				<span class="text-xs font-semibold text-ink-muted">Type:</span>
				<button
					type="button"
					onclick={() => handleTypeFilter('')}
					class="inline-flex items-center gap-2 rounded-md border {typeFilter === ''
						? 'border-accent bg-accent text-accent-ink'
						: 'border-border-subtle text-ink hover:bg-surface-muted'} px-3 py-1.5 text-xs font-semibold transition disabled:opacity-50"
					disabled={isLoading}
				>
					All
				</button>
				{#each certTypes as type (type)}
					<button
						type="button"
						onclick={() => handleTypeFilter(type)}
						class="inline-flex items-center gap-2 rounded-md border {typeFilter === type
							? 'border-accent bg-accent text-accent-ink'
							: 'border-border-subtle text-ink hover:bg-surface-muted'} px-3 py-1.5 text-xs font-semibold transition disabled:opacity-50"
						disabled={isLoading}
					>
						{type}
					</button>
				{/each}
			</div>

			<div class="flex gap-2">
				<span class="text-xs font-semibold text-ink-muted">Status:</span>
				{#each statusOptions as status (status)}
					<button
						type="button"
						onclick={() => handleStatusFilter(status)}
						class="inline-flex items-center gap-2 rounded-md border {statusFilter === status
							? 'border-accent bg-accent text-accent-ink'
							: 'border-border-subtle text-ink hover:bg-surface-muted'} px-3 py-1.5 text-xs font-semibold transition disabled:opacity-50"
						disabled={isLoading}
					>
						{status}
					</button>
				{/each}
			</div>
		</div>
	</div>

	{#if !hasLoaded}
		<p class="text-sm text-ink-muted">Loading…</p>
	{:else if certificates.length === 0}
		<p class="text-sm text-ink-muted">No certificates found matching your search.</p>
	{:else}
		<div data-testid="cert-list" class="flex flex-col gap-2.5">
			{#each certificates as cert (cert.id)}
				<a href={resolve(`/certs/${cert.id}`)}>
					<CertRow
						cert={{
							...cert,
							decided_by_outcome: cert.decided_by_outcome || undefined,
							decided_by_subject: cert.decided_by_subject || undefined,
							decided_by_username: cert.decided_by_username || undefined,
							decided_by_email: cert.decided_by_email || undefined,
							decided_by_groups: cert.decided_by_groups || [],
							decided_by_other_accounts: cert.decided_by_other_accounts || [],
							decided_by_service_accounts: cert.decided_by_service_accounts || [],
							decided_source_ip: cert.decided_source_ip || undefined,
							decided_user_agent: cert.decided_user_agent || undefined,
							decided_accept_language: cert.decided_accept_language || undefined,
							decided_forwarded_for: cert.decided_forwarded_for || undefined,
							decided_at: cert.decided_at || undefined,
							retrieved_source_ip: cert.retrieved_source_ip || undefined,
							retrieved_at: cert.retrieved_at || undefined,
							enrollment_id: cert.enrollment_id || undefined
						}}
						event="certificate issued"
						testid="cert-row"
						onclick={() => {}}
					/>
				</a>
			{/each}
		</div>
	{/if}

	<Pager meta={pageInfo} onpage={handlePage} busy={isLoading} testid="pager" />
</div>
