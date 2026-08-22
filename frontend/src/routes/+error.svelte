<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import Card from '$lib/components/Card.svelte';
</script>

<div class="w-full max-w-[560px]">
	<Card title="{page.status} — that page isn't here">
		<p class="text-sm text-ink-muted">
			{page.error?.message ?? 'The link may be incomplete, or the page may have moved.'}
		</p>
		<p class="mt-4 text-sm">
			<a class="text-accent hover:underline" href={resolve('/dashboard')}>Back to the dashboard</a>
		</p>
	</Card>
</div>
