<script lang="ts">
	import { listCertificates } from '$lib/api/endpoints';
	import type { CertificateRecord } from '$lib/api/types';
	import { errorMessage, redirectIfUnauthenticated } from '$lib/auth';
	import Alert from '$lib/components/Alert.svelte';
	import Card from '$lib/components/Card.svelte';
	import { formatDateTime, isExpired } from '$lib/format';

	// Every certificate ever issued to this identity, newest first. This is
	// the audit trail: GET /api/certs is scoped to the caller by the service
	// (CertificateService.ListForIdentity) with no parameter to widen it, so
	// there is nothing here to filter by user.
	let certificates = $state<CertificateRecord[] | null>(null);
	let loadError = $state<string | null>(null);

	const rows = $derived(
		[...(certificates ?? [])].sort(
			(a, b) => new Date(b.issued_at).getTime() - new Date(a.issued_at).getTime()
		)
	);

	$effect(() => {
		const controller = new AbortController();

		listCertificates(controller.signal)
			.then((certs) => {
				certificates = certs;
			})
			.catch((cause) => {
				if (controller.signal.aborted || redirectIfUnauthenticated(cause)) {
					return;
				}
				loadError = errorMessage(cause);
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
	{:else if certificates === null}
		<p class="text-sm text-ink-muted">Loading…</p>
	{:else if rows.length === 0}
		<p class="text-sm text-ink-muted">No certificates have been issued to you yet.</p>
	{:else}
		<!-- Scrolls on its own rather than widening the page: the serial and
		     fingerprint columns are wide and not worth truncating. -->
		<div class="overflow-x-auto">
			<table class="w-full text-left text-sm">
				<thead class="border-b border-border-subtle text-ink-muted">
					<tr>
						<th class="py-2 pr-4 font-medium">Issued</th>
						<th class="py-2 pr-4 font-medium">Type</th>
						<th class="py-2 pr-4 font-medium">Principals</th>
						<th class="py-2 pr-4 font-medium">Serial</th>
						<th class="py-2 pr-4 font-medium">Key fingerprint</th>
						<th class="py-2 font-medium">Expired</th>
					</tr>
				</thead>
				<tbody class="divide-y divide-border-subtle">
					{#each rows as cert (cert.id)}
						<tr>
							<td class="py-2 pr-4 whitespace-nowrap">{formatDateTime(cert.issued_at)}</td>
							<td class="py-2 pr-4">{cert.type}</td>
							<td class="py-2 pr-4 font-mono">{cert.principals}</td>
							<td class="py-2 pr-4 font-mono">{cert.serial_number}</td>
							<td class="py-2 pr-4 font-mono break-all">{cert.public_key_fingerprint}</td>
							<td class="py-2 whitespace-nowrap">
								{isExpired(cert.expires_at) ? formatDateTime(cert.expires_at) : 'still valid'}
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/if}
</Card>
