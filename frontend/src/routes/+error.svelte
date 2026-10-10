<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import PageHeading from '#lib/components/PageHeading.svelte';
	import PageShell from '#lib/components/PageShell.svelte';

	/** What one status reads as: the tab title, the heading, and the line
	 * under it when the error carries no message of its own. */
	interface StatusCopy {
		tab: string;
		title: string;
		fallback: string;
	}

	// Every status used to be titled "Page not found", so a server failure
	// told the reader the link was wrong. The cases a reader acts on
	// differently are named; anything else is the server's.
	function describeStatus(status: number): StatusCopy {
		if (status === 404) {
			return {
				tab: 'Page not found',
				title: "That page isn't here",
				fallback: 'The link may be incomplete, or the page may have moved.'
			};
		}
		if (status === 401 || status === 403) {
			return {
				tab: 'No access',
				title: 'You do not have access to this page',
				fallback: 'Your account is not allowed to open it. Ask an administrator if you need it.'
			};
		}
		return {
			tab: 'Could not load the page',
			title: 'This page could not load',
			fallback: 'The server did not answer as expected. Reload the page, or try again shortly.'
		};
	}

	const copy = $derived(describeStatus(page.status));
</script>

<svelte:head><title>{copy.tab} · ssoossh</title></svelte:head>

<PageShell width="focus">
	<div class="rounded-lg bg-surface p-5">
		<!-- The same heading every other screen opens with, rather than a
		     hand-written h1. An error page is still a page, and it was one of
		     five that copied the h1's classes out of PageHeading and would
		     have been left behind by a change to it. -->
		<PageHeading title={copy.title}>
			<!-- The status code used to sit above the title as an eyebrow.
			     It is not the page's name, it is a fact about what went
			     wrong, so it reads as the first half of the sentence that
			     explains it. -->
			{#snippet sub()}
				Error {page.status}. {page.error?.message ?? copy.fallback}
			{/snippet}
		</PageHeading>
		<p class="mt-4 text-sm">
			<a
				class="inline-flex min-h-target items-center text-accent hover:underline"
				href={resolve('dashboard')}
				data-testid="error-dashboard">Back to the dashboard</a
			>
		</p>
	</div>
</PageShell>
