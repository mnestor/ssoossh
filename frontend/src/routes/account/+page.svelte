<script lang="ts">
	import { getCurrentUser } from '$lib/api/endpoints';
	import type { CurrentUser } from '$lib/api/types';
	import { errorMessage, redirectIfUnauthenticated } from '$lib/auth';
	import Alert from '$lib/components/Alert.svelte';
	import DetailRow from '$lib/components/DetailRow.svelte';
	import MonoChip from '$lib/components/MonoChip.svelte';
	import PageHeading from '$lib/components/PageHeading.svelte';
	import PageSection from '$lib/components/PageSection.svelte';
	import PageShell from '$lib/components/PageShell.svelte';

	// Fetched here rather than read from the app-wide session store: this
	// page's whole content is the identity, so it should reflect what the
	// server says right now — and the fetch is what produces the 401 that
	// bounces a signed-out visitor to login, the same shape as every other
	// authenticated page.
	let user = $state<CurrentUser | null>(null);
	let loadError = $state<string | null>(null);
	let hasLoaded = $state(false);

	// Every access level this session holds, widest first. The roles nest —
	// an admin holds SOC and auditor too — and the page names all of them
	// rather than only the narrowest, so an admin is not told "Auditor" and
	// left unable to tell that from an account that really is auditor-only.
	const roles = $derived(
		[
			user?.is_admin ? 'Admin' : null,
			user?.is_soc ? 'SOC' : null,
			user?.is_auditor ? 'Auditor' : null
		].filter((role): role is string => role !== null)
	);

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

<PageShell width="wide">
	<PageHeading eyebrow="Account" title="Your account" />

	{#if loadError}
		<Alert variant="error" title="Could not load your account">{loadError}</Alert>
	{:else if !hasLoaded}
		<p class="text-sm text-ink-muted">Loading…</p>
	{:else if user}
		<PageSection
			title="Identity"
			description="Who the server sees this session as."
			testid="account-identity-card"
		>
			<!-- Ruled between rows like every other field list. It was the one
			     that was not, which mattered less at 760px than it does now
			     the column is 1120. -->
			<dl data-testid="identity-fields" class="divide-y divide-border-subtle">
				{#if user.name}
					<DetailRow label="Name" icon="user">{user.name}</DetailRow>
				{/if}
				<DetailRow label="Username" mono icon="user">{user.username}</DetailRow>
				<DetailRow label="Email">{user.email || '—'}</DetailRow>
				<!-- The identifier the whole account is keyed by, from the claim
				     authentication.fields.subject names. Not the username: that
				     changes when someone is renamed. -->
				<DetailRow label="Account identifier" mono>{user.subject}</DetailRow>
				{#if user.extra}
					{#each Object.entries(user.extra) as [name, value] (name)}
						<div data-testid="extra-field-{name}">
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
						</div>
					{/each}
				{/if}
				{#if roles.length > 0}
					<DetailRow label="Access">
						<span class="flex flex-wrap gap-1.5" data-testid="access-roles">
							{#each roles as role (role)}
								<span
									class="inline-flex items-center gap-1.5 rounded-full bg-granted-surface px-2.5 py-1 text-xs font-semibold text-granted"
								>
									{role}
								</span>
							{/each}
						</span>
					</DetailRow>
				{/if}
			</dl>
		</PageSection>

		<!-- Flat, rather than the two nested labels this used to carry inside
		     an "Accounts you can mint certificates for" box. Without the box
		     there is no group to name, and the wrapper's own sentence said
		     nothing the two sections below do not each say for themselves. -->
		<PageSection
			title="Principals for user certificates"
			description="Your username and any alternate account names you can use as principals. Your username is the primary identity."
			testid="principals-section"
		>
			<span class="flex flex-wrap gap-1.5" data-testid="principals-list">
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
		</PageSection>

		<PageSection title="Service accounts">
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
		</PageSection>

		<PageSection
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
		</PageSection>
	{/if}
</PageShell>
