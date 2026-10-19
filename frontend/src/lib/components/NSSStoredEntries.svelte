<script lang="ts">
	import { resolve } from '$app/paths';

	import { ApiError } from '#lib/api/client.js';
	import { listNSSAccounts, listNSSGroups } from '#lib/api/endpoints.js';
	import { errorMessage, redirectIfUnauthenticated } from '#lib/auth.js';
	import Alert from './Alert.svelte';
	import LoadingBlock from './LoadingBlock.svelte';
	import PageSection from './PageSection.svelte';

	// The Directory page's way into the NSS directory viewer: how many
	// accounts and groups the server has stored, with a link to each list.
	// The counts are each list's total, read with one row apiece.
	//
	// It loads, fails and retries on its own, so a viewer that cannot answer
	// leaves the sync, the probe and the NSS panels around it standing. A 404
	// means NSS went off after the page decided to show it, as for the panels.

	interface Counts {
		accounts: number;
		groups: number;
	}

	let counts = $state<Counts | null>(null);
	let error = $state<string | null>(null);
	let gone = $state(false);
	let attempt = $state(0);

	$effect(() => {
		void attempt;
		const controller = new AbortController();
		Promise.all([
			listNSSAccounts({ limit: 1, signal: controller.signal }),
			listNSSGroups({ limit: 1, signal: controller.signal })
		])
			.then(([accounts, groups]) => {
				counts = { accounts: accounts.total, groups: groups.total };
			})
			.catch((cause) => {
				if (controller.signal.aborted || redirectIfUnauthenticated(cause)) return;
				gone = cause instanceof ApiError && cause.isNotFound;
				error = gone ? null : errorMessage(cause);
			});
		return () => controller.abort();
	});

	function retry() {
		error = null;
		counts = null;
		attempt += 1;
	}

	/** plural says "1 account", "2 accounts". */
	function plural(count: number, noun: string): string {
		return `${count} ${count === 1 ? noun : `${noun}s`}`;
	}
</script>

<PageSection
	title="Stored entries"
	description="The accounts and groups the sync has stored for hosts, each with what a host's lookup is served now. Read only."
	testid="nss-stored"
	id="stored-entries"
>
	{#if gone}
		<p class="text-dense text-ink-muted">The NSS directory is off on this server.</p>
	{:else if error}
		<Alert
			variant="error"
			title="Could not count the stored entries"
			onretry={retry}
			testid="nss-stored-error">{error}</Alert
		>
	{:else if !counts}
		<LoadingBlock shape="lines" count={1} testid="nss-stored-loading" />
	{:else}
		<p class="text-sm text-ink" data-testid="nss-stored-counts">
			<a
				href={resolve('/admin/nss/accounts')}
				class="text-accent tabular-nums hover:underline"
				data-testid="nss-stored-accounts-link">{plural(counts.accounts, 'stored account')}</a
			>
			and
			<a
				href={resolve('/admin/nss/groups')}
				class="text-accent tabular-nums hover:underline"
				data-testid="nss-stored-groups-link">{plural(counts.groups, 'stored group')}</a
			>.
		</p>
	{/if}
</PageSection>
