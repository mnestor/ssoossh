<script lang="ts">
	import { expiryLabel, relativeTime } from '#lib/format.js';
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
		/** The account's own address (`?account=`), so the row is a real
		 *  link: middle-click and "copy link address" work as on a code row. */
		href: string;
		/** Opens the account in place; a plain click only, so a modified
		 *  click still opens the link the browser's way. */
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
		href,
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

	// The expiry reminder mail goes out a week ahead by default
	// (mail.expiry_reminder_lead), and the row says the same thing then:
	// the job about to break should stand out before it breaks.
	const soonMs = 7 * 24 * 60 * 60 * 1000;
	const expiringSoon = $derived(
		liveCount > 0 && !!nextExpiry && new Date(nextExpiry).getTime() - now.getTime() < soonMs
	);

	// No live code on an account that had codes is the job that has
	// stopped, the state this list exists to surface, so it reads as a
	// warning. An account that never had one may simply be unused.
	const state = $derived(
		liveCount === 0
			? expiredCount > 0
				? { label: 'No live code', tone: 'warn', icon: 'alert-triangle' }
				: { label: 'No codes', tone: 'muted', icon: 'key' }
			: expiringSoon
				? { label: 'Expires soon', tone: 'warn', icon: 'alert-triangle' }
				: { label: 'Active', tone: 'ok', icon: 'circle-check' }
	);

	/** follow opens the account in place for a plain click, and leaves a
	 *  modified one (new tab, new window) to the browser. */
	function follow(event: MouseEvent) {
		if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) {
			return;
		}
		event.preventDefault();
		onclick();
	}
</script>

<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- a query on this same page -->
<a
	{href}
	onclick={follow}
	data-testid={testid}
	class="flex w-full items-center justify-between gap-4 rounded-lg bg-surface px-4 py-3 text-left text-ink no-underline transition hover:bg-surface-muted focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
>
	<span class="flex min-w-0 items-center gap-3">
		<TypeBadge type="service" />
		<span class="min-w-0">
			<span class="block truncate font-mono text-dense">{account}</span>
			<span class="mt-0.5 block text-xs {expiringSoon ? 'text-trimmed' : 'text-ink-muted'}"
				>{detail}</span
			>
			<span class="mt-px block truncate text-xs text-ink-muted">{usage}</span>
		</span>
	</span>
	<span
		class="inline-flex flex-shrink-0 items-center gap-1.5 rounded px-2.5 py-1 text-xs font-semibold {state.tone ===
		'warn'
			? 'bg-trimmed-surface text-trimmed'
			: state.tone === 'ok'
				? 'bg-granted-surface text-granted'
				: 'bg-surface-muted text-ink-muted'}"
		data-testid="service-account-state"
	>
		<Icon name={state.icon} size="xs" />
		{state.label}
	</span>
</a>
