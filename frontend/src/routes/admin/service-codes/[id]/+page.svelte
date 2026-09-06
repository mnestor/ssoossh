<script lang="ts">
	import { page } from '$app/state';
	import { ApiError } from '$lib/api/client';
	import { getAdminEnrollmentDetail, type AdminEnrollmentDetail } from '$lib/api/endpoints';
	import { errorMessage, redirectIfUnauthenticated } from '$lib/auth';
	import Alert from '$lib/components/Alert.svelte';
	import AdminServiceCodeDetailModal from '$lib/components/AdminServiceCodeDetailModal.svelte';
	import PageHeading from '$lib/components/PageHeading.svelte';

	// One enrollment, addressed by id. This is where an operator lands with
	// an id copied out of a notification email, an enrollment.* audit event,
	// or a server log line.
	//
	// It reads the detail endpoint, which resolves any enrollment. It used
	// to ask the list endpoint for a thousand rows and search them in the
	// browser -- and paging.Parse clamps limit to 100 whatever a caller
	// asks for, so it silently stopped resolving anything past the hundred
	// newest codes and reported them as "Enrollment not found".
	let detail = $state<AdminEnrollmentDetail | null>(null);
	let loadError = $state<string | null>(null);
	let hasLoaded = $state(false);

	$effect(() => {
		const controller = new AbortController();
		const id = page.params.id;
		if (!id) {
			loadError = 'No enrollment ID provided';
			hasLoaded = true;
			return;
		}

		hasLoaded = false;
		loadError = null;

		getAdminEnrollmentDetail(id, controller.signal)
			.then((result) => {
				detail = result;
				hasLoaded = true;
			})
			.catch((cause) => {
				if (controller.signal.aborted || redirectIfUnauthenticated(cause)) {
					return;
				}
				// The three answers a reader has to be able to tell apart:
				// no such code, a code they may not see, and a failure.
				if (cause instanceof ApiError && cause.status === 404) {
					loadError = 'No enrollment with that ID.';
				} else if (cause instanceof ApiError && cause.status === 403) {
					loadError = 'You do not have permission to view this enrollment.';
				} else {
					loadError = errorMessage(cause);
				}
				hasLoaded = true;
			});

		return () => controller.abort();
	});

	function handleClosed() {
		// Back to wherever the reader came from, which for a pasted link is
		// out of the app rather than to the list.
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
	{:else if detail}
		<!-- The detail is handed over rather than fetched again: the
		     endpoint is audited, so a second read would write two
		     admin.enrollment_viewed events for one look. -->
		<AdminServiceCodeDetailModal enrollment={detail.enrollment} {detail} onclosed={handleClosed} />
	{/if}
</div>
