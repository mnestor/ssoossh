<script lang="ts">
	import { pushState } from '$app/navigation';
	import { page } from '$app/state';
	import { listCertificates } from '$lib/api/endpoints';
	import type { CertificateListResponse, CertificateRecord } from '$lib/api/types';
	import { errorMessage, redirectIfUnauthenticated } from '$lib/auth';
	import Alert from '$lib/components/Alert.svelte';
	import Card from '$lib/components/Card.svelte';
	import CertDetailModal from '$lib/components/CertDetailModal.svelte';
	import Icon from '$lib/components/Icon.svelte';
	import { expiryLabel, formatDateTime, isExpired } from '$lib/format';

	// "Am I good until the end of the day?" is the question this page
	// answers, and the only one — there is deliberately no list of requests
	// awaiting approval. A request has no owner until someone opens it, so
	// such a list could only ever show everyone's, and approving a stranger's
	// request issues a certificate carrying *your* principals to *their* key.
	// The approval URL reaches a human one way: their own client prints it.
	let allCertificates = $state<CertificateRecord[]>([]);
	let nextCursor = $state<string | null>(null);
	let loadError = $state<string | null>(null);
	let isLoading = $state(false);

	// Recomputed against a clock that ticks, not against load time: this page
	// is the sort of thing that stays open in a tab, and a certificate that
	// expired twenty minutes ago should not still read as active.
	let now = $state(new Date());
	$effect(() => {
		const timer = setInterval(() => (now = new Date()), 30_000);
		return () => clearInterval(timer);
	});

	const active = $derived(allCertificates.filter((c) => !isExpired(c.expires_at, now)));

	// Shallow routing for cert detail modal
	const modalCertId = $derived(page.url.searchParams.get('modal'));
	const modalCert = $derived(active.find((c) => c.id === modalCertId));

	// Icon mapping for certificate types
	const certTypeIcons: Record<string, string> = {
		user: 'user',
		pam: 'terminal',
		service: 'cog',
		host: 'server'
	};

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
		{#if allCertificates.length === 0}
			<p class="text-sm text-ink-muted">Loading…</p>
		{:else if active.length === 0}
			<p class="text-sm text-ink-muted">
				Nothing active right now. Run <code class="font-mono">ssoossh login</code> to request one.
			</p>
		{:else}
			<ul class="divide-y divide-border-subtle">
				{#each active as cert (cert.id)}
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
								<span class="text-sm font-medium text-granted">
									{expiryLabel(cert.expires_at, now)}
								</span>
							</div>
							<p class="mt-2 text-xs text-ink-muted">
								issued {formatDateTime(cert.issued_at)}
							</p>
						</button>
					</li>
				{/each}
			</ul>
		{/if}
	</Card>

	{#if nextCursor}
		<div class="flex justify-center">
			<button
				onclick={loadMoreCertificates}
				disabled={isLoading}
				class="rounded-md border border-border-subtle px-4 py-2 transition hover:bg-surface-muted disabled:cursor-not-allowed disabled:opacity-50"
			>
				{isLoading ? 'Loading…' : 'Load more'}
			</button>
		</div>
	{/if}

	{#if modalCert}
		<CertDetailModal cert={modalCert} onclosed={closeCertDetail} />
	{/if}
</div>
