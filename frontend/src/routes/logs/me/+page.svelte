<script lang="ts">
	import { resolve } from '$app/paths';
	import { listCertificates, listDeniedRequests } from '$lib/api/endpoints';
	import type { CertificateListResponse, CertificateRecord, DeniedRequest } from '$lib/api/types';
	import { errorMessage, redirectIfUnauthenticated } from '$lib/auth';
	import Alert from '$lib/components/Alert.svelte';
	import Button from '$lib/components/Button.svelte';
	import CertRow from '$lib/components/CertRow.svelte';
	import DeniedRow from '$lib/components/DeniedRow.svelte';
	import FilterGroup from '$lib/components/FilterGroup.svelte';
	import PageHeading from '$lib/components/PageHeading.svelte';
	import PageShell from '$lib/components/PageShell.svelte';
	import SearchInput from '$lib/components/SearchInput.svelte';
	import { outcomeFilters, statusFilters, typeFilters } from '$lib/filters';
	import { isExpired } from '$lib/format';

	// Cursor-paginated decision history: what was issued, and what was
	// refused. The type filter and client-side pagination apply only to
	// loaded results — if the user filters to "console" and everything
	// loaded so far is "user", they see nothing until they load more pages.
	// This is the accepted tradeoff of load-more pagination over offset
	// pagination.
	//
	// Two sources because the server has two. /api/certs reads the
	// certificates table, and a denial never writes a row there, so the half
	// of somebody's decisions that says "no" is served by
	// /api/decisions/denied and interleaved here by time. That half is the
	// one an incident review asks about first, and until now this page could
	// not show it at all.
	let allCertificates = $state<CertificateRecord[]>([]);
	let allDenials = $state<DeniedRequest[]>([]);
	let nextCursor = $state<string | null>(null);
	let nextDenialCursor = $state<string | null>(null);
	let loadError = $state<string | null>(null);
	let isLoading = $state(false);
	let hasLoaded = $state(false);

	// Filter state. The vocabulary is shared with /admin/certificates —
	// same labels, same icons, same order; see $lib/filters. Only the
	// outcome group is this page's alone, because the admin list reads the
	// certificates table and a denial never writes a row there.
	//
	// Approved by default, not both. This page has always been the list of
	// certificates somebody holds, and that is what most visits are for;
	// opening it on a mixture would change what an existing reader gets
	// without their asking. A refusal is one click away instead, which is
	// the right place for a thing you go looking for.
	let selectedOutcome = $state('approved');
	let selectedType = $state('');
	let selectedStatus = $state('');
	let searchQuery = $state('');

	let currentPage = $state(1);
	const pageSize = 10;

	// What each certificate type's row is a record of.
	const rowEvents: Record<string, string> = {
		user: 'certificate requested',
		pam: 'certificate requested',
		console: 'console login',
		service: 'service key requested'
	};

	// Rows say how long ago something was, so they need a clock that moves.
	let now = $state(new Date());
	$effect(() => {
		const timer = setInterval(() => (now = new Date()), 30_000);
		return () => clearInterval(timer);
	});

	// One list of two kinds of thing, newest first. A discriminated union
	// rather than a shared shape: the two have almost no fields in common,
	// and the row components take the record each was written for.
	type Entry =
		| { kind: 'certificate'; at: number; cert: CertificateRecord }
		| { kind: 'denial'; at: number; denial: DeniedRequest };

	const sorted = $derived<Entry[]>(
		[
			...allCertificates.map((cert): Entry => ({
				kind: 'certificate',
				at: new Date(cert.issued_at).getTime(),
				cert
			})),
			...allDenials.map((denial): Entry => ({
				kind: 'denial',
				at: new Date(denial.decided_at).getTime(),
				denial
			}))
		].sort((a, b) => b.at - a.at)
	);

	// Outcome, then type, then validity, then the search term. Each stage
	// narrows what the last one left, and every one of them works on what
	// has been loaded rather than on what the server holds.
	const byOutcome = $derived(
		selectedOutcome === ''
			? sorted
			: sorted.filter((entry) =>
					selectedOutcome === 'denied' ? entry.kind === 'denial' : entry.kind === 'certificate'
				)
	);

	// A denial whose request row is gone reports no type, so it survives
	// only the "All" type filter. It has to survive that one: dropping it
	// everywhere would quietly shorten the history rather than filter it.
	const byType = $derived(
		selectedType === ''
			? byOutcome
			: byOutcome.filter((entry) =>
					entry.kind === 'certificate'
						? entry.cert.type === selectedType
						: entry.denial.type === selectedType
				)
	);

	// Validity is a property of an issued certificate, so a denial is
	// neither live nor expired and is excluded by either. That is the
	// honest answer rather than a bug: nothing was issued to still be
	// working.
	const byStatus = $derived(
		selectedStatus === ''
			? byType
			: byType.filter(
					(entry) =>
						entry.kind === 'certificate' &&
						isExpired(entry.cert.expires_at, now) === (selectedStatus === 'expired')
				)
	);

	/** haystack is everything about one row that a search should match. */
	function haystack(entry: Entry): string {
		if (entry.kind === 'certificate') {
			const c = entry.cert;
			return [
				c.key_id,
				c.principals,
				c.serial_number,
				c.public_key_fingerprint,
				c.reported_username,
				c.reported_hostname,
				c.retrieved_source_ip
			]
				.filter(Boolean)
				.join(' ')
				.toLowerCase();
		}
		const d = entry.denial;
		return [
			d.certificate_request_id,
			d.reported_username,
			d.reported_hostname,
			d.pam_service,
			d.tty,
			d.remote_host,
			d.client
		]
			.filter(Boolean)
			.join(' ')
			.toLowerCase();
	}

	// The same fields the admin list searches, minus the owner: on your own
	// history every row is yours, so searching by owner would match
	// everything or nothing.
	const needle = $derived(searchQuery.trim().toLowerCase());
	const filtered = $derived(
		needle === '' ? byStatus : byStatus.filter((entry) => haystack(entry).includes(needle))
	);

	// Reset to page 1 when either filter changes. Use void operator to
	// suppress the linter warning about an unused value.
	$effect(() => {
		void selectedType;
		void selectedOutcome;
		void selectedStatus;
		void needle;
		currentPage = 1;
	});

	const paginated = $derived(filtered.slice((currentPage - 1) * pageSize, currentPage * pageSize));

	const totalPages = $derived(Math.ceil(filtered.length / pageSize));

	// A row opens the certificate's own page rather than a dialog over the
	// list. `from` is what the back chip there reads: this list, not the
	// reader's own history, is where they came from.
	function certHref(certId: string): string {
		return `${resolve(`/certs/${certId}`)}?from=history`;
	}

	// True while either source has more to give. One button loads both, so a
	// reader is never told there is no more history while half of it is
	// still unfetched.
	const hasMore = $derived(nextCursor !== null || nextDenialCursor !== null);

	async function loadMore() {
		if (isLoading || !hasMore) {
			return;
		}
		isLoading = true;
		const controller = new AbortController();
		try {
			// Both pages in flight together: they are independent reads, and
			// awaiting them in turn would double the wait for one button.
			const [certs, denials] = await Promise.all([
				nextCursor ? listCertificates(controller.signal, nextCursor, 25) : null,
				nextDenialCursor ? listDeniedRequests(controller.signal, nextDenialCursor, 25) : null
			]);
			if (certs) {
				allCertificates = [...allCertificates, ...certs.certificates];
				nextCursor = certs.next_cursor ?? null;
			}
			if (denials) {
				allDenials = [...allDenials, ...(denials.denials ?? [])];
				nextDenialCursor = denials.next_cursor ?? null;
			}
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

		Promise.all([
			listCertificates(controller.signal, null, 25),
			listDeniedRequests(controller.signal, null, 25)
		])
			.then(
				([certs, denials]: [
					CertificateListResponse,
					{ denials?: DeniedRequest[]; next_cursor?: string }
				]) => {
					// Both arrays are guarded: an unexpected response shape
					// must render as "nothing of that kind" rather than take
					// down a page whose other half loaded fine.
					allCertificates = Array.isArray(certs?.certificates) ? certs.certificates : [];
					nextCursor = certs?.next_cursor ?? null;
					allDenials = Array.isArray(denials?.denials) ? denials.denials : [];
					nextDenialCursor = denials?.next_cursor ?? null;
					hasLoaded = true;
				}
			)
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

<PageShell width="wide">
	<PageHeading eyebrow="History" title="Certificate history" />

	{#if loadError}
		<Alert variant="error" title="Could not load your history">{loadError}</Alert>
	{:else if !hasLoaded}
		<p class="text-sm text-ink-muted">Loading…</p>
	{:else if sorted.length === 0}
		<p class="text-sm text-ink-muted">You have not decided any certificate requests yet.</p>
	{:else}
		<!-- Search, then the filter groups on one line. The admin
		     certificate list opens exactly the same way; see $lib/filters. -->
		<div class="flex flex-col gap-3">
			<SearchInput
				label="Search your history"
				placeholder="Key ID, principal, serial, fingerprint, host"
				value={searchQuery}
				onsearch={(term) => (searchQuery = term)}
				testid="search-input"
			/>

			<div class="flex flex-wrap items-center gap-x-5 gap-y-2">
				<FilterGroup
					label="Outcome"
					options={outcomeFilters}
					selected={selectedOutcome}
					onselect={(value) => (selectedOutcome = value)}
					testid="outcome-filter"
				/>
				<FilterGroup
					label="Type"
					options={typeFilters}
					selected={selectedType}
					onselect={(value) => (selectedType = value)}
					testid="type-filter"
				/>
				<FilterGroup
					label="Status"
					options={statusFilters}
					selected={selectedStatus}
					onselect={(value) => (selectedStatus = value)}
					testid="status-filter"
				/>
			</div>
		</div>

		{#if filtered.length === 0}
			<p class="text-sm text-ink-muted">Nothing in your history matches the selected filter.</p>
		{:else}
			<div class="flex flex-col gap-2.5">
				<!-- Keyed on the kind as well as the id: a certificate and the
				     decision that refused a different request are separate
				     tables with separate id spaces, and nothing stops one from
				     colliding with the other. -->
				{#each paginated as entry (entry.kind + ':' + (entry.kind === 'certificate' ? entry.cert.id : entry.denial.id))}
					{#if entry.kind === 'certificate'}
						<CertRow
							cert={entry.cert}
							{now}
							event={rowEvents[entry.cert.type] ?? 'certificate requested'}
							testid="cert-row"
							href={certHref(entry.cert.id)}
						/>
					{:else}
						<DeniedRow denial={entry.denial} {now} testid="denied-row" />
					{/if}
				{/each}
			</div>

			{#if totalPages > 1 || hasMore}
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

				{#if hasMore}
					<div class="flex justify-center">
						<Button variant="ghost" busy={isLoading} onclick={loadMore}>
							{isLoading ? 'Loading…' : 'Load more results'}
						</Button>
					</div>
				{/if}
			{/if}
		{/if}
	{/if}
</PageShell>
