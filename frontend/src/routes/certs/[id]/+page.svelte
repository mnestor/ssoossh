<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { getCertificateDetail } from '$lib/api/endpoints';
	import type { CertificateResponse } from '$lib/api/types';
	import { ApiError } from '$lib/api/client';
	import { errorMessage, redirectIfUnauthenticated } from '$lib/auth';
	import Alert from '$lib/components/Alert.svelte';
	import Card from '$lib/components/Card.svelte';
	import DetailRow from '$lib/components/DetailRow.svelte';
	import Icon from '$lib/components/Icon.svelte';
	import MonoChip from '$lib/components/MonoChip.svelte';
	import PageHeading from '$lib/components/PageHeading.svelte';
	import StatusBadge from '$lib/components/StatusBadge.svelte';
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

	// A certificate exists because a request was approved; an absent decision
	// record means the audit trail predates it, not that it was denied.
	const decision = $derived(cert && cert.decided_by_outcome === 'denied' ? 'denied' : 'approved');
	const decidedBy = $derived(
		cert ? cert.decided_by_email || cert.decided_by_username || cert.decided_by_subject : null
	);
	const decidedByGroups = $derived(cert?.decided_by_groups ?? []);

	// What the certificate actually grants on the far side. Extensions are a
	// set of names; critical options are name/value pairs, and are listed
	// separately rather than folded together because sshd rejects a
	// certificate outright over a critical option it does not understand and
	// merely ignores an unknown extension.
	const extensions = $derived(cert?.extensions ?? []);
	const criticalOptions = $derived(Object.entries(cert?.critical_options ?? {}));
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
		<p class="text-sm text-ink-muted">Loading…</p>
	{:else if cert}
		<div data-testid="cert-details" class="flex flex-col gap-5">
			<!-- The identity strip: what kind of certificate this is, what
			     happened to the request behind it, and the id to quote in a
			     ticket. Wraps rather than squeezes, because the id is a full
			     uuid and the badges must not shrink to make room for it. -->
			<div
				class="flex flex-wrap items-center gap-x-3 gap-y-2 rounded-[10px] border border-border-subtle bg-surface-muted px-4 py-3"
			>
				<TypeChip type={cert.type} />
				<StatusBadge status={decision} />
				<span class="ml-auto font-mono text-xs break-all text-ink-muted">{cert.id}</span>
			</div>

			<Card title="Certificate" description="What this certificate carries, and how long for.">
				<dl class="divide-y divide-border-subtle">
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
			</Card>

			<Card
				title="What it grants"
				description="The extensions and critical options signed into the certificate."
				testid="cert-grants"
			>
				<dl class="divide-y divide-border-subtle">
					<DetailRow label="Extensions">
						{#if extensions.length > 0}
							<span class="flex flex-wrap gap-1.5">
								{#each extensions as extension (extension)}
									<MonoChip>{extension}</MonoChip>
								{/each}
							</span>
						{:else}
							<span class="text-ink-muted">None</span>
						{/if}
					</DetailRow>

					<!-- Stated even when empty: "no critical options" is a fact
					     about the certificate worth reading, not an absence. A
					     force-command that is not there is why an interactive
					     shell works. -->
					<DetailRow label="Critical options">
						{#if criticalOptions.length > 0}
							<span class="flex flex-col items-start gap-1.5">
								{#each criticalOptions as [name, value] (name)}
									<MonoChip>{name} <span class="text-ink-muted">=</span> {value}</MonoChip>
								{/each}
							</span>
						{:else}
							<span class="text-ink-muted">None</span>
						{/if}
					</DetailRow>
				</dl>
			</Card>

			{#if decidedBy}
				<!-- Who decided, from where, and when. The modal states this as a
				     sentence because it has one line to spare; the page has room
				     for the fields themselves, including the approver's groups —
				     the policy input that decided they were allowed to approve at
				     all, and which appears nowhere in the certificate. -->
				<Card
					title="Decision"
					description="The approval this certificate was issued against."
					testid="cert-decision"
				>
					<dl class="divide-y divide-border-subtle">
						<DetailRow label={decision === 'denied' ? 'Denied by' : 'Approved by'} icon="user">
							{decidedBy}
						</DetailRow>
						{#if cert.decided_source_ip}
							<DetailRow label="Source address" mono>{cert.decided_source_ip}</DetailRow>
						{/if}
						{#if cert.decided_at}
							<DetailRow label="Decided at">{formatDateTime(cert.decided_at)}</DetailRow>
						{/if}
						{#if decidedByGroups.length > 0}
							<DetailRow label="Approver groups">
								<span class="flex flex-wrap gap-1.5">
									{#each decidedByGroups as group (group)}
										<MonoChip>{group}</MonoChip>
									{/each}
								</span>
							</DetailRow>
						{/if}
					</dl>
				</Card>
			{/if}

			{#if cert.retrieved_source_ip}
				<!-- Only a service certificate has one. The address here is the
				     machine that ran `service retrieve`, which is a different
				     question from the decision above: that names the human who
				     approved the code, months earlier and from a browser, and is
				     identical on every certificate the code has ever minted. -->
				<Card
					title="Where it was fetched"
					description="The redemption of the service code that produced this certificate."
				>
					<dl class="divide-y divide-border-subtle">
						<DetailRow label="Source address" mono>{cert.retrieved_source_ip}</DetailRow>
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
				</Card>
			{/if}
		</div>
	{/if}
</div>
