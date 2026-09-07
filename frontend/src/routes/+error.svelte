<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import PageHeading from '$lib/components/PageHeading.svelte';
	import PageShell from '$lib/components/PageShell.svelte';
</script>

<PageShell width="focus">
	<div class="rounded-lg border border-border-subtle bg-surface-muted p-4">
		<!-- The same heading every other screen opens with, rather than a
		     hand-written h1. An error page is still a page, and it was one of
		     five that copied the h1's classes out of PageHeading and would
		     have been left behind by a change to it. -->
		<PageHeading eyebrow="Error {page.status}" title="That page isn't here">
			{#snippet sub()}
				{page.error?.message ?? 'The link may be incomplete, or the page may have moved.'}
			{/snippet}
		</PageHeading>
		<p class="mt-4 text-sm">
			<a class="text-accent hover:underline" href={resolve('/dashboard')}>Back to the dashboard</a>
		</p>
	</div>
</PageShell>
