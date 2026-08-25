<script lang="ts">
	import { page } from '$app/state';
	import { listAdminEnrollments } from '$lib/api/endpoints';
	import type { AdminEnrollment } from '$lib/api/types';
	import { errorMessage, redirectIfUnauthenticated } from '$lib/auth';
	import Alert from '$lib/components/Alert.svelte';
	import AdminServiceCodeDetailModal from '$lib/components/AdminServiceCodeDetailModal.svelte';
	import PageHeading from '$lib/components/PageHeading.svelte';

	let enrollment = $state<AdminEnrollment | null>(null);
	let loadError = $state<string | null>(null);
	let hasLoaded = $state(false);

	// Load the specific enrollment
	$effect(() => {
		const controller = new AbortController();
		hasLoaded = false;
		loadError = null;

		const id = page.params.id;
		if (!id) return;

		// For now, load from the list endpoint and find the enrollment
		// In a real implementation, there would be a dedicated detail endpoint
		listAdminEnrollments(controller.signal, 1000, 0)
			.then((result) => {
				enrollment = result.enrollments.find((e) => e.id === id) || null;
				if (!enrollment) {
					loadError = 'Enrollment not found';
				}
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

	function handleClosed() {
		// Navigate back
		history.back();
	}
</script>

<svelte:head>
	<title>Service code details · Admin · ssoossh</title>
</svelte:head>

<div class="flex w-full max-w-[680px] flex-col gap-5">
	<PageHeading eyebrow="Admin" title="Service code details" />

	{#if loadError}
		<Alert variant="error" title="Could not load enrollment">{loadError}</Alert>
	{:else if !hasLoaded}
		<p class="text-sm text-ink-muted">Loading…</p>
	{:else if enrollment}
		<AdminServiceCodeDetailModal {enrollment} onclosed={handleClosed} />
	{/if}
</div>
