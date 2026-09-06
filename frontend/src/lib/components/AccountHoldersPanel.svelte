<script lang="ts">
	import { ApiError } from '$lib/api/client';
	import { listEnrollmentHolders } from '$lib/api/endpoints';
	import type { AccountHolder } from '$lib/api/types';
	import SectionLabel from './SectionLabel.svelte';

	// Who else can use and manage one enrollment code.
	//
	// A code belongs to its service account rather than to whoever approved
	// it (see docs/proposals/enrollment-group-ownership.md), so the row
	// itself cannot answer "who else has this" — the approver's name is
	// provenance, not ownership. This is that answer, and it is the same
	// panel on the holder's page and the admin's, because it is the same
	// fact.
	//
	// Loaded here rather than passed in: the list is one query per opened
	// code, and it would be N queries on a list that never shows it.
	interface Props {
		/** The enrollment whose account's holders to resolve. */
		enrollmentId: string;
		/** The account itself, shown as the subject of the sentence. */
		serviceAccount?: string;
		/** Highlighted as "you" — the viewer's own username, when known. */
		viewerUsername?: string;
	}

	let { enrollmentId, serviceAccount = '', viewerUsername = '' }: Props = $props();

	let holders = $state<AccountHolder[] | null>(null);
	let loadError = $state<string | null>(null);

	$effect(() => {
		const controller = new AbortController();
		const id = enrollmentId;
		holders = null;
		loadError = null;

		listEnrollmentHolders(id, controller.signal)
			.then((result) => {
				// Defensive: an unexpected response shape must render as
				// "nobody listed" rather than tearing down the panel this
				// section sits in, which carries everything else about the
				// code.
				holders = Array.isArray(result?.holders) ? result.holders : [];
			})
			.catch((cause) => {
				if (controller.signal.aborted) {
					return;
				}
				// A holder list that fails to load must not take the panel
				// it sits in with it: everything else on the screen is still
				// worth reading.
				loadError =
					cause instanceof ApiError && cause.status === 403
						? 'You do not have permission to see who holds this account.'
						: 'Could not load who holds this account.';
			});

		return () => controller.abort();
	});

	/** label names one holder in the terms the reader thinks in. */
	function label(holder: AccountHolder): string {
		return holder.name || holder.username;
	}
</script>

<div data-testid="account-holders">
	<SectionLabel>Who has access</SectionLabel>

	<p class="mb-2 text-[13px] text-ink-muted">
		Everyone holding
		{#if serviceAccount}<span class="font-mono">{serviceAccount}</span>{:else}this service account{/if}
		can see and manage this code, whoever approved it. Only people who have signed in at least once are
		listed: the server never reads a directory to find the rest.
	</p>

	{#if loadError}
		<p class="text-[13px] text-ink-muted" data-testid="account-holders-error">{loadError}</p>
	{:else if holders === null}
		<p class="text-[13px] text-ink-muted">Loading…</p>
	{:else if holders.length === 0}
		<p class="text-[13px] text-ink-muted" data-testid="account-holders-empty">
			Nobody who has signed in holds this account. Notifications about this code reach nobody unless
			an address is set below.
		</p>
	{:else}
		<dl class="divide-y divide-border-subtle">
			{#each holders as holder (holder.user_id)}
				<div class="flex items-start justify-between gap-3 py-2" data-testid="account-holder">
					<div class="min-w-0">
						<div class="flex flex-wrap items-baseline gap-x-2 text-[13px]">
							<span class="font-medium text-ink">{label(holder)}</span>
							{#if holder.name}
								<span class="font-mono text-[11px] text-ink-muted">{holder.username}</span>
							{/if}
							{#if viewerUsername && holder.username === viewerUsername}
								<span class="text-[11px] text-ink-muted">(you)</span>
							{/if}
						</div>
						{#if holder.email}
							<div class="text-[11px] text-ink-muted">{holder.email}</div>
						{/if}
					</div>
					<div class="flex flex-shrink-0 flex-col items-end gap-0.5 text-[11px]">
						{#if holder.disabled}
							<!-- Listed rather than dropped: the claim is still
							     on their row and comes back with the account,
							     so hiding them would answer "who has access"
							     with a set that quietly grows again later. -->
							<span class="font-semibold text-danger">Disabled</span>
						{/if}
						{#if holder.own}
							<span class="text-ink-muted">their own account</span>
						{/if}
					</div>
				</div>
			{/each}
		</dl>
	{/if}
</div>
