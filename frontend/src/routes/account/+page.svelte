<script lang="ts">
	import { getCurrentUser } from '$lib/api/endpoints';
	import type { CurrentUser } from '$lib/api/types';
	import { errorMessage, redirectIfUnauthenticated } from '$lib/auth';
	import Alert from '$lib/components/Alert.svelte';
	import Card from '$lib/components/Card.svelte';
	import DetailRow from '$lib/components/DetailRow.svelte';
	import MonoChip from '$lib/components/MonoChip.svelte';
	import PageHeading from '$lib/components/PageHeading.svelte';
	import SectionLabel from '$lib/components/SectionLabel.svelte';

	// Fetched here rather than read from the app-wide session store: this
	// page's whole content is the identity, so it should reflect what the
	// server says right now — and the fetch is what produces the 401 that
	// bounces a signed-out visitor to login, the same shape as every other
	// authenticated page.
	let user = $state<CurrentUser | null>(null);
	let loadError = $state<string | null>(null);
	let hasLoaded = $state(false);

	$effect(() => {
		const controller = new AbortController();

		getCurrentUser(controller.signal)
			.then((result) => {
				user = result;
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

<svelte:head><title>Account · ssoossh</title></svelte:head>

<div class="flex w-full max-w-[680px] flex-col gap-5">
	<PageHeading eyebrow="Account" title="Your account" />

	{#if loadError}
		<Alert variant="error" title="Could not load your account">{loadError}</Alert>
	{:else if !hasLoaded}
		<p class="text-sm text-ink-muted">Loading…</p>
	{:else if user}
		<Card title="Identity" description="Who the server sees this session as.">
			<dl>
				<DetailRow label="Username" mono icon="user">{user.username}</DetailRow>
				<DetailRow label="Email">{user.email || '—'}</DetailRow>
				<DetailRow label="Subject" mono>{user.subject}</DetailRow>
				{#if user.extra}
					{#each Object.entries(user.extra) as [name, value] (name)}
						<DetailRow label={name} mono={typeof value === 'string'}>
							{#if Array.isArray(value)}
								<span class="flex flex-wrap gap-1.5">
									{#each value as v (v)}
										<MonoChip>{v}</MonoChip>
									{/each}
								</span>
							{:else if value === '' || value === null}
								<!-- Missing extra field: display it visibly so operators can debug missing claims -->
								<span class="text-ink-muted">MISSING</span>
							{:else}
								{value}
							{/if}
						</DetailRow>
					{/each}
				{/if}
				{#if user.is_auditor}
					<DetailRow label="Access">
						<span
							class="inline-flex items-center gap-1.5 rounded-full bg-granted-surface px-2.5 py-1 text-xs font-semibold text-granted"
						>
							Auditor
						</span>
					</DetailRow>
				{/if}
			</dl>
		</Card>

		<Card
			title="Accounts you can mint certificates for"
			description="The principals the server will put in certificates issued to or approved by you."
		>
			<div class="flex flex-col gap-5">
				<div>
					<SectionLabel>Principals for user certificates</SectionLabel>
					<p class="mb-2 text-[13px] text-ink-muted">
						Your username and any alternate account names you can use as principals. Your username
						is the primary identity.
					</p>
					<span class="flex flex-wrap gap-1.5">
						<MonoChip>{user.username} <span class="text-ink-muted">(primary)</span></MonoChip>
						{#each user.other_accounts as account (account)}
							<MonoChip>{account}</MonoChip>
						{/each}
					</span>
					{#if user.other_accounts.length === 0}
						<p class="mt-2 text-[13px] text-ink-muted">
							Only your primary username is available; no alternate account names are linked.
						</p>
					{/if}
				</div>

				<div>
					<SectionLabel>Service accounts</SectionLabel>
					{#if user.service_accounts.length === 0}
						<p class="text-[13px] text-ink-muted">
							No service accounts are linked to your identity, so you cannot approve service
							certificates.
						</p>
					{:else}
						<p class="mb-2 text-[13px] text-ink-muted">
							You can approve service certificates for these accounts; the one you pick becomes the
							certificate's principal.
						</p>
						<span class="flex flex-wrap gap-1.5">
							{#each user.service_accounts as account (account)}
								<MonoChip>{account}</MonoChip>
							{/each}
						</span>
					{/if}
				</div>
			</div>
		</Card>

		<Card
			title="Groups"
			description="Group membership feeds certificate policy (approval eligibility and lifetime) but never appears in a certificate."
		>
			{#if user.groups.length === 0}
				<p class="text-[13px] text-ink-muted">Your identity carries no groups.</p>
			{:else}
				<span class="flex flex-wrap gap-1.5">
					{#each user.groups as group (group)}
						<MonoChip>{group}</MonoChip>
					{/each}
				</span>
			{/if}
		</Card>
	{/if}
</div>
