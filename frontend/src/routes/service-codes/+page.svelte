<script lang="ts">
	import { pushState } from '$app/navigation';
	import { page } from '$app/state';
	import { listServiceEnrollments } from '$lib/api/endpoints';
	import type { ServiceEnrollment } from '$lib/api/types';
	import { errorMessage, redirectIfUnauthenticated } from '$lib/auth';
	import Alert from '$lib/components/Alert.svelte';
	import PageHeading from '$lib/components/PageHeading.svelte';
	import SectionLabel from '$lib/components/SectionLabel.svelte';
	import ServiceCodeDetailModal from '$lib/components/ServiceCodeDetailModal.svelte';
	import ServiceCodeRow from '$lib/components/ServiceCodeRow.svelte';
	import { isExpired } from '$lib/format';

	// The codes an operator approved for unattended jobs, listed the way the
	// certificate history is: a stack of rows, each opening into its own
	// panel. The codes themselves are never part of it — `service enroll`
	// prints one once and the server keeps it only to match a redemption
	// against, so what a row can answer is what the code grants, what it is
	// bound to, and how long it has left.
	let enrollments = $state<ServiceEnrollment[]>([]);
	let loadError = $state<string | null>(null);
	let hasLoaded = $state(false);

	// Rows say how long ago something was and how long a code has left, so
	// they need a clock that moves.
	let now = $state(new Date());
	$effect(() => {
		const timer = setInterval(() => (now = new Date()), 30_000);
		return () => clearInterval(timer);
	});

	// Expired codes stay on the page under their own heading rather than
	// dropping off it: a job that stopped working is explained by the code
	// beneath it, and hiding the row hides the explanation.
	const live = $derived(enrollments.filter((e) => !isExpired(e.expires_at, now)));
	const dead = $derived(enrollments.filter((e) => isExpired(e.expires_at, now)));

	// Shallow routing keeps the open code in page.state; the search parameter
	// is what a pasted link arrives with, and is the fallback until something
	// on this page opens or closes a panel — the same arrangement as the
	// history page's certificate modal.
	const modalEnrollmentId = $derived(
		'modalEnrollmentId' in page.state
			? page.state.modalEnrollmentId
			: page.url.searchParams.get('modal')
	);

	const modalEnrollment = $derived(enrollments.find((e) => e.id === modalEnrollmentId));

	// Shallow-route within this same page (a modal query parameter), not a
	// navigation to a different route id — resolve() is for the latter, so it
	// does not apply here.
	function openDetail(id: string) {
		const url = new URL(page.url);
		url.searchParams.set('modal', id);
		// eslint-disable-next-line svelte/no-navigation-without-resolve
		pushState(url, { modalEnrollmentId: id });
	}

	// Closing records an explicit null rather than an empty state: an absent
	// modalEnrollmentId means "nothing has been opened or closed here yet",
	// which falls back to the search parameter — and on a page reached by a
	// pasted ?modal= link, that would reopen the panel the moment it closed.
	function closeDetail() {
		const url = new URL(page.url);
		url.searchParams.delete('modal');
		// eslint-disable-next-line svelte/no-navigation-without-resolve
		pushState(url, { modalEnrollmentId: null });
	}

	$effect(() => {
		const controller = new AbortController();

		listServiceEnrollments(controller.signal)
			.then((result) => {
				enrollments = result.enrollments;
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

<svelte:head><title>Service codes · ssoossh</title></svelte:head>

<div class="flex w-full max-w-[680px] flex-col gap-5">
	<PageHeading eyebrow="Service" title="Service enrollment codes" testid="service-codes-heading" />

	<p class="text-sm text-ink-muted">
		The codes you have approved for unattended certificate issuance. The codes themselves are not
		shown: <code class="font-mono">ssoossh service enroll</code> prints each one once, and the server
		keeps it only to match a redemption against. Open a row for what it hands out and how long it stays
		redeemable.
	</p>

	{#if loadError}
		<Alert variant="error" title="Could not load your service codes">{loadError}</Alert>
	{:else if !hasLoaded}
		<p class="text-sm text-ink-muted">Loading…</p>
	{:else if enrollments.length === 0}
		<p class="text-sm text-ink-muted">
			You have not approved any service enrollments yet. One is created when you approve a request
			from <code class="font-mono">ssoossh service enroll</code>.
		</p>
	{:else}
		{#if live.length > 0}
			<div class="flex flex-col gap-2.5">
				{#each live as enrollment (enrollment.id)}
					<ServiceCodeRow {enrollment} {now} onclick={() => openDetail(enrollment.id)} />
				{/each}
			</div>
		{/if}

		{#if dead.length > 0}
			<div class="flex flex-col gap-2.5">
				<SectionLabel>Expired codes</SectionLabel>
				{#each dead as enrollment (enrollment.id)}
					<ServiceCodeRow {enrollment} {now} onclick={() => openDetail(enrollment.id)} />
				{/each}
			</div>
		{/if}
	{/if}

	{#if modalEnrollment}
		<ServiceCodeDetailModal enrollment={modalEnrollment} {now} onclosed={closeDetail} />
	{/if}
</div>
