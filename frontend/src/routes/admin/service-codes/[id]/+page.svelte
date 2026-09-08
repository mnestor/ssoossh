<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { ApiError } from '$lib/api/client';
	import {
		expireEnrollment,
		getAdminEnrollmentDetail,
		type AdminEnrollmentDetail
	} from '$lib/api/endpoints';
	import { errorMessage, redirectIfUnauthenticated } from '$lib/auth';
	import AdminServiceCodeDetail from '$lib/components/AdminServiceCodeDetail.svelte';
	import Alert from '$lib/components/Alert.svelte';
	import LoadingBlock from '$lib/components/LoadingBlock.svelte';
	import ExpireCodeAction from '$lib/components/ExpireCodeAction.svelte';
	import PageHeading from '$lib/components/PageHeading.svelte';
	import PageShell from '$lib/components/PageShell.svelte';
	import { isExpired } from '$lib/format';

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

	// An already-expired code needs no control: the outcome it would produce
	// is already true. Read here rather than inside the detail component
	// because retiring is the page's action, not the reading's.
	const expired = $derived(detail ? isExpired(detail.enrollment.expires_at) : false);

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

<PageShell width="wide">
	<!-- The chip always names the list, whatever route reached this page: it
	     is the one place every code is, and an operator who arrived from a
	     log line was nowhere before this. The retire control sits opposite
	     it, the same place an account is disabled from: it is the one thing
	     here that ends the code rather than describing it. -->
	<PageHeading
		title="Service code details"
		back={{ href: listHref, label: 'All service codes', testid: 'admin-service-code-back' }}
	>
		{#snippet action()}
			{#if detail && !expired}
				{@const enrollmentId = detail.enrollment.id}
				<ExpireCodeAction
					testid="admin-expire-code"
					expire={(reason) => expireEnrollment(enrollmentId, reason)}
					onexpired={() => goto(listHref)}
				/>
			{/if}
		{/snippet}
	</PageHeading>

	{#if loadError}
		<Alert variant="error" title="Could not load enrollment">{loadError}</Alert>
	{:else if !hasLoaded}
		<LoadingBlock
			shape="lines"
			count={4}
			label="Loading this enrollment…"
			testid="enrollment-loading"
		/>
	{:else if detail}
		<!-- The detail is handed over rather than fetched again: the
		     endpoint is audited, so a second read would write two
		     admin.enrollment_viewed events for one look. -->
		<AdminServiceCodeDetail {detail} />
	{/if}
</PageShell>
