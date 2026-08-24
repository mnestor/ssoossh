<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { getCertificateDetail } from '$lib/api/endpoints';
	import type { CertificateResponse } from '$lib/api/types';
	import { ApiError } from '$lib/api/client';
	import { errorMessage, redirectIfUnauthenticated } from '$lib/auth';
	import Alert from '$lib/components/Alert.svelte';
	import DetailRow from '$lib/components/DetailRow.svelte';
	import Icon from '$lib/components/Icon.svelte';
	import MonoChip from '$lib/components/MonoChip.svelte';
	import PageHeading from '$lib/components/PageHeading.svelte';
	import SectionLabel from '$lib/components/SectionLabel.svelte';
	import TypeChip from '$lib/components/TypeChip.svelte';
	import { formatDateTime, formatDuration } from '$lib/format';

	const id = $derived(page.params.id ?? '');

	let cert = $state<CertificateResponse | null>(null);
	let loadError = $state<string | null>(null);
	let isAccessDenied = $state(false);
	let hasLoaded = $state(false);

	$effect(() => {
		const controller = new AbortController();

		getCertificateDetail(id, controller.signal)
			.then((result: CertificateResponse) => {
				cert = result;
				hasLoaded = true;
			})
			.catch((cause) => {
				if (controller.signal.aborted || redirectIfUnauthenticated(cause)) {
					return;
				}
				if (cause instanceof ApiError && cause.isNotFound) {
					isAccessDenied = true;
				} else {
					loadError = errorMessage(cause);
				}
				hasLoaded = true;
			});

		return () => controller.abort();
	});

	const principals = $derived(
		cert && cert.principals
			? cert.principals
					.split(',')
					.map((p) => p.trim())
					.filter((p) => p.length > 0)
			: []
	);

	const validFor = $derived(
		cert
			? Math.floor(
					(new Date(cert.expires_at).getTime() - new Date(cert.issued_at).getTime()) / 1000
				)
			: 0
	);

	const decision = $derived(cert && cert.decided_by_outcome === 'denied' ? 'denied' : 'approved');
	const decidedBy = $derived(
		cert ? cert.decided_by_email || cert.decided_by_username || cert.decided_by_subject : null
	);
</script>

<svelte:head><title>Certificate · ssoossh</title></svelte:head>

<div class="flex w-full max-w-[600px] flex-col gap-5">
	<PageHeading eyebrow="Certificate" title="Details" />

	{#if loadError}
		<Alert variant="error" title="Could not load certificate">{loadError}</Alert>
	{:else if isAccessDenied}
		<div data-testid="access-denied">
			<Alert variant="error" title="Access denied">
				You don't have permission to view this certificate. It may not exist or you may not have the
				required permissions.
			</Alert>
		</div>
	{:else if !hasLoaded}
		<div class="flex flex-col gap-5">
			<p class="text-sm text-ink-muted">Loading…</p>
		</div>
	{:else if cert}
		<div data-testid="cert-details" class="flex flex-col gap-5">
			<div class="flex items-center justify-between gap-3 rounded-lg bg-surface-muted px-4 py-3">
				<TypeChip type={cert.type} />
				<span class="text-xs text-ink-muted font-mono">{cert.id}</span>
			</div>

			{#if decidedBy}
				<div
					class="flex gap-2.5 rounded-lg bg-surface-muted px-3.5 py-3 text-[13px] leading-normal"
				>
					<Icon name="user" size="sm" class="mt-px flex-shrink-0 text-ink-muted" />
					<span>
						{decision === 'denied' ? 'Denied' : 'Approved'} by <strong>{decidedBy}</strong>
						{#if cert.decided_source_ip}
							from <span class="font-mono">{cert.decided_source_ip}</span>
						{/if}
						{#if cert.decided_at}
							· {formatDateTime(cert.decided_at)}
						{/if}
					</span>
				</div>
			{/if}

			<div>
				<SectionLabel>Certificate Details</SectionLabel>
				<dl class="divide-y divide-border-subtle rounded-lg border border-border-subtle">
					<DetailRow label="Type" mono>{cert.type}</DetailRow>

					{#if principals.length > 0}
						<DetailRow label="Principals">
							<span class="flex flex-wrap gap-1.5">
								{#each principals as principal, index (index)}
									<MonoChip>{principal}</MonoChip>
								{/each}
							</span>
						</DetailRow>
					{/if}

					<DetailRow label="Key ID" mono>
						<span data-testid="cert-key-id">{cert.key_id}</span>
					</DetailRow>
					<DetailRow label="Serial number" mono>
						<span data-testid="cert-serial-number">{cert.serial_number}</span>
					</DetailRow>
					<DetailRow label="Key fingerprint" mono>{cert.public_key_fingerprint}</DetailRow>

					{#if validFor > 0}
						<DetailRow label="Valid for">{formatDuration(validFor)}</DetailRow>
					{/if}

					<DetailRow label="Issued at">{formatDateTime(cert.issued_at)}</DetailRow>
					<DetailRow label="Expires at">{formatDateTime(cert.expires_at)}</DetailRow>
				</dl>
			</div>

			{#if cert.retrieved_source_ip}
				<div>
					<SectionLabel>Service Certificate Info</SectionLabel>
					<dl class="divide-y divide-border-subtle rounded-lg border border-border-subtle">
						<DetailRow label="Retrieved from" mono>{cert.retrieved_source_ip}</DetailRow>
						{#if cert.retrieved_at}
							<DetailRow label="Retrieved at">{formatDateTime(cert.retrieved_at)}</DetailRow>
						{/if}
						{#if cert.enrollment_id}
							<DetailRow label="Service code">
								<a
									href="{resolve('/service-codes')}?modal={cert.enrollment_id}"
									class="inline-flex items-center gap-1.5 text-accent underline-offset-2 hover:underline"
								>
									View the code this came from
									<Icon name="arrow-right" size="xs" />
								</a>
							</DetailRow>
						{/if}
					</dl>
				</div>
			{/if}
		</div>
	{/if}
</div>
