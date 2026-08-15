<script lang="ts">
	import { listCertificates } from '$lib/api/endpoints';
	import type { CertificateRecord } from '$lib/api/types';
	import { errorMessage, redirectIfUnauthenticated } from '$lib/auth';
	import Alert from '$lib/components/Alert.svelte';
	import Card from '$lib/components/Card.svelte';
	import { expiryLabel, formatDateTime, isExpired } from '$lib/format';

	// "Am I good until the end of the day?" is the question this page
	// answers, and the only one — there is deliberately no list of requests
	// awaiting approval. A request has no owner until someone opens it, so
	// such a list could only ever show everyone's, and approving a stranger's
	// request issues a certificate carrying *your* principals to *their* key.
	// The approval URL reaches a human one way: their own client prints it.
	let certificates = $state<CertificateRecord[] | null>(null);
	let loadError = $state<string | null>(null);

	// Recomputed against a clock that ticks, not against load time: this page
	// is the sort of thing that stays open in a tab, and a certificate that
	// expired twenty minutes ago should not still read as active.
	let now = $state(new Date());
	$effect(() => {
		const timer = setInterval(() => (now = new Date()), 30_000);
		return () => clearInterval(timer);
	});

	const active = $derived((certificates ?? []).filter((c) => !isExpired(c.expires_at, now)));

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

<svelte:head><title>Dashboard · ssoossh</title></svelte:head>

<div class="space-y-6">
	{#if loadError}
		<Alert variant="error" title="Could not load your certificates">{loadError}</Alert>
	{/if}

	<Card title="Active certificates" description="Issued to you and not yet expired.">
		{#if certificates === null}
			<p class="text-sm text-ink-muted">Loading…</p>
		{:else if active.length === 0}
			<p class="text-sm text-ink-muted">
				Nothing active right now. Run <code class="font-mono">ssoossh login</code> to request one.
			</p>
		{:else}
			<ul class="divide-y divide-border-subtle">
				{#each active as cert (cert.id)}
					<li class="py-3">
						<div class="flex flex-wrap items-baseline justify-between gap-2">
							<span class="font-mono text-sm">{cert.principals || cert.key_id}</span>
							<span class="text-sm font-medium text-granted">
								{expiryLabel(cert.expires_at, now)}
							</span>
						</div>
						<p class="mt-1 text-xs text-ink-muted">
							{cert.type} · issued {formatDateTime(cert.issued_at)} · expires {formatDateTime(
								cert.expires_at
							)}
						</p>
					</li>
				{/each}
			</ul>
		{/if}
	</Card>
</div>
