<script lang="ts">
	import { resolve } from '$app/paths';

	import PageHeading from './PageHeading.svelte';
	import PageShell from './PageShell.svelte';

	// The not-authorized state for a signed-in viewer whose role does not
	// reach a page. The same card the error page gives a 403: a heading, the
	// role the page needs, and a way back to the dashboard. Signing in again
	// cannot grant a role, so this explains rather than redirecting.
	//
	// Display only. The server refuses the page's reads on its own; this is
	// what stands in for a failed fetch.
	interface Props {
		/** The sentence under the heading: which role the page needs. */
		requirement: string;
		/** Root test id; the dashboard link takes `<testid>-dashboard`. */
		testid?: string;
	}

	let { requirement, testid = 'access-denied' }: Props = $props();
</script>

<svelte:head><title>No access · ssoossh</title></svelte:head>

<PageShell width="focus">
	<div data-testid={testid} class="rounded-lg bg-surface p-5">
		<PageHeading title="You do not have access to this page">
			{#snippet sub()}
				{requirement}
			{/snippet}
		</PageHeading>
		<p class="mt-4 text-sm">
			<a
				class="inline-flex min-h-target items-center text-accent hover:underline"
				href={resolve('dashboard')}
				data-testid="{testid}-dashboard">Back to the dashboard</a
			>
		</p>
	</div>
</PageShell>
