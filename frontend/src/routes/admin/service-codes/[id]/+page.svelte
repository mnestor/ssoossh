<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { ApiError } from '$lib/api/client';
	import { getAdminEnrollmentDetail, type AdminEnrollmentDetail } from '$lib/api/endpoints';
	import { errorMessage, redirectIfUnauthenticated } from '$lib/auth';
	import AdminServiceCodeDetail from '$lib/components/AdminServiceCodeDetail.svelte';
	import Alert from '$lib/components/Alert.svelte';
	import Icon from '$lib/components/Icon.svelte';
	import PageHeading from '$lib/components/PageHeading.svelte';
	import PageShell from '$lib/components/PageShell.svelte';

	// One enrollment, addressed by id. This is where an operator lands from
	// the list, and where they land with an id copied out of a notification
	// email, an enrollment.* audit event, or a server log line.
	//
	// It reads the detail endpoint, which resolves any enrollment. It used
	// to ask the list endpoint for a thousand rows and search them in the
	// browser -- and paging.Parse clamps limit to 100 whatever a caller
	// asks for, so it silently stopped resolving anything past the hundred
	// newest codes and reported them as "Enrollment not found".
	let detail = $state<AdminEnrollmentDetail | null>(null);
	let loadError = $state<string | null>(null);
	let hasLoaded = $state(false);

	const listHref = resolve('/admin/service-codes');

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
</script>

<svelte:head>
	<title>Service code details · Admin · ssoossh</title>
</svelte:head>

<PageShell width="default">
	<!-- Always the list, whatever route reached this page: it is the one
	     place every code is, and an operator who arrived from a log line was
	     nowhere before this. -->
	<a
		href={listHref}
		data-testid="admin-service-code-back"
		class="-mb-2 inline-flex w-fit items-center gap-1 text-sm text-accent transition hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
	>
		<Icon name="chevron-left" size="xs" />
		All service codes
	</a>

	<PageHeading eyebrow="Admin" title="Service code details" />

	{#if loadError}
		<Alert variant="error" title="Could not load enrollment">{loadError}</Alert>
	{:else if !hasLoaded}
		<p class="text-sm text-ink-muted">Loading…</p>
	{:else if detail}
		<!-- The detail is handed over rather than fetched again: the
		     endpoint is audited, so a second read would write two
		     admin.enrollment_viewed events for one look. -->
		<AdminServiceCodeDetail {detail} onexpired={() => goto(listHref)} />
	{/if}
</PageShell>
