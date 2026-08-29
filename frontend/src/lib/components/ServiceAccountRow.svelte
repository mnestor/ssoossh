<script lang="ts">
	import { expiryLabel, relativeTime } from '$lib/format';
	import Icon from './Icon.svelte';
	import TypeBadge from './TypeBadge.svelte';

	// One service account as a clickable card, the top level of the service
	// codes page. An account is what owns enrollments now — everyone holding
	// it owns every code approved for it — so the account, not the code, is
	// what the page is a list of.
	//
	// Shaped like ServiceCodeRow so the two levels read as one page.
	interface Props {
		account: string;
		/** Codes for this account that are still redeemable. */
		liveCount: number;
		/** Codes for this account that have expired. */
		expiredCount: number;
		/** Soonest expiry among the live codes, absent when none are live. */
		nextExpiry?: string;
		/** Most recent redemption across every code, absent if never redeemed. */
		lastRetrievedAt?: string;
		/** Pinned clock, so a list of rows agrees with itself and tests can fix it. */
		now?: Date;
		onclick: () => void;
		testid?: string;
	}

	let {
		account,
		liveCount,
		expiredCount,
		nextExpiry,
		lastRetrievedAt,
		now = new Date(),
		onclick,
		testid
	}: Props = $props();

	// An account with no live code is the case this level exists to make
	// visible: the unattended job behind it has nothing to renew with, and a
	// list of codes alone would simply not mention it.
	const codes = $derived(
		liveCount === 0
			? expiredCount === 0
				? 'no codes'
				: `no live codes · ${expiredCount} expired`
			: `${liveCount} live code${liveCount === 1 ? '' : 's'}${expiredCount > 0 ? ` · ${expiredCount} expired` : ''}`
	);

	// expiryLabel already reads "expires in 30d", so the soonest one needs
	// only the qualifier in front of it.
	const expiry = $derived(nextExpiry ? `soonest ${expiryLabel(nextExpiry, now)}` : '');

	const usage = $derived(
		lastRetrievedAt ? `last redeemed ${relativeTime(lastRetrievedAt, now)}` : 'never redeemed'
	);

	const detail = $derived([codes, expiry].filter(Boolean).join(' · '));
</script>

<button
	type="button"
	{onclick}
	data-testid={testid}
	class="flex w-full items-center justify-between gap-4 rounded-[10px] border border-border-subtle bg-surface px-5 py-3.5 text-left transition hover:bg-surface-muted focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
>
	<span class="flex min-w-0 items-center gap-3">
		<TypeBadge type="service" />
		<span class="min-w-0">
			<span class="block truncate font-mono text-[13px]">{account}</span>
			<span class="mt-0.5 block text-xs text-ink-muted">{detail}</span>
			<span class="mt-px block truncate text-xs text-ink-muted">{usage}</span>
		</span>
	</span>

	{#if liveCount === 0}
		<span
			class="inline-flex flex-shrink-0 items-center gap-1.5 rounded-full bg-surface-muted px-2.5 py-1 text-xs font-semibold text-ink-muted"
		>
			<Icon name="alert-triangle" size="xs" />
			No live code
		</span>
	{:else}
		<span
			class="inline-flex flex-shrink-0 items-center gap-1.5 rounded-full bg-granted-surface px-2.5 py-1 text-xs font-semibold text-granted"
		>
			<Icon name="check-circle" size="xs" />
			Active
		</span>
	{/if}
</button>
